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

package agentrun

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"time"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
	"github.com/henomis/phero/v2/trace"
)

// exposed serves an agent on the NATS Agent Protocol.
type exposed struct {
	a        Agent
	sessions *sessionCache
	logger   *slog.Logger
	tracing  *trace.Provider
}

// Exposed returns a protocol handler (for phero's nats.Server) that answers
// each prompt with a fresh run of a: its tools, knowledge and delegates, and
// its long-term memory when it has one (one session for all callers; a
// per-execution memory has no execution here and uses that same session).
// Execution memory, routes and human input belong to flow steps and do not
// apply. Of the options, only WithLogger and WithTracing apply.
func Exposed(a Agent, opts ...Option) natsagent.Handler {
	r := newRunner(a, opts)

	e := &exposed{a: a, logger: r.logger, tracing: r.tracing}
	if e.logger == nil {
		e.logger = slog.Default()
	}

	if a.Memory != nil {
		e.sessions = &sessionCache{build: a.Memory}
	}

	return e
}

// Request outcomes, as logged.
const (
	outcomeCompleted = "completed"
	outcomeFailed    = "failed"
)

// Run implements natsagent.Handler. Every request logs a start and an end
// line at info, on the agent's name, the caller's step (when a stiggy remote
// step sent it) and the request's trace; the end carries the outcome, the
// duration, the stop reason and the usage, delegates included, and a failed
// request ends at warn with err.
func (e *exposed) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	ctx = trace.WithProvider(ctx, e.tracing)
	ctx, span := trace.Start(ctx, spanRequest+" "+e.a.Name, &trace.Custom{Type: spanRequest})

	log := e.logger.With(append([]any{keyAgent, e.a.Name}, callerAttrs(ctx)...)...)
	log = log.With(trace.LogKeyTraceID, span.Context().TraceID.String())
	ctx = withLogger(ctx, log)

	log.InfoContext(ctx, "stiggy: request started")

	began := time.Now()
	res, err := e.run(ctx, parts...)
	totals := span.Totals()
	span.End(err)

	outcome, level := outcomeCompleted, slog.LevelInfo
	if err != nil {
		outcome, level = outcomeFailed, slog.LevelWarn
	}

	attrs := []any{"outcome", outcome, keyDuration, time.Since(began)}

	switch {
	case res != nil && res.Summary != nil:
		attrs = append(attrs, keyStop, res.Summary.StopReason)
	case err != nil:
		attrs = append(attrs, keyStop, stopReasonOf(err))
	}

	usage := usageOf(&totals)
	for _, k := range []string{CounterTokensIn, CounterTokensOut, CounterCostUSD, CounterLLMCalls, CounterToolCalls} {
		if v, ok := usage[k]; ok {
			attrs = append(attrs, k, v)
		}
	}

	if err != nil {
		attrs = append(attrs, "err", err)
	}

	log.Log(ctx, level, "stiggy: request ended", attrs...)

	return res, err
}

// callerAttrs are the log attributes of the step that sent the request,
// from the headers a stiggy remote step sends; none for other callers.
func callerAttrs(ctx context.Context) []any {
	req, ok := natsagent.RequestFrom(ctx)
	if !ok || req.Header == nil {
		return nil
	}

	var attrs []any

	for _, h := range [...][2]string{
		{HeaderExec, "caller_exec"},
		{HeaderFlow, "caller_flow"},
		{HeaderNode, "caller_node"},
		{HeaderAttempt, "caller_attempt"},
	} {
		if v := req.Header.Get(h[0]); v != "" {
			attrs = append(attrs, h[1], v)
		}
	}

	return attrs
}

// run answers one request with a fresh agent; its spans, and its
// delegates', are children of the request's.
func (e *exposed) run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	// An exposed agent has no job context: the compiler keeps its system
	// prompt and its delegates' static.
	system, err := SystemPrompt(e.a.Name, e.a.Spec, nil)
	if err != nil {
		return nil, err
	}

	ag, err := agent.New(e.a.LLM, e.a.Name, system)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
	}

	delegates, err := delegateTools(e.a.Delegates, nil)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
	}

	for _, t := range slices.Concat(e.a.Tools, delegates) {
		if err = ag.AddTool(t); err != nil {
			return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
		}
	}

	e.a.limit(ag)

	if e.sessions != nil {
		mem := e.a.Spec.Memory
		mem.PerExecution = false

		// An exposed agent has no job context: the compiler only lets a
		// static session through.
		session, serr := SessionOf(e.a.Name, mem, nil, "")
		if serr != nil {
			return nil, fmt.Errorf("agent %q: memory: %w", e.a.Name, serr)
		}

		m, merr := e.sessions.get(ctx, session)
		if merr != nil {
			return nil, fmt.Errorf("agent %q: memory: %w", e.a.Name, merr)
		}

		ag.SetMemory(m)
	}

	return ag.Run(ctx, parts...)
}
