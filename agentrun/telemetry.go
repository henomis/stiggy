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
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
	"go.opentelemetry.io/otel/trace"
)

// telemetryScope is the instrumentation scope of stiggy's spans and metrics.
const telemetryScope = "github.com/henomis/stiggy"

// Attribute keys: the OpenTelemetry GenAI conventions, and stiggy's own.
const (
	attrOperation  = attribute.Key("gen_ai.operation.name")
	attrAgentName  = attribute.Key("gen_ai.agent.name")
	attrReqModel   = attribute.Key("gen_ai.request.model")
	attrRespModel  = attribute.Key("gen_ai.response.model")
	attrTokensIn   = attribute.Key("gen_ai.usage.input_tokens")
	attrTokensOut  = attribute.Key("gen_ai.usage.output_tokens")
	attrTokenType  = attribute.Key("gen_ai.token.type")
	attrToolName   = attribute.Key("gen_ai.tool.name")
	attrToolCallID = attribute.Key("gen_ai.tool.call.id")
	attrToolType   = attribute.Key("gen_ai.tool.type")
	attrErrorType  = attribute.Key("error.type")
	attrMode       = attribute.Key("stiggy.run.mode")
	attrOutcome    = attribute.Key("stiggy.outcome")
	attrIterations = attribute.Key("stiggy.agent.iterations")
	attrLLMCalls   = attribute.Key("stiggy.agent.llm_calls")
	attrToolCalls  = attribute.Key("stiggy.agent.tool_calls")
	attrCost       = attribute.Key("stiggy.agent.cost_usd")
	attrMessages   = attribute.Key("stiggy.llm.messages")
)

// Run modes: a flow step, an agent exposed on the protocol, a delegate.
const (
	modeStep     = "step"
	modeExposed  = "exposed"
	modeDelegate = "delegate"
)

// Outcomes of agent runs and tool calls, as measured.
const (
	outcomeOK    = "ok"
	outcomeError = "error"
)

// secondBuckets are the boundaries, in seconds, of model, tool and run
// durations.
var secondBuckets = []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 30, 60, 120, 300, 600}

// tokenBuckets are the boundaries of token counts per model call (the GenAI
// conventions' advice).
var tokenBuckets = []float64{
	1, 4, 16, 64, 256, 1024, 4096, 16384, 65536, 262144, 1048576, 4194304, 16777216, 67108864,
}

// Telemetry is where agent runs report spans and metrics.
type Telemetry struct {
	tracer trace.Tracer

	runs         metric.Int64Counter
	runDuration  metric.Float64Histogram
	opDuration   metric.Float64Histogram
	tokens       metric.Int64Histogram
	cost         metric.Float64Counter
	toolCalls    metric.Int64Counter
	toolDuration metric.Float64Histogram
}

// NewTelemetry builds the telemetry of agent runs on tp and mp (nil: the
// global providers, no-ops until an SDK is installed).
func NewTelemetry(tp trace.TracerProvider, mp metric.MeterProvider) *Telemetry {
	if tp == nil {
		tp = otel.GetTracerProvider()
	}

	if mp == nil {
		mp = otel.GetMeterProvider()
	}

	t, err := newTelemetry(tp.Tracer(telemetryScope), mp.Meter(telemetryScope))
	if err != nil {
		t, _ = newTelemetry(tp.Tracer(telemetryScope), noop.Meter{})
	}

	return t
}

func newTelemetry(tr trace.Tracer, m metric.Meter) (*Telemetry, error) {
	t := &Telemetry{tracer: tr}

	var err error

	seconds := func(name, desc string) metric.Float64Histogram {
		h, herr := m.Float64Histogram(name, metric.WithDescription(desc), metric.WithUnit("s"),
			metric.WithExplicitBucketBoundaries(secondBuckets...))
		if herr != nil {
			err = herr
		}

		return h
	}

	t.runDuration = seconds("stiggy.agent.run.duration", "Duration of agent runs.")
	t.opDuration = seconds("gen_ai.client.operation.duration", "Duration of model calls.")
	t.toolDuration = seconds("stiggy.tool.duration", "Duration of tool calls.")

	if err != nil {
		return nil, err
	}

	if t.runs, err = m.Int64Counter("stiggy.agent.runs", metric.WithDescription("Agent runs, by outcome."),
		metric.WithUnit("{run}")); err != nil {
		return nil, err
	}

	if t.tokens, err = m.Int64Histogram("gen_ai.client.token.usage",
		metric.WithDescription("Tokens used per model call."), metric.WithUnit("{token}"),
		metric.WithExplicitBucketBoundaries(tokenBuckets...)); err != nil {
		return nil, err
	}

	if t.cost, err = m.Float64Counter("stiggy.agent.cost", metric.WithDescription("Estimated cost of agent runs."),
		metric.WithUnit("USD")); err != nil {
		return nil, err
	}

	if t.toolCalls, err = m.Int64Counter("stiggy.tool.calls", metric.WithDescription("Tool calls, by outcome."),
		metric.WithUnit("{call}")); err != nil {
		return nil, err
	}

	return t, nil
}

var defaultTelemetry = NewTelemetry(nil, nil)

func (t *Telemetry) orDefault() *Telemetry {
	if t == nil {
		return defaultTelemetry
	}

	return t
}

func (t *Telemetry) recordRun(ctx context.Context, agentName, mode string, failed bool, d time.Duration,
	cost float64,
) {
	attrs := metric.WithAttributes(attrAgentName.String(agentName), attrMode.String(mode),
		attrOutcome.String(outcomeOf(failed)))
	t.runs.Add(ctx, 1, attrs)
	t.runDuration.Record(ctx, d.Seconds(), attrs)

	if cost > 0 {
		t.cost.Add(ctx, cost, metric.WithAttributes(attrAgentName.String(agentName)))
	}
}

func (t *Telemetry) recordModelCall(ctx context.Context, model string, d time.Duration, in, out int, failed bool) {
	attrs := []attribute.KeyValue{attrOperation.String("chat"), attrRespModel.String(model)}
	if failed {
		attrs = append(attrs, attrErrorType.String("model_error"))
	}

	t.opDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attrs...))

	if in > 0 {
		t.tokens.Record(ctx, int64(in), metric.WithAttributes(attrOperation.String("chat"),
			attrRespModel.String(model), attrTokenType.String("input")))
	}

	if out > 0 {
		t.tokens.Record(ctx, int64(out), metric.WithAttributes(attrOperation.String("chat"),
			attrRespModel.String(model), attrTokenType.String("output")))
	}
}

func (t *Telemetry) recordToolCall(ctx context.Context, tool string, failed bool, d time.Duration) {
	attrs := metric.WithAttributes(attrToolName.String(tool), attrOutcome.String(outcomeOf(failed)))
	t.toolCalls.Add(ctx, 1, attrs)
	t.toolDuration.Record(ctx, d.Seconds(), metric.WithAttributes(attrToolName.String(tool)))
}

func outcomeOf(failed bool) string {
	if failed {
		return outcomeError
	}

	return outcomeOK
}
