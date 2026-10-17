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

package stiggy_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"

	"github.com/henomis/stiggy"
)

// TestTelemetry checks one execution is one trace from the caller to every
// model and tool call: the step agent's run under packtrail's job span, a
// delegate's run under the tool call that asked it; that the metrics are
// recorded; and that the step's summary reaches the timeline.
func TestTelemetry(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))

	h := startLoggingFleet(t, stiggy.WithTracerProvider(tp), stiggy.WithMeterProvider(mp))

	ctx, root := tp.Tracer("test").Start(context.Background(), "caller")
	id := runAsk(ctx, t, h)

	root.End()

	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, s := range spans.Ended() {
		byID[s.SpanContext().SpanID()] = s
	}

	named := func(name string) sdktrace.ReadOnlySpan {
		t.Helper()

		for _, s := range spans.Ended() {
			if s.Name() == name {
				return s
			}
		}

		var names []string
		for _, s := range spans.Ended() {
			names = append(names, s.Name())
		}

		t.Fatalf("no span %q in %v", name, names)

		return nil
	}

	parentName := func(s sdktrace.ReadOnlySpan) string {
		if p := byID[s.Parent().SpanID()]; p != nil {
			return p.Name()
		}

		return ""
	}

	for child, parent := range map[string]string{
		"invoke_agent lead":     "packtrail.job answer",
		"execute_tool ask_aide": "invoke_agent lead",
		"invoke_agent aide":     "execute_tool ask_aide",
		"execute_tool note":     "invoke_agent aide",
	} {
		if got := parentName(named(child)); got != parent {
			t.Errorf("%s: parent %q, want %q", child, got, parent)
		}
	}

	for _, s := range spans.Ended() {
		if s.SpanContext().TraceID() != root.SpanContext().TraceID() {
			t.Errorf("span %s in trace %v, not the caller's", s.Name(), s.SpanContext().TraceID())
		}

		if strings.HasPrefix(s.Name(), "chat") && !strings.HasPrefix(parentName(s), "invoke_agent ") {
			t.Errorf("%s under %q", s.Name(), parentName(s))
		}
	}

	lead := named("invoke_agent lead")
	for _, a := range lead.Attributes() {
		if a.Key == "gen_ai.usage.input_tokens" && a.Value.AsInt64() == 0 {
			t.Errorf("lead's run has no input tokens")
		}
	}

	var rm metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &rm); err != nil {
		t.Fatal(err)
	}

	got := map[string]bool{}

	for _, sm := range rm.ScopeMetrics {
		for _, m := range sm.Metrics {
			got[m.Name] = true
		}
	}

	for _, name := range []string{
		"stiggy.agent.runs", "stiggy.agent.run.duration", "gen_ai.client.operation.duration",
		"gen_ai.client.token.usage", "stiggy.tool.calls", "stiggy.tool.duration", "packtrail.worker.jobs",
	} {
		if !got[name] {
			t.Errorf("no metric %s", name)
		}
	}

	tl, err := h.Client.Timeline(ctxTimeout(t), id)
	if err != nil {
		t.Fatal(err)
	}

	if len(tl.Steps) != 1 {
		t.Fatalf("timeline %+v", tl)
	}

	var sum stiggy.StepSummary
	if err = json.Unmarshal(tl.Steps[0].Summary, &sum); err != nil {
		t.Fatalf("summary %s: %v", tl.Steps[0].Summary, err)
	}

	if sum.Agent != "lead" || sum.ToolCalls != 1 || len(sum.Tools) != 1 || sum.Tools[0].Name != "ask_aide" ||
		sum.Delegates["aide"].Runs != 1 || sum.TokensIn == 0 || sum.LLMCalls != 2 {
		t.Fatalf("summary %+v", sum)
	}
}
