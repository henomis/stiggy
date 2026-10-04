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

// Package agentrun runs a phero agent as a packtrail worker. Each job gets a
// fresh agent built from the agent's spec, so concurrent jobs share nothing
// mutable; the job's step (from the node's meta) decides the prompt, the
// tools that steer the workflow (routes, ask_human), the output shape
// (output_schema) and how the answer flows back into the execution.
package agentrun

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"sync"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/spec"
)

// Usage counters every agent step reports. Flows cap them with `budget:`.
const (
	CounterAgentSteps = "agent_steps"
	CounterTokensIn   = "tokens_in"
	CounterTokensOut  = "tokens_out"
	CounterCostUSD    = "cost_usd"
)

// ErrAgentMismatch is returned when a job's step names another agent than
// the one the worker runs: the worker kind and the meta disagree.
var ErrAgentMismatch = errors.New("agentrun: the step names another agent")

// Agent is everything needed to build the agent for a job.
type Agent struct {
	Name  string
	Spec  spec.Agent
	LLM   llm.LLM
	Tools []*llm.Tool
	// Memory builds the long-term memory of a session, when the spec's
	// memory is one (not none or execution).
	Memory MemoryFactory
	// Delegates are the agents this one may call as tools.
	Delegates []Agent
}

// FlowSource returns a registered flow version; *packtrail.Client is one.
// The runner reads node settings that live outside the step meta, such as
// output_schema.
type FlowSource interface {
	Flow(ctx context.Context, name, version string) (*flow.Flow, error)
}

// Option configures a handler.
type Option func(*runner)

// WithFlows sets where the runner reads flow definitions. Without it,
// output_schema is not passed to the model and answers are plain text.
func WithFlows(src FlowSource) Option {
	return func(r *runner) { r.flows = newFlowCache(src) }
}

type runner struct {
	a        Agent
	flows    *flowCache
	sessions *sessionCache
}

// Handler returns the worker handler of a.
func Handler(a Agent, opts ...Option) worker.Handler {
	r := &runner{a: a}
	if a.Memory != nil {
		r.sessions = &sessionCache{build: a.Memory}
	}

	for _, opt := range opts {
		opt(r)
	}

	return r.handle
}

func (r *runner) handle(ctx context.Context, job *worker.Job) (*worker.Result, error) {
	step, err := stepio.Step(job)
	if err != nil {
		return nil, err
	}

	if step.Agent != r.a.Name {
		return nil, worker.Permanent(fmt.Errorf("%w: %q, worker runs %q", ErrAgentMismatch, step.Agent, r.a.Name))
	}

	schema, err := r.flows.schema(ctx, job)
	if err != nil {
		return nil, err
	}

	h, err := openHITL(job)
	if err != nil {
		return nil, err
	}

	if p := h.approved; p != nil {
		return h.finish(step, p.Output, p.Route, nil, memoryWrite(r.a.Name, p.Memory))
	}

	prompt, err := Prompt(job, step)
	if err != nil {
		return nil, worker.Permanent(err)
	}

	if schema != nil {
		instr, serr := schemaInstruction(schema)
		if serr != nil {
			return nil, serr
		}

		prompt += instr
	}

	runCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)

	ctl := &control{cancel: cancel}

	mem, cm, err := r.memoryFor(ctx, job)
	if err != nil {
		return nil, err
	}

	meter := &usageMeter{}

	ag, err := r.build(job.Node, step, schema, ctl, meter)
	if err != nil {
		return nil, worker.Permanent(err)
	}

	if mem != nil {
		ag.SetMemory(mem)
	}

	// Every way this run ends reports what it spent: a completion through
	// Result.Usage, an interrupt or a failure through worker.WithUsage.
	tr := &summaryTracer{}
	ag.SetTracer(tr)

	res, err := r.runAgent(runCtx, job, ag, prompt+h.transcript())
	usage := addUsage(usageOf(tr.summary()), meter.total())

	if q := ctl.question(); q != "" {
		return nil, worker.WithUsage(h.interrupt(&pending{Kind: kindQuestion, Text: q}, r.a.Name, job.Node), usage)
	}

	if err != nil {
		return nil, worker.WithUsage(classify(err), usage)
	}

	output, err := parseOutput(res.TextContent(), schema)
	if err != nil {
		return nil, worker.WithUsage(err, usage)
	}

	if step.HumanInput {
		p := &pending{
			Kind: kindReview, Text: res.TextContent(), Output: output, Route: ctl.route(), Memory: cm.turn(),
		}

		return nil, worker.WithUsage(h.interrupt(p, r.a.Name, job.Node), usage)
	}

	result, err := h.finish(step, output, ctl.route(), usage, memoryWrite(r.a.Name, cm.turn()))

	return result, worker.WithUsage(err, usage)
}

// memoryWrite returns the channel write that appends a run's turn to
// execution memory.
func memoryWrite(agentName string, turn []llm.Message) map[string]any {
	if len(turn) == 0 {
		return nil
	}

	return map[string]any{spec.MemoryChannel(agentName): turn}
}

// build makes a fresh phero agent for one job.
func (r *runner) build(node string, step spec.Step, schema map[string]any, ctl *control, meter *usageMeter,
) (*agent.Agent, error) {
	ag, err := agent.New(r.a.LLM, r.a.Name, SystemPrompt(r.a.Name, r.a.Spec)+ctl.instructions(step))
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
	}

	steering, err := ctl.tools(step)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
	}

	delegates, err := delegateTools(r.a.Delegates, meter)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
	}

	all := slices.Concat(r.a.Tools, delegates, steering)

	for _, t := range all {
		if err = ag.AddTool(t); err != nil {
			return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
		}
	}

	// Unset keeps phero's default (agent.DefaultMaxIterations).
	if r.a.Spec.MaxIterations > 0 {
		ag.SetMaxIterations(r.a.Spec.MaxIterations)
	}

	if schema != nil {
		f, ferr := responseFormat(node, schema)
		if ferr != nil {
			return nil, ferr
		}

		ag.SetResponseFormat(f)
	}

	return ag, nil
}

// runAgent runs the agent, streaming its progress when the agent asks for it.
func (r *runner) runAgent(ctx context.Context, job *worker.Job, ag *agent.Agent, prompt string) (*agent.Result, error) {
	if !r.a.Spec.Stream {
		return ag.Run(ctx, llm.Text(prompt))
	}

	return runStreaming(ctx, job, r.a.Name, ag, prompt)
}

// SystemPrompt composes an agent's system prompt from its system text and
// its role, goal and backstory.
func SystemPrompt(name string, s spec.Agent) string {
	var parts []string

	if s.System != "" {
		parts = append(parts, s.System)
	}

	for _, kv := range [][2]string{{"Role", s.Role}, {"Goal", s.Goal}, {"Backstory", s.Backstory}} {
		if kv[1] != "" {
			parts = append(parts, kv[0]+": "+kv[1])
		}
	}

	if len(parts) == 0 {
		return "You are " + name + ", a helpful agent."
	}

	return strings.Join(parts, "\n\n")
}

// classify marks the errors no retry can fix as permanent. Everything else,
// rate limits included, is left to the node's retry policy (the
// provider's retry-after cannot be passed on).
func classify(err error) error {
	if errors.Is(err, agent.ErrMaxIterationsReached) {
		return worker.Permanent(err)
	}

	var pe *llm.ProviderError
	if errors.As(err, &pe) && pe.StatusCode >= http.StatusBadRequest && pe.StatusCode < http.StatusInternalServerError {
		switch pe.StatusCode {
		case http.StatusRequestTimeout, http.StatusConflict, http.StatusTooManyRequests:
			return err
		default:
			return worker.Permanent(err)
		}
	}

	return err
}

// summaryTracer keeps the summary phero traces at the end of every run,
// including runs that fail before returning a result.
type summaryTracer struct {
	mu   sync.Mutex
	last *trace.RunSummary
}

func (t *summaryTracer) Trace(ev trace.Event) {
	if e, ok := ev.(trace.AgentRunSummaryEvent); ok {
		t.mu.Lock()
		t.last = &e.Summary
		t.mu.Unlock()
	}
}

func (t *summaryTracer) summary() *trace.RunSummary {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}

// usageOf returns the counters of one agent run from its summary, which is
// nil when the run never started.
func usageOf(sum *trace.RunSummary) map[string]float64 {
	u := map[string]float64{CounterAgentSteps: 1}

	if sum == nil {
		return u
	}

	s := sum.Usage
	u[CounterTokensIn] = float64(s.InputTokens)
	u[CounterTokensOut] = float64(s.OutputTokens)

	if s.CostUSD > 0 {
		u[CounterCostUSD] = s.CostUSD
	}

	return u
}
