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
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/internal/report"
	"github.com/henomis/stiggy/spec"
)

// Usage counters every agent step reports. Flows cap them with `budget:`.
// Counters that would be zero are left out, except agent_steps.
const (
	CounterAgentSteps       = "agent_steps"
	CounterTokensIn         = "tokens_in"
	CounterTokensOut        = "tokens_out"
	CounterTokensCacheRead  = "tokens_cache_read"  //nolint:gosec // a counter name, not a credential.
	CounterTokensCacheWrite = "tokens_cache_write" //nolint:gosec // a counter name, not a credential.
	CounterTokensReasoning  = "tokens_reasoning"
	CounterCostUSD          = "cost_usd"
	CounterLLMCalls         = "llm_calls"
	CounterToolCalls        = "tool_calls"
	CounterToolErrors       = "tool_errors"
)

// Separators of the per-agent and per-model counters: tokens_in, tokens_out
// and cost_usd are also reported as <counter>__agent__<agent> and
// <counter>__model__<model>, with every character outside [A-Za-z0-9_-]
// replaced by "_".
const (
	CounterByAgent = "__agent__"
	CounterByModel = "__model__"
)

// maxCounterName is packtrail's bound on a budget counter's name.
const maxCounterName = 128

// Span names and custom span types of stiggy's own spans.
const (
	spanStep    = "stiggy.step"
	spanRequest = "stiggy.request"
)

// How a step's next node was chosen, as reported in route_by.
const (
	routeByAgent   = "agent"
	routeByDefault = "default"
)

// attrPrefix prefixes the attributes stiggy sets on spans.
const attrPrefix = "stiggy."

// stepContext prepares ctx for an agent step's spans: the provider, the
// job's trace (its traceparent, when the execution has one), the execution
// as the session, and the step as attributes of every span below.
func stepContext(ctx context.Context, p *trace.Provider, job *worker.Job, agentName string) context.Context {
	ctx = trace.WithProvider(ctx, p)
	ctx = trace.Extract(ctx, job.Traceparent)
	ctx = trace.WithSession(ctx, job.ExecID)

	attrs := []trace.Attr{
		trace.String(attrPrefix+"exec", job.ExecID),
		trace.String(attrPrefix+"flow", job.Flow),
		trace.String(attrPrefix+"node", job.Node),
		trace.String(attrPrefix+"key", job.Key),
		trace.Int(attrPrefix+"generation", job.Generation),
		trace.Int(attrPrefix+"attempt", job.Attempt),
		trace.String(attrPrefix+"agent", agentName),
	}

	if job.Context.Index != nil {
		attrs = append(attrs, trace.Int(attrPrefix+"index", *job.Context.Index))
	}

	if by := job.Context.ResumedBy; by != "" {
		attrs = append(attrs, trace.String(attrPrefix+"resumed_by", by))
	}

	return withLogger(trace.WithAttributes(ctx, attrs...), job.Logger())
}

// observer reports what an attempt learns, on the step's span and in the
// step's end line.
type observer struct {
	span *trace.Span
	rep  *report.Report
}

func observe(ctx context.Context, span *trace.Span) *observer {
	return &observer{span: span, rep: report.From(ctx)}
}

// note records attrs in the report and, prefixed, on the span. Durations go
// on the span in milliseconds, with a _ms suffix.
func (o *observer) note(attrs ...slog.Attr) {
	o.rep.Add(attrs...)

	for _, a := range attrs {
		if a.Value.Kind() == slog.KindDuration {
			o.span.SetAttributes(trace.Int64(attrPrefix+a.Key+"_ms", a.Value.Duration().Milliseconds()))

			continue
		}

		o.span.SetAttributes(spanAttr(attrPrefix+a.Key, a.Value))
	}
}

func spanAttr(key string, v slog.Value) trace.Attr {
	//nolint:exhaustive // every other kind is exported as its string.
	switch v.Kind() {
	case slog.KindInt64:
		return trace.Int64(key, v.Int64())
	case slog.KindFloat64:
		return trace.Float64(key, v.Float64())
	case slog.KindBool:
		return trace.Bool(key, v.Bool())
	case slog.KindAny:
		if ss, ok := v.Any().([]string); ok {
			return trace.Strings(key, ss)
		}
	}

	return trace.String(key, v.String())
}

// run reports how the agent run went: from its summary when phero returned
// one, and the totals of everything the attempt ran (delegates included).
func (o *observer) run(res *agent.Result, err error, t *trace.Totals) {
	var attrs []slog.Attr

	if res != nil && res.Summary != nil {
		s := res.Summary
		attrs = append(attrs, slog.String(keyStop, string(s.StopReason)),
			slog.Int("iterations", s.Iterations), slog.Int("max_iterations", s.MaxIterations))

		if s.Latency.TimeToFirstToken > 0 {
			attrs = append(attrs, slog.Duration("ttft", s.Latency.TimeToFirstToken))
		}
	} else if err != nil {
		attrs = append(attrs, slog.String(keyStop, string(stopReasonOf(err))))
	}

	attrs = append(attrs, slog.Int(CounterLLMCalls, t.LLMCalls), slog.Duration("llm_time", t.LLMTime))

	if t.ToolCalls > 0 {
		attrs = append(attrs, slog.Int(CounterToolCalls, t.ToolCalls), slog.Int(CounterToolErrors, t.ToolErrors),
			slog.Duration("tool_time", t.ToolTime), slog.Any("tools", toolList(t.ByTool)))
	}

	o.note(attrs...)
	o.failure(err)
}

// routed reports where the step goes next: the route the agent chose and
// why, or the default, among the routes it could choose.
func (o *observer) routed(step spec.Step, route, reason string) {
	targets := step.DynamicTargets()
	if len(targets) == 0 {
		return
	}

	attrs := []slog.Attr{slog.Any("routes", targets)}

	if route == "" {
		attrs = append(attrs, slog.String("route_by", routeByDefault))
	} else {
		attrs = append(attrs, slog.String("route", route), slog.String("route_by", routeByAgent))
		if reason != "" {
			attrs = append(attrs, slog.String("route_reason", reason))
		}
	}

	o.note(attrs...)

	if route != "" {
		o.span.AddEvent(attrPrefix+"route", trace.String(attrPrefix+"route", route),
			trace.String(attrPrefix+"route_reason", reason))
	}
}

// review reports the human's reply a resumed run starts from: how they
// replied, who (when the resume named them) and how long the step waited
// for them, until this attempt.
func (o *observer) review(h *hitl, job *worker.Job) {
	if h.replied == nil {
		return
	}

	attrs := []slog.Attr{slog.String("review_kind", h.replied.Kind), slog.String("review_outcome", h.outcome)}

	if by := job.Context.ResumedBy; by != "" {
		attrs = append(attrs, slog.String("reviewed_by", by))
	}

	if !h.replied.AskedAt.IsZero() {
		attrs = append(attrs, slog.Duration("review_wait", time.Since(h.replied.AskedAt).Round(time.Millisecond)))
	}

	o.note(attrs...)
}

// stopReasonOf classifies the error of a run that returned no summary (it
// failed before phero's run started, or its stream ended without a result)
// as phero would.
func stopReasonOf(err error) trace.StopReason {
	switch {
	case errors.Is(err, agent.ErrMaxIterationsReached):
		return trace.StopMaxIterations
	case errors.Is(err, agent.ErrStopRun):
		return trace.StopRequested
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return trace.StopCancelled
	case errors.Is(err, llm.ErrGuardrailBlocked):
		return trace.StopGuardrail
	default:
		return trace.StopError
	}
}

// failure reports what a provider said about an error: the wait it asked
// for, and its code. The node's retry policy still decides the next attempt.
func (o *observer) failure(err error) {
	var pe *llm.ProviderError
	if errors.As(err, &pe) {
		if pe.RetryAfter > 0 {
			o.note(slog.Duration("retry_after", pe.RetryAfter))
		}

		if pe.StatusCode != 0 {
			o.note(slog.Int("status_code", pe.StatusCode))
		}
	}
}

// toolList renders per-tool totals as name:calls/errors/duration, sorted.
func toolList(byTool map[string]trace.ToolTotals) []string {
	out := make([]string, 0, len(byTool))

	for _, name := range slices.Sorted(maps.Keys(byTool)) {
		tt := byTool[name]
		out = append(out, fmt.Sprintf("%s:%d/%d/%s", name, tt.Calls, tt.Errors, tt.Duration.Round(time.Millisecond)))
	}

	return out
}

// usageOf returns the counters of what an attempt ran, from the totals of
// its span: the agent's run and every delegate under it. A step whose run
// never started still counts one agent step.
func usageOf(t *trace.Totals) map[string]float64 {
	u := map[string]float64{CounterAgentSteps: float64(max(t.AgentRuns, 1))}

	put := func(k string, v float64) {
		if v != 0 {
			u[k] += v
		}
	}

	s := t.Usage
	u[CounterTokensIn] = float64(s.InputTokens)
	u[CounterTokensOut] = float64(s.OutputTokens)
	put(CounterTokensCacheRead, float64(s.CacheReadTokens))
	put(CounterTokensCacheWrite, float64(s.CacheWriteTokens))
	put(CounterTokensReasoning, float64(s.ReasoningTokens))
	put(CounterCostUSD, s.CostUSD)
	put(CounterLLMCalls, float64(t.LLMCalls))
	put(CounterToolCalls, float64(t.ToolCalls))
	put(CounterToolErrors, float64(t.ToolErrors))

	breakdown := func(sep, name string, us trace.UsageSummary) {
		put(counterName(CounterTokensIn, sep, name), float64(us.InputTokens))
		put(counterName(CounterTokensOut, sep, name), float64(us.OutputTokens))
		put(counterName(CounterCostUSD, sep, name), us.CostUSD)
	}

	for name, a := range t.ByAgent {
		breakdown(CounterByAgent, name, a.Usage)
	}

	for name, m := range t.ByModel {
		breakdown(CounterByModel, name, m.Usage)
	}

	return u
}

// counterName is counter+sep+name, with name made a valid counter name and
// the whole cut to packtrail's bound.
func counterName(counter, sep, name string) string {
	clean := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '-':
			return r
		default:
			return '_'
		}
	}, name)

	k := counter + sep + clean
	if len(k) > maxCounterName {
		k = k[:maxCounterName]
	}

	return k
}

// endErr is the error a step's span ends with: none for an interrupt, which
// is a pause rather than a failure.
func endErr(err error) error {
	var intr *worker.InterruptError
	if errors.As(err, &intr) {
		return nil
	}

	return err
}

// stepData is the step's place in its flow, for its span's payload.
func stepData(job *worker.Job) map[string]any {
	return map[string]any{
		"exec": job.ExecID, "flow": job.Flow, "node": job.Node,
		"generation": strconv.Itoa(job.Generation), "visit": job.Context.Visits[job.Node],
	}
}
