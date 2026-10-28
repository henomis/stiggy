// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stiggy

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/llm/middleware"
	"github.com/henomis/phero/v2/memory"
	natsagent "github.com/henomis/phero/v2/nats"
	"github.com/henomis/phero/v2/rag"
	"github.com/henomis/phero/v2/textsplitter"
	"github.com/henomis/phero/v2/textsplitter/markdown"
	"github.com/henomis/phero/v2/textsplitter/recursive"
	"github.com/henomis/phero/v2/vectorstore"

	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

// Defaults of knowledge ingestion.
const (
	defaultChunkSize    = 1000
	defaultChunkOverlap = 100
)

// knowledgeToolPrefix starts the names of knowledge search tools.
const knowledgeToolPrefix = "search_"

// assembly builds the runtime objects of one Run from the fleet: models,
// tools, knowledge indexes and agents, each once, on first use. Agents may
// build memories while jobs run, so it is safe for concurrent use.
type assembly struct {
	fleet *spec.Fleet
	reg   *registry.Registry
	env   registry.Env
	sql   *registry.SQLPool

	// kmu serializes knowledge builds. They run without mu, as a vector
	// store may register its cleanup (Env.Defer), which takes mu.
	kmu sync.Mutex

	mu       sync.Mutex
	models   map[string]llm.LLM
	tools    map[string][]*llm.Tool
	rags     map[string]*rag.RAG
	stores   map[string]vectorstore.Store
	resolver *natsagent.Resolver
	cleanups []func() error
}

func newAssembly(a *App) *assembly {
	s := &assembly{
		fleet:  a.fleet,
		reg:    a.opts.registry,
		sql:    registry.NewSQLPool(),
		models: map[string]llm.LLM{},
		tools:  map[string][]*llm.Tool{},
		rags:   map[string]*rag.RAG{},
		stores: map[string]vectorstore.Store{},
	}

	s.env = registry.Env{Conn: a.nc, Logger: a.logger, SQL: s.sql.Open, Defer: s.addCleanup}

	return s
}

func (s *assembly) addCleanup(f func() error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.cleanups = append(s.cleanups, f)
}

// close runs the cleanups, newest first, then closes the SQL pool.
func (s *assembly) close() error {
	s.mu.Lock()
	cleanups := s.cleanups
	s.cleanups = nil
	s.mu.Unlock()

	var errs []error

	for i := len(cleanups) - 1; i >= 0; i-- {
		errs = append(errs, cleanups[i]())
	}

	return errors.Join(append(errs, s.sql.Close())...)
}

func (s *assembly) model(name string) (llm.LLM, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if l, ok := s.models[name]; ok {
		return l, nil
	}

	m := s.fleet.Models[name]

	l, err := s.reg.BuildModel(name, m)
	if err != nil {
		return nil, err
	}

	if l, err = s.withMiddleware(name, m, l); err != nil {
		return nil, err
	}

	s.models[name] = l

	return l, nil
}

// unboundedConcurrency stands for "no bound" in phero's limiter, which
// needs a positive concurrency.
const unboundedConcurrency = 1 << 16

// concurrencyLimit bounds the calls in flight, without bounding their rate.
// phero's limiter always bounds the rate, refilling its bucket on a ticker,
// so "no rate" cannot be expressed as a very high one.
func concurrencyLimit(n int) llm.Middleware {
	slots := make(chan struct{}, n)

	return func(next llm.LLM) llm.LLM {
		return llm.Func(func(ctx context.Context, msgs []llm.Message, opts ...llm.CallOption) (*llm.Result, error) {
			select {
			case slots <- struct{}{}:
			case <-ctx.Done():
				return nil, ctx.Err()
			}

			defer func() { <-slots }()

			return next.Execute(ctx, msgs, opts...)
		})
	}
}

// secondsPerMinute converts requests per minute to the limiter's rate.
const secondsPerMinute = 60

// withMiddleware wraps a model with its rate limit and retries. The caller
// holds s.mu.
func (s *assembly) withMiddleware(name string, m spec.Model, l llm.LLM) (llm.LLM, error) {
	var mws []llm.Middleware

	switch r := m.RateLimit; {
	case r == nil:
	case r.RequestsPerMinute > 0:
		conc := r.MaxConcurrent
		if conc == 0 {
			conc = unboundedConcurrency
		}

		mw, stop, err := middleware.NewLimiter(r.RequestsPerMinute/secondsPerMinute, conc)
		if err != nil {
			return nil, fmt.Errorf("model %q: rate_limit: %w", name, err)
		}

		s.cleanups = append(s.cleanups, func() error { stop(); return nil })
		mws = append(mws, mw)
	case r.MaxConcurrent > 0:
		mws = append(mws, concurrencyLimit(r.MaxConcurrent))
	}

	if m.Retries > 0 {
		opts := []middleware.RetryOption{middleware.WithShouldRetry(transient)}
		if m.RetryBackoff > 0 {
			opts = append(opts, middleware.WithInitialBackoff(time.Duration(m.RetryBackoff)))
		}

		mw, err := middleware.NewRetry(m.Retries+1, opts...)
		if err != nil {
			return nil, fmt.Errorf("model %q: retries: %w", name, err)
		}

		mws = append(mws, mw)
	}

	if len(mws) == 0 {
		return l, nil
	}

	return llm.Use(l, mws...), nil
}

// transient reports whether a failed model call may succeed when retried. A
// provider's answer is judged by phero (a timeout, a conflict, a rate limit
// or a 5xx is worth another try; a bad request, a bad key or a prompt too
// long for the context window is not); any other error, a network failure
// included, is retried, unless it is a guardrail block.
func transient(err error) bool {
	if _, ok := errors.AsType[*llm.ProviderError](err); ok {
		return llm.IsRetryable(err)
	}

	return !errors.Is(err, llm.ErrGuardrailBlocked)
}

func (s *assembly) tool(ctx context.Context, name string) ([]*llm.Tool, error) {
	s.mu.Lock()
	ts, ok := s.tools[name]
	s.mu.Unlock()

	if ok {
		return ts, nil
	}

	// Built outside the lock: an MCP server may take a while to start.
	ts, err := s.reg.BuildTool(ctx, name, s.fleet.Tools[name], s.env)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.tools[name] = ts
	s.mu.Unlock()

	return ts, nil
}

func (s *assembly) knowledge(ctx context.Context, name string) (*rag.RAG, error) {
	s.kmu.Lock()
	defer s.kmu.Unlock()

	s.mu.Lock()
	r, ok := s.rags[name]
	s.mu.Unlock()

	if ok {
		return r, nil
	}

	k := s.fleet.Knowledge[name]

	emb, err := s.reg.BuildEmbedder(k.Embedder, s.fleet.Embedders[k.Embedder])
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	store, ok := s.stores[k.VectorStore]
	s.mu.Unlock()

	if !ok {
		store, err = s.reg.BuildVectorStore(ctx, k.VectorStore, s.fleet.VectorStores[k.VectorStore], s.env)
		if err != nil {
			return nil, err
		}

		s.mu.Lock()
		s.stores[k.VectorStore] = store
		s.mu.Unlock()
	}

	var opts []rag.Option
	if k.TopK > 0 {
		opts = append(opts, rag.WithTopK(k.TopK))
	}

	r, err = rag.New(store, emb, opts...)
	if err != nil {
		return nil, fmt.Errorf("knowledge %q: %w", name, err)
	}

	s.mu.Lock()
	s.rags[name] = r
	s.mu.Unlock()

	return r, nil
}

// agent builds a worker agent with its tools, knowledge tools, memory and
// delegates. The compiler guarantees delegation is acyclic.
func (s *assembly) agent(ctx context.Context, name string) (agentrun.Agent, error) {
	as := s.fleet.Agents[name]

	l, err := s.model(as.Model)
	if err != nil {
		return agentrun.Agent{}, err
	}

	out := agentrun.Agent{Name: name, Spec: as, LLM: l}
	names := toolNames{agent: name, owners: map[string]string{}}

	if as.Todo {
		if err = names.claim("todo", agentrun.TodoToolName); err != nil {
			return agentrun.Agent{}, err
		}
	}

	if as.Compaction != nil {
		if out.Compactor, err = s.compactor(*as.Compaction, l); err != nil {
			return agentrun.Agent{}, err
		}
	}

	if err = s.agentTools(ctx, as, &out, names); err != nil {
		return agentrun.Agent{}, err
	}

	for _, k := range as.Knowledge {
		t, kerr := s.knowledgeTool(ctx, k)
		if kerr != nil {
			return agentrun.Agent{}, kerr
		}

		if kerr = names.add(fmt.Sprintf("knowledge %q", k), t); kerr != nil {
			return agentrun.Agent{}, kerr
		}

		out.Tools = append(out.Tools, t)
	}

	if err = s.delegates(ctx, as, &out, names); err != nil {
		return agentrun.Agent{}, err
	}

	if as.Memory.IsLongTerm() {
		out.Memory = s.memoryFactory(as.Memory)
	}

	return out, nil
}

// agentTools adds an agent's tool entries to out, under its permissions.
func (s *assembly) agentTools(ctx context.Context, as spec.Agent, out *agentrun.Agent, names toolNames) error {
	guard, err := toolGuard(as.Permissions)
	if err != nil {
		return fmt.Errorf("agent %q: %w", out.Name, err)
	}

	for _, t := range as.Tools {
		ts, terr := s.tool(ctx, t)
		if terr != nil {
			return terr
		}

		if terr = names.add(fmt.Sprintf("tool %q", t), ts...); terr != nil {
			return terr
		}

		if ts, terr = guard(ts); terr != nil {
			return terr
		}

		out.Tools = append(out.Tools, ts...)
	}

	return nil
}

// compactor builds the summary compactor of cp; the agent's own model, own,
// writes the summaries unless cp names another.
func (s *assembly) compactor(cp spec.Compaction, own llm.LLM) (agent.Compactor, error) {
	l := own

	if cp.Model != "" {
		var err error
		if l, err = s.model(cp.Model); err != nil {
			return nil, err
		}
	}

	var opts []agent.SummaryCompactorOption

	if cp.KeepRecent > 0 {
		opts = append(opts, agent.WithSummaryKeepRecent(cp.KeepRecent))
	}

	if cp.KeepTokens > 0 {
		opts = append(opts, agent.WithSummaryKeepTokens(cp.KeepTokens))
	}

	if cp.MaxSummaryTokens > 0 {
		opts = append(opts, agent.WithSummaryMaxTokens(cp.MaxSummaryTokens))
	}

	if cp.Prompt != "" {
		opts = append(opts, agent.WithSummaryPrompt(cp.Prompt))
	}

	return agent.NewSummaryCompactor(l, opts...), nil
}

// delegates adds an agent's delegates to out: remote ones as tools, agents
// as delegate agents, which agentrun turns into ask_<name> tools.
func (s *assembly) delegates(ctx context.Context, as spec.Agent, out *agentrun.Agent, names toolNames) error {
	for _, d := range as.Delegates {
		if rm, remote := s.fleet.Remotes[d]; remote {
			t, rerr := s.remoteTool(d, rm)
			if rerr != nil {
				return rerr
			}

			if rerr = names.add(fmt.Sprintf("delegate %q", d), t); rerr != nil {
				return rerr
			}

			out.Tools = append(out.Tools, t)

			continue
		}

		// agentrun gives a delegate agent this tool name.
		if err := names.claim(fmt.Sprintf("delegate %q", d), "ask_"+agent.SanitizeToolName(d)); err != nil {
			return err
		}

		da, derr := s.agent(ctx, d)
		if derr != nil {
			return derr
		}

		out.Delegates = append(out.Delegates, da)
	}

	return nil
}

// toolNames checks an agent's tool names are distinct when the app starts:
// phero would only reject a duplicate on the agent's first step. Names are
// what the model sees, not the keys of the fleet's tools, so two entries of
// one type (two file_read tools, say) both provide "read".
type toolNames struct {
	agent  string
	owners map[string]string // tool name → the source that provides it
}

// add claims the names of tools for source.
func (n toolNames) add(source string, tools ...*llm.Tool) error {
	for _, t := range tools {
		if err := n.claim(source, t.Name()); err != nil {
			return err
		}
	}

	return nil
}

// claim records that source provides the tool name.
func (n toolNames) claim(source, name string) error {
	if first, taken := n.owners[name]; taken {
		return fmt.Errorf("%w: agent %q: %s and %s both provide %q",
			ErrToolClash, n.agent, first, source, name)
	}

	n.owners[name] = source

	return nil
}

// agentResolver returns the fleet's resolver of protocol agents, creating it
// on first use. It tracks heartbeats until the app stops.
func (s *assembly) agentResolver() (*natsagent.Resolver, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.resolver != nil {
		return s.resolver, nil
	}

	r, err := natsagent.NewResolver(natsagent.NewClient(s.env.Conn))
	if err != nil {
		return nil, fmt.Errorf("stiggy: agent resolver: %w", err)
	}

	s.resolver = r
	s.cleanups = append(s.cleanups, r.Close)

	return r, nil
}

// remoteTool exposes a remote agent as a delegate tool. Calls are resolved
// on each use, so a restarted or moved agent is found again.
func (s *assembly) remoteTool(name string, rm spec.Remote) (*llm.Tool, error) {
	r, err := s.agentResolver()
	if err != nil {
		return nil, err
	}

	desc := "Ask agent " + name + " to do a task and return its answer."
	if rm.Description != "" {
		desc += " " + rm.Description
	}

	t, err := r.AsTool(rm.Owner, rm.Name, "ask_"+agent.SanitizeToolName(name), desc)
	if err != nil {
		return nil, fmt.Errorf("remote %q: %w", name, err)
	}

	return t, nil
}

func (s *assembly) knowledgeTool(ctx context.Context, name string) (*llm.Tool, error) {
	r, err := s.knowledge(ctx, name)
	if err != nil {
		return nil, err
	}

	desc := "Search the " + name + " knowledge base and return the most relevant passages."
	if d := s.fleet.Knowledge[name].Description; d != "" {
		desc += " It contains: " + d
	}

	t, err := r.AsTool(knowledgeToolPrefix+name, desc)
	if err != nil {
		return nil, fmt.Errorf("knowledge %q: %w", name, err)
	}

	return t, nil
}

func (s *assembly) memoryFactory(m spec.Memory) agentrun.MemoryFactory {
	return func(ctx context.Context, session string) (memory.Memory, error) {
		return s.reg.BuildMemory(ctx, m, registry.MemoryEnv{
			Env:       s.env,
			Session:   session,
			Knowledge: func(name string) (*rag.RAG, error) { return s.knowledge(ctx, name) },
		})
	}
}

// ingest fills every knowledge index whose store is empty from its sources.
// It runs in the engine role, so a fleet ingests once however many workers
// it has.
func (s *assembly) ingest(ctx context.Context, logger interface{ Info(string, ...any) }) error {
	for _, name := range sortedKeys(s.fleet.Knowledge) {
		k := s.fleet.Knowledge[name]
		if len(k.Sources) == 0 {
			continue
		}

		r, err := s.knowledge(ctx, name)
		if err != nil {
			return err
		}

		s.mu.Lock()
		store := s.stores[k.VectorStore]
		s.mu.Unlock()

		// A missing collection counts as empty; Ingest creates it.
		if n, cerr := store.Count(ctx); cerr == nil && n > 0 {
			logger.Info("stiggy: knowledge already ingested", "knowledge", name, "documents", n)

			continue
		}

		for _, src := range k.Sources {
			if err = ingestSource(ctx, r, src); err != nil {
				return fmt.Errorf("knowledge %q: %w", name, err)
			}
		}

		logger.Info("stiggy: knowledge ingested", "knowledge", name)
	}

	return nil
}

func ingestSource(ctx context.Context, r *rag.RAG, src spec.Source) error {
	files, err := filepath.Glob(src.Path)
	if err != nil {
		return fmt.Errorf("source %q: %w", src.Path, err)
	}

	if len(files) == 0 {
		return fmt.Errorf("source %q: %w", src.Path, errNoFiles)
	}

	size, overlap := src.ChunkSize, src.ChunkOverlap
	if size == 0 {
		size, overlap = defaultChunkSize, defaultChunkOverlap
	}

	for _, f := range files {
		var sp textsplitter.Splitter

		md := strings.EqualFold(filepath.Ext(f), ".md")

		switch {
		case src.Splitter == compile.SplitterMarkdown || (src.Splitter == "" && md):
			sp = markdown.New(f, size, overlap)
		default:
			sp = recursive.New(f, size, overlap)
		}

		if err = r.Ingest(ctx, sp); err != nil {
			return fmt.Errorf("ingest %s: %w", f, err)
		}
	}

	return nil
}

var errNoFiles = errors.New("no files match")
