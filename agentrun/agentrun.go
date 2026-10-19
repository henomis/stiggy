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
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/internal/jobctx"
	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/spec"
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

// WithLogger sets the logger of an exposed agent's runs (default
// slog.Default()). Flow steps log through their job's logger.
func WithLogger(l *slog.Logger) Option {
	return func(r *runner) { r.logger = l }
}

// WithTracing sets the provider of the runs' spans. It should include
// NewLogProcessor, or the debug lines are lost. Without it, spans are only
// logged.
func WithTracing(p *trace.Provider) Option {
	return func(r *runner) { r.tracing = p }
}

type runner struct {
	a        Agent
	flows    *flowCache
	sessions *sessionCache
	logger   *slog.Logger
	tracing  *trace.Provider
}

func newRunner(a Agent, opts []Option) *runner {
	r := &runner{a: a}
	for _, opt := range opts {
		opt(r)
	}

	if r.tracing == nil {
		r.tracing = defaultProvider()
	}

	return r
}

// Handler returns the worker handler of a.
func Handler(a Agent, opts ...Option) worker.Handler {
	r := newRunner(a, opts)
	if a.Memory != nil {
		r.sessions = &sessionCache{build: a.Memory}
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

	// The step's span is the parent of the agent's run: its totals are what
	// the attempt spent, delegates included, however the run ends.
	ctx = stepContext(ctx, r.tracing, job, r.a.Name)
	ctx, span := trace.Start(ctx, spanStep+" "+job.Node, &trace.Custom{Type: spanStep, Data: stepData(job)})
	obs := observe(ctx, span)

	if job.Traceparent == "" {
		obs.rep.Add(slog.String(trace.LogKeyTraceID, span.Context().TraceID.String()))
	}

	obs.review(h, job)

	res, err := r.attempt(ctx, job, step, schema, h, obs)
	span.End(endErr(err))

	return res, err
}

// attempt runs the step's agent once, under the step's span.
func (r *runner) attempt(ctx context.Context, job *worker.Job, step spec.Step, schema map[string]any, h *hitl,
	obs *observer,
) (*worker.Result, error) {
	if p := h.approved; p != nil {
		obs.routed(step, p.Route, p.RouteReason)

		return h.finish(step, p.Output, p.Route, nil, memoryWrite(r.a.Name, p.Memory))
	}

	data, prompt, err := stepPrompt(job, step, schema)
	if err != nil {
		return nil, err
	}

	runCtx := jobctx.With(ctx, jobInfo(job, r.a.Name))
	ctl := &control{}

	mem, cm, err := r.memoryFor(ctx, job, data)
	if err != nil {
		return nil, err
	}

	ag, err := r.build(job.Node, step, schema, data, ctl)
	if err != nil {
		return nil, worker.Permanent(err)
	}

	if mem != nil {
		ag.SetMemory(mem)
	}

	// Every way this run ends reports what it spent: a completion through
	// Result.Usage, an interrupt or a failure through worker.WithUsage.
	res, err := r.runAgent(runCtx, job, ag, prompt+h.transcript())

	totals := obs.span.Totals()
	usage := usageOf(&totals)
	obs.run(res, err, &totals)

	if q := ctl.question(); q != "" {
		return nil, worker.WithUsage(h.interrupt(&pending{Kind: kindQuestion, Text: q}, r.a.Name, job.Node), usage)
	}

	if err != nil {
		return nil, worker.WithUsage(classify(err, maxIterations(r.a.Spec)), usage)
	}

	route, reason := ctl.route()
	obs.routed(step, route, reason)

	output, err := answerOutput(res.TextContent(), schema, route != "")
	if err != nil {
		return nil, worker.WithUsage(err, usage)
	}

	if step.HumanInput {
		p := &pending{
			Kind: kindReview, Text: res.TextContent(), Output: output, Route: route, RouteReason: reason,
			Memory: cm.turn(),
		}

		return nil, worker.WithUsage(h.interrupt(p, r.a.Name, job.Node), usage)
	}

	result, err := h.finish(step, output, route, usage, memoryWrite(r.a.Name, cm.turn()))

	return result, worker.WithUsage(err, usage)
}

// jobInfo describes a job for its tools (stiggy.JobFromContext). The step
// id is packtrail's task key and generation: a retry reschedules the task
// with the same generation and the next attempt, while a revisit or a
// resume allocates a new generation.
func jobInfo(job *worker.Job, agentName string) jobctx.Job {
	return jobctx.Job{
		ExecID: job.ExecID, Flow: job.Flow, Node: job.Node, Agent: agentName,
		StepID:  job.ExecID + "/" + job.Key + "/" + strconv.Itoa(job.Generation),
		Attempt: job.Attempt, Index: job.Context.Index, Input: job.Context.Input, Item: job.Context.Item,
		Channels: visibleChannels(job.Context.Channels), Store: job.Store(),
		Logger: job.Logger().With("agent", agentName),
	}
}

// visibleChannels returns the channels a tool may read: all but the agents'
// hidden memory channels.
func visibleChannels(channels map[string]json.RawMessage) map[string]json.RawMessage {
	out := maps.Clone(channels)
	maps.DeleteFunc(out, func(name string, _ json.RawMessage) bool {
		return strings.HasPrefix(name, spec.MemoryChannelPrefix)
	})

	return out
}

// stepPrompt returns the job context and the step's prompt, with the
// output schema's instruction when the step has one.
func stepPrompt(job *worker.Job, step spec.Step, schema map[string]any) (map[string]any, string, error) {
	data, err := stepio.Data(job)
	if err != nil {
		return nil, "", err
	}

	prompt, err := Prompt(job, step)
	if err != nil {
		return nil, "", worker.Permanent(err)
	}

	if schema != nil {
		instr, serr := schemaInstruction(schema)
		if serr != nil {
			return nil, "", serr
		}

		prompt += instr
	}

	return data, prompt, nil
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
func (r *runner) build(node string, step spec.Step, schema, data map[string]any, ctl *control,
) (*agent.Agent, error) {
	system, err := SystemPrompt(r.a.Name, r.a.Spec, data)
	if err != nil {
		return nil, err
	}

	ag, err := agent.New(r.a.LLM, r.a.Name, system+ctl.instructions(step))
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
	}

	steering, err := ctl.tools(step)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", r.a.Name, err)
	}

	delegates, err := delegateTools(r.a.Delegates, data)
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
// its role, goal and backstory. Each is a template over data, the job
// context (see spec.ParsePrompt); nil data is for agents that run outside
// any job, whose fields the compiler keeps static.
func SystemPrompt(name string, s spec.Agent, data map[string]any) (string, error) {
	var parts []string

	for _, f := range [][3]string{
		{"system", "", s.System},
		{"role", "Role: ", s.Role},
		{"goal", "Goal: ", s.Goal},
		{"backstory", "Backstory: ", s.Backstory},
	} {
		if f[2] == "" {
			continue
		}

		text, err := render(f[0], f[2], data)
		if err != nil {
			return "", fmt.Errorf("agent %q: %w", name, err)
		}

		parts = append(parts, f[1]+text)
	}

	if len(parts) == 0 {
		return "You are " + name + ", a helpful agent.", nil
	}

	return strings.Join(parts, "\n\n"), nil
}

// maxIterations is the iteration limit of an agent built from s.
func maxIterations(s spec.Agent) int {
	if s.MaxIterations > 0 {
		return s.MaxIterations
	}

	return agent.DefaultMaxIterations
}

// classify marks the errors no retry can fix as permanent. Everything else,
// rate limits included, is left to the node's retry policy (the
// provider's retry-after is reported, not passed on). maxIterations is the
// agent's limit, for the message of a run that reached it.
func classify(err error, maxIterations int) error {
	if errors.Is(err, agent.ErrMaxIterationsReached) {
		return worker.Permanent(fmt.Errorf("%s: reached %d: %w", trace.StopMaxIterations, maxIterations, err))
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
