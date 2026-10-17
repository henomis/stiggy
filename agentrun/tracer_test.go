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
	"strings"
	"testing"
	"time"

	"github.com/henomis/phero/v2/llm"
	phtrace "github.com/henomis/phero/v2/trace"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
)

// TestTracerConcurrentAndNestedDelegates feeds the tracers the events of a
// step agent that asks the same delegate twice at once, one of which asks a
// delegate of its own: every span ends, each run of the delegate sits under
// its own tool call, and the nested delegate under the call that asked it.
func TestTracerConcurrentAndNestedDelegates(t *testing.T) {
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	tel := NewTelemetry(tp, nil)

	ctx, root := tp.Tracer("t").Start(context.Background(), "job")
	tr := newRunTracer(ctx, nil, tel, modeStep, nil)
	dl := newRunTracer(ctx, nil, tel, modeDelegate, tr)

	t0 := time.Now()
	at := func(ms int) time.Time { return t0.Add(time.Duration(ms) * time.Millisecond) }
	sum := func(agentName string, ms int) phtrace.AgentRunSummaryEvent {
		return phtrace.AgentRunSummaryEvent{Summary: phtrace.RunSummary{AgentName: agentName}, Timestamp: at(ms)}
	}

	for _, ev := range []struct {
		to *runTracer
		ev phtrace.Event
	}{
		{tr, phtrace.AgentStartEvent{AgentName: "lead", Timestamp: at(0)}},
		{tr, phtrace.ToolCallEvent{AgentName: "lead", ToolName: "ask_aide", CallID: "c1", Timestamp: at(1)}},
		{tr, phtrace.ToolCallEvent{AgentName: "lead", ToolName: "ask_aide", CallID: "c2", Timestamp: at(2)}},
		{dl, phtrace.AgentStartEvent{AgentName: "aide", Timestamp: at(3)}},
		{dl, phtrace.AgentStartEvent{AgentName: "aide", Timestamp: at(4)}},
		{dl, phtrace.LLMRequestEvent{AgentName: "aide", Iteration: 1, Timestamp: at(5)}},
		{dl, phtrace.LLMRequestEvent{AgentName: "aide", Iteration: 1, Timestamp: at(6)}},
		{dl, phtrace.LLMResponseEvent{AgentName: "aide", Iteration: 1, Message: &llm.Message{}, Timestamp: at(7)}},
		{dl, phtrace.LLMResponseEvent{AgentName: "aide", Iteration: 1, Timestamp: at(8)}}, // a failed call
		{dl, phtrace.ToolCallEvent{AgentName: "aide", ToolName: "ask_sub", CallID: "c3", Timestamp: at(9)}},
		{dl, phtrace.AgentStartEvent{AgentName: "sub", Timestamp: at(10)}},
		{dl, phtrace.AgentEndEvent{AgentName: "sub", Timestamp: at(11)}},
		{dl, sum("sub", 11)},
		{dl, phtrace.ToolResultEvent{AgentName: "aide", ToolName: "ask_sub", CallID: "c3", Timestamp: at(12)}},
		{dl, phtrace.AgentEndEvent{AgentName: "aide", Timestamp: at(13)}},
		{dl, sum("aide", 13)},
		{dl, phtrace.AgentEndEvent{AgentName: "aide", Timestamp: at(14)}},
		{dl, sum("aide", 14)},
		{tr, phtrace.ToolResultEvent{AgentName: "lead", ToolName: "ask_aide", CallID: "c1", Timestamp: at(15)}},
		{tr, phtrace.ToolResultEvent{AgentName: "lead", ToolName: "ask_aide", CallID: "c2", Timestamp: at(16)}},
		{tr, phtrace.AgentEndEvent{AgentName: "lead", Timestamp: at(17)}},
		{tr, sum("lead", 17)},
	} {
		ev.to.Trace(ev.ev)
	}

	root.End()

	if s, e := len(rec.Started()), len(rec.Ended()); s != e {
		t.Fatalf("%d spans started, %d ended", s, e)
	}

	byID := map[trace.SpanID]sdktrace.ReadOnlySpan{}
	for _, s := range rec.Ended() {
		byID[s.SpanContext().SpanID()] = s
	}

	parents := map[string][]sdktrace.ReadOnlySpan{}

	for _, s := range rec.Ended() {
		if p := byID[s.Parent().SpanID()]; p != nil {
			parents[s.Name()] = append(parents[s.Name()], p)
		}
	}

	aides := parents["invoke_agent aide"]
	if len(aides) != 2 || aides[0].Name() != "execute_tool ask_aide" || aides[1].Name() != "execute_tool ask_aide" ||
		aides[0].SpanContext().SpanID() == aides[1].SpanContext().SpanID() {
		t.Fatalf("aide runs under %v, want two distinct ask_aide calls", aides)
	}

	if subs := parents["invoke_agent sub"]; len(subs) != 1 || subs[0].Name() != "execute_tool ask_sub" {
		t.Fatalf("sub under %v, want the ask_sub call", subs)
	}

	var failed int

	for _, s := range rec.Ended() {
		if strings.HasPrefix(s.Name(), "chat") && s.Status().Code == codes.Error {
			failed++
		}
	}

	if failed != 1 {
		t.Fatalf("%d failed chat spans, want 1", failed)
	}
}

func TestSummaryErrorHoldsNoAnswer(t *testing.T) {
	_, err := parseOutput("my secret answer", map[string]any{"type": "object"})
	if got := summaryError(err); got != ErrNotJSONObject.Error() {
		t.Fatalf("summary error %q", got)
	}
}
