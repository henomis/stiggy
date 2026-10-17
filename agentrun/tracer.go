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
	"log/slog"
	"sync"
	"time"

	"github.com/henomis/phero/v2/agent"
	phtrace "github.com/henomis/phero/v2/trace"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// maxLogged bounds the model text, tool arguments and tool results a debug
// line carries.
const maxLogged = 512

// Log keys of the debug lines.
const (
	keyAgent     = "agent"
	keyIteration = "iteration"
	keyDuration  = "duration"
)

// runTracer receives the trace events of phero runs: a step agent's, an
// exposed agent's, or every delegate run of one job. It
//
//   - keeps their summaries, which become the step's usage and summary;
//   - turns them into spans (invoke_agent, chat, execute_tool) under the
//     run's context, and into metrics;
//   - logs each model call, tool call and memory access at debug level.
//
// phero's Tracer gets no context, so a runTracer is built per run around the
// run's: the job's, or the protocol request's for an exposed agent. Tools
// may run in parallel: it locks.
type runTracer struct {
	ctx    context.Context //nolint:containedctx // phero's Tracer takes no context.
	logger *slog.Logger
	tel    *Telemetry
	mode   string
	// parent is the tracer of the run that delegates to this one's agents:
	// a delegate's span goes under the tool call that asked it.
	parent *runTracer

	mu        sync.Mutex
	last      *phtrace.RunSummary
	total     map[string]float64
	delegates map[string]*DelegateSummary
	// agents and chats are queues by name: the same delegate may run
	// several times at once (parallel tool calls), and phero's events carry
	// no run id, so runs are matched first in, first out.
	agents map[string][]*agentSpan
	chats  map[llmCall][]*openSpan
	tools  map[string]*openSpan
	// models is the model that last answered each agent.
	models map[string]string
}

type llmCall struct {
	agent     string
	iteration int
}

// agentSpan is an agent run in progress; it ends with the run's summary.
type agentSpan struct {
	ctx     context.Context //nolint:containedctx // the span's context, for its children.
	span    trace.Span
	started time.Time
	ended   time.Time
	err     error
}

type openSpan struct {
	ctx     context.Context //nolint:containedctx // the span's context, for its children.
	span    trace.Span
	name    string
	started time.Time
	// claimed is set once a delegate's run took this tool call as parent.
	claimed bool
}

func newRunTracer(ctx context.Context, logger *slog.Logger, tel *Telemetry, mode string,
	parent *runTracer,
) *runTracer {
	if logger == nil {
		logger = slog.Default()
	}

	return &runTracer{
		ctx: ctx, logger: logger, tel: tel.orDefault(), mode: mode, parent: parent,
		delegates: map[string]*DelegateSummary{}, agents: map[string][]*agentSpan{},
		chats: map[llmCall][]*openSpan{}, tools: map[string]*openSpan{}, models: map[string]string{},
	}
}

// Trace implements phero's trace.Tracer.
//
//nolint:gocyclo // one case per event type.
func (t *runTracer) Trace(ev phtrace.Event) {
	switch e := ev.(type) {
	case phtrace.AgentStartEvent:
		t.agentStarted(e)
	case phtrace.AgentEndEvent:
		t.agentEnded(e)
	case phtrace.AgentRunSummaryEvent:
		t.summarized(e)
	case phtrace.LLMRequestEvent:
		t.llmRequested(e)
	case phtrace.LLMResponseEvent:
		t.llmResponded(e)
	case phtrace.ToolCallEvent:
		t.toolCalled(e)
	case phtrace.ToolResultEvent:
		t.toolReturned(e)
	case phtrace.MemoryRetrieveEvent:
		t.memory(e.AgentName, "memory.retrieve", "stiggy: memory retrieved", e.Count)
	case phtrace.MemorySaveEvent:
		t.memory(e.AgentName, "memory.save", "stiggy: memory saved", e.Count)
	}
}

func (t *runTracer) agentStarted(e phtrace.AgentStartEvent) {
	kind := trace.SpanKindInternal
	if t.mode == modeExposed {
		kind = trace.SpanKindServer
	}

	ctx, span := t.tel.tracer.Start(t.parentCtx(e.AgentName), "invoke_agent "+e.AgentName,
		trace.WithSpanKind(kind), trace.WithTimestamp(e.Timestamp),
		trace.WithAttributes(attrOperation.String("invoke_agent"), attrAgentName.String(e.AgentName),
			attrMode.String(t.mode)))

	t.mu.Lock()
	t.agents[e.AgentName] = append(t.agents[e.AgentName], &agentSpan{ctx: ctx, span: span, started: e.Timestamp})
	t.mu.Unlock()
}

// parentCtx is where an agent's span goes: under the tool call that asked
// it (a delegate: a delegate's own delegates are asked from this tracer,
// the step agent's from the parent), else under the run's context.
func (t *runTracer) parentCtx(agentName string) context.Context {
	name := delegateToolPrefix + agent.SanitizeToolName(agentName)

	for tr := t; tr != nil; tr = tr.parent {
		if ctx, ok := tr.claimTool(name); ok {
			return ctx
		}
	}

	return t.ctx
}

// claimTool takes the oldest unclaimed tool call in progress named name, so
// concurrent runs of one delegate each go under their own call.
func (t *runTracer) claimTool(name string) (context.Context, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()

	var oldest *openSpan

	for _, s := range t.tools {
		if s.name == name && !s.claimed && (oldest == nil || s.started.Before(oldest.started)) {
			oldest = s
		}
	}

	if oldest == nil {
		return nil, false
	}

	oldest.claimed = true

	return oldest.ctx, true
}

func (t *runTracer) agentEnded(e phtrace.AgentEndEvent) {
	t.mu.Lock()
	for _, a := range t.agents[e.AgentName] {
		if a.ended.IsZero() {
			a.ended, a.err = e.Timestamp, e.Err

			break
		}
	}
	t.mu.Unlock()

	attrs := []any{keyAgent, e.AgentName, "iterations", e.Iterations}
	if e.Err != nil {
		attrs = append(attrs, "err", e.Err)
	}

	t.debug("stiggy: agent run ended", attrs...)
}

// summarized closes an agent run: phero sends its summary right after its
// end.
func (t *runTracer) summarized(e phtrace.AgentRunSummaryEvent) {
	s := e.Summary

	t.mu.Lock()
	t.last = &s

	if t.total == nil {
		t.total = map[string]float64{}
	}

	for k, v := range usageOf(&s) {
		t.total[k] += v
	}

	d := t.delegates[s.AgentName]
	if d == nil {
		d = &DelegateSummary{}
		t.delegates[s.AgentName] = d
	}

	d.Runs++
	d.TokensIn += s.Usage.InputTokens
	d.TokensOut += s.Usage.OutputTokens
	d.CostUSD += s.Usage.CostUSD

	a := t.popAgent(s.AgentName)
	t.mu.Unlock()

	if a == nil {
		return
	}

	a.span.SetAttributes(attrTokensIn.Int(s.Usage.InputTokens), attrTokensOut.Int(s.Usage.OutputTokens),
		attrIterations.Int(s.Iterations), attrLLMCalls.Int(s.LLMCalls), attrToolCalls.Int(s.ToolCalls),
		attrCost.Float64(s.Usage.CostUSD))

	if a.err != nil {
		a.span.RecordError(a.err)
		a.span.SetStatus(codes.Error, a.err.Error())
		a.span.SetAttributes(attrErrorType.String("agent_error"))
	}

	end := a.ended
	if end.IsZero() {
		end = e.Timestamp
	}

	a.span.End(trace.WithTimestamp(end))
	t.tel.recordRun(t.ctx, s.AgentName, t.mode, a.err != nil, end.Sub(a.started), s.Usage.CostUSD)
}

// popAgent removes the run of agentName a summary closes: the first that
// ended, else the first. The caller holds mu.
func (t *runTracer) popAgent(agentName string) *agentSpan {
	q := t.agents[agentName]
	if len(q) == 0 {
		return nil
	}

	i := 0

	for j, a := range q {
		if !a.ended.IsZero() {
			i = j

			break
		}
	}

	a := q[i]

	if q = append(q[:i:i], q[i+1:]...); len(q) == 0 {
		delete(t.agents, agentName)
	} else {
		t.agents[agentName] = q
	}

	return a
}

// agentCtx is the context of agentName's latest run in progress.
func (t *runTracer) agentCtx(agentName string) context.Context {
	t.mu.Lock()
	defer t.mu.Unlock()

	if q := t.agents[agentName]; len(q) > 0 {
		return q[len(q)-1].ctx
	}

	return t.ctx
}

func (t *runTracer) llmRequested(e phtrace.LLMRequestEvent) {
	ctx, span := t.tel.tracer.Start(t.agentCtx(e.AgentName), "chat", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithTimestamp(e.Timestamp), trace.WithAttributes(attrOperation.String("chat"),
			attrAgentName.String(e.AgentName), attrMessages.Int(e.MessageCount)))

	key := llmCall{e.AgentName, e.Iteration}

	t.mu.Lock()
	t.chats[key] = append(t.chats[key], &openSpan{ctx: ctx, span: span, started: e.Timestamp})
	t.mu.Unlock()

	t.debug("stiggy: llm request", keyAgent, e.AgentName, keyIteration, e.Iteration, "messages", e.MessageCount,
		"tools", len(e.ToolNames))
}

func (t *runTracer) llmResponded(e phtrace.LLMResponseEvent) {
	key := llmCall{e.AgentName, e.Iteration}

	t.mu.Lock()

	var c *openSpan

	if q := t.chats[key]; len(q) > 0 {
		c = q[0]

		if len(q) == 1 {
			delete(t.chats, key)
		} else {
			t.chats[key] = q[1:]
		}
	}

	if e.Model != "" {
		t.models[e.AgentName] = e.Model
	}
	t.mu.Unlock()

	var in, out int
	if e.Usage != nil {
		in, out = e.Usage.InputTokens, e.Usage.OutputTokens
	}

	var d time.Duration

	// phero reports a failed model call as a response without a message.
	failed := e.Message == nil

	if c != nil {
		d = e.Timestamp.Sub(c.started)

		if e.Model != "" {
			c.span.SetName("chat " + e.Model)
		}

		c.span.SetAttributes(attrRespModel.String(e.Model), attrTokensIn.Int(in), attrTokensOut.Int(out))

		if failed {
			c.span.SetStatus(codes.Error, "model call failed")
			c.span.SetAttributes(attrErrorType.String("model_error"))
		}

		c.span.End(trace.WithTimestamp(e.Timestamp))
	}

	t.tel.recordModelCall(t.ctx, e.Model, d, in, out, failed)

	attrs := []any{keyAgent, e.AgentName, keyIteration, e.Iteration, "model", e.Model, keyDuration, d}
	if e.Usage != nil {
		attrs = append(attrs, "tokens_in", in, "tokens_out", out)
	}

	if e.Message != nil && t.debugging() {
		attrs = append(attrs, "tool_calls", len(e.Message.ToolCalls), "text", clip(e.Message.TextContent()))
	}

	t.debug("stiggy: llm response", attrs...)
}

func (t *runTracer) toolCalled(e phtrace.ToolCallEvent) {
	ctx, span := t.tel.tracer.Start(t.agentCtx(e.AgentName), "execute_tool "+e.ToolName,
		trace.WithSpanKind(trace.SpanKindInternal), trace.WithTimestamp(e.Timestamp),
		trace.WithAttributes(attrOperation.String("execute_tool"), attrToolName.String(e.ToolName),
			attrToolCallID.String(e.CallID), attrToolType.String("function")))

	t.mu.Lock()
	t.tools[e.CallID] = &openSpan{ctx: ctx, span: span, name: e.ToolName, started: e.Timestamp}
	t.mu.Unlock()

	t.debug("stiggy: tool call", keyAgent, e.AgentName, keyIteration, e.Iteration, "tool", e.ToolName,
		"call_id", e.CallID, "args", clip(e.Arguments))
}

func (t *runTracer) toolReturned(e phtrace.ToolResultEvent) {
	t.mu.Lock()
	c := t.tools[e.CallID]
	delete(t.tools, e.CallID)
	t.mu.Unlock()

	var d time.Duration

	if c != nil {
		d = e.Timestamp.Sub(c.started)

		if e.Err != nil {
			c.span.RecordError(e.Err)
			c.span.SetStatus(codes.Error, e.Err.Error())
			c.span.SetAttributes(attrErrorType.String("tool_error"))
		}

		c.span.End(trace.WithTimestamp(e.Timestamp))
	}

	t.tel.recordToolCall(t.ctx, e.ToolName, e.Err != nil, d)

	attrs := []any{
		keyAgent, e.AgentName, keyIteration, e.Iteration, "tool", e.ToolName, "call_id", e.CallID,
		keyDuration, d,
	}
	if e.Err != nil {
		attrs = append(attrs, "err", e.Err)
	} else if t.debugging() {
		attrs = append(attrs, "result", clip(e.Result))
	}

	t.debug("stiggy: tool result", attrs...)
}

func (t *runTracer) memory(agentName, event, msg string, n int) {
	trace.SpanFromContext(t.agentCtx(agentName)).AddEvent(event)
	t.debug(msg, keyAgent, agentName, "messages", n)
}

// finish ends the spans a run left open (a run cut short by its context).
func (t *runTracer) finish() {
	t.mu.Lock()
	defer t.mu.Unlock()

	for _, s := range t.tools {
		s.span.End()
	}

	for _, q := range t.chats {
		for _, s := range q {
			s.span.End()
		}
	}

	for _, q := range t.agents {
		for _, a := range q {
			a.span.End()
		}
	}

	t.tools, t.chats, t.agents = map[string]*openSpan{}, map[llmCall][]*openSpan{}, map[string][]*agentSpan{}
}

func (t *runTracer) debugging() bool { return t.logger.Enabled(t.ctx, slog.LevelDebug) }

func (t *runTracer) debug(msg string, attrs ...any) {
	t.logger.DebugContext(t.ctx, msg, attrs...)
}

// summary is the last run's summary (the step agent's own run), nil when
// it never started.
func (t *runTracer) summary() *phtrace.RunSummary {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}

// usage adds up every run traced (a step's delegates), nil when none ran.
func (t *runTracer) usage() map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.total
}

// delegateSummaries is what each delegate spent, nil when none ran.
func (t *runTracer) delegateSummaries() map[string]DelegateSummary {
	t.mu.Lock()
	defer t.mu.Unlock()

	if len(t.delegates) == 0 {
		return nil
	}

	out := make(map[string]DelegateSummary, len(t.delegates))
	for k, v := range t.delegates {
		out[k] = *v
	}

	return out
}

// model is the model that last answered agentName.
func (t *runTracer) model(agentName string) string {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.models[agentName]
}

// clip shortens s for a log line.
func clip(s string) string {
	if len(s) <= maxLogged {
		return s
	}

	return s[:maxLogged] + "…"
}
