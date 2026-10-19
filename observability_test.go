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
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"
	"github.com/henomis/phero/v2/trace/tracetest"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/stiggytest"
)

const (
	testTraceID     = "4bf92f3577b34da6a3ce929d0e0e4736"
	testParentID    = "00f067aa0ba902b7"
	testTraceparent = "00-" + testTraceID + "-" + testParentID + "-01"
)

// TestTracing checks the spans of a step: the step's span joins the
// execution's trace, the agent's run nests under it and the delegate's
// under that, every span carries the step's attributes and the execution as
// its session, and the step's counters and end line come from the totals.
func TestTracing(t *testing.T) {
	note, err := llm.NewTool("note", "Takes a note.", func(context.Context, *struct{}) (string, error) {
		return "noted", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	callFirst := func(tool string) llm.LLM {
		return stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
			if len(r.ToolResults) == 0 {
				return stiggytest.Response{Calls: []stiggytest.Call{{Tool: tool, Args: map[string]any{"input": "go"}}}}
			}

			return stiggytest.Response{Text: "done: " + r.ToolResults[0]}
		})
	}

	spans := tracetest.NewRecorder()
	logs := newLogRecorder(slog.LevelInfo)
	h := startYAML(t, loggingFleet, map[string]llm.LLM{"direct": callFirst("note"), "boss": callFirst("ask_aide")},
		stiggy.WithTool("note", note), stiggy.WithLogger(slog.New(logs)),
		stiggy.WithTracing(trace.WithProcessor(spans)))

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "ask", map[string]any{"q": "?"}, packtrail.WithTraceparent(testTraceparent))
	if err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v", st, err)
	}

	steps := spans.ByName("stiggy.step answer")
	if len(steps) != 1 {
		t.Fatalf("step spans: %v", spanNames(spans.Spans()))
	}

	step := steps[0]
	if step.Parent.TraceID.String() != testTraceID || step.Parent.SpanID.String() != testParentID {
		t.Errorf("step parent = %v, want the execution's traceparent", step.Parent)
	}

	if step.SessionID != id || attr(step, "stiggy.exec") != id || attr(step, "stiggy.node") != "answer" ||
		attr(step, "stiggy.generation") == nil || attr(step, "stiggy.llm_calls") != int64(4) {
		t.Errorf("step span: session %q, attrs %v", step.SessionID, step.Attrs)
	}

	runs := spans.ByKind(trace.KindRun)
	if len(runs) != 2 {
		t.Fatalf("run spans: %v", spanNames(runs))
	}

	lead := spans.ByName("invoke_agent lead")
	if len(lead) != 1 || lead[0].Parent != step.Context {
		t.Fatalf("lead's run is not the step's child: %v", lead)
	}

	aide := spans.ByName("invoke_agent aide")
	if len(aide) != 1 || aide[0].Context.TraceID != step.Context.TraceID || attr(aide[0], "stiggy.exec") != id {
		t.Fatalf("aide's run is not in the step's trace: %v", aide)
	}

	for k, want := range map[string]float64{
		"agent_steps": 2, "llm_calls": 4, "tool_calls": 2,
	} {
		if st.Counters[k] != want {
			t.Errorf("counter %s = %v, want %v (all: %v)", k, st.Counters[k], want, st.Counters)
		}
	}

	if st.Counters["tokens_in__agent__aide"] == 0 || st.Counters["tokens_in__agent__lead"] == 0 {
		t.Errorf("per-agent counters missing: %v", st.Counters)
	}

	ended := logs.find("stiggy: step ended", map[string]string{"exec": id, "trace_id": testTraceID})
	if len(ended) != 1 || ended[0]["stop_reason"] != "final" || ended[0]["llm_calls"] != "4" ||
		ended[0]["llm_time"] == "" || ended[0]["tools"] == "" {
		t.Fatalf("step ended: %v", logs.find("stiggy: step ended", nil))
	}
}

// TestRouteReport checks a step's end line says where it went and why: the
// route the agent chose with its reason, or the default.
func TestRouteReport(t *testing.T) {
	reviews := 0
	critic := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			if reviews++; reviews == 1 {
				return stiggytest.Response{Calls: []stiggytest.Call{
					{Tool: "route_to_write", Args: map[string]any{"reason": "too long"}},
				}}
			}
		}

		return stiggytest.Response{Text: "looks good"}
	})

	logs := newLogRecorder(slog.LevelInfo)
	h := startYAML(t, `
models: {w: {provider: x}, c: {provider: x}}
agents:
  writer: {model: w}
  critic: {model: c}
flows:
  draft:
    start: write
    nodes:
      - {id: write, type: task, agent: writer, next: review}
      - {id: review, type: task, agent: critic, routes: [write], next: done}
      - {id: done, type: task, activity: done}
`, map[string]llm.LLM{"w": stiggytest.Echo("draft: "), "c": critic},
		stiggy.WithLogger(slog.New(logs)),
		stiggy.WithActivity("done", func(context.Context, *worker.Job) (*worker.Result, error) {
			return &worker.Result{Output: map[string]any{"ok": true}}, nil
		}))

	st := h.Run(t, "draft", map[string]any{"topic": "nats"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	review := map[string]string{"exec": st.ExecID, "node": "review"}

	routed := logs.find("stiggy: step ended", merge(review, map[string]string{"route_by": "agent"}))
	if len(routed) != 1 || routed[0]["route"] != "write" || routed[0]["route_reason"] != "too long" ||
		routed[0]["routes"] != "[write]" {
		t.Fatalf("routed step: %v", logs.find("stiggy: step ended", review))
	}

	if len(logs.find("stiggy: step ended", merge(review, map[string]string{"route_by": "default"}))) != 1 {
		t.Fatalf("default route not reported: %v", logs.find("stiggy: step ended", review))
	}
}

// TestReviewReport checks the end line of a resumed step reports the
// human's reply: how, who and after how long.
func TestReviewReport(t *testing.T) {
	logs := newLogRecorder(slog.LevelInfo)
	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, human_input: true}
`, map[string]llm.LLM{"m": stiggytest.Echo("draft: ")}, stiggy.WithLogger(slog.New(logs)))

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "write", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, id, "draft")

	if err = h.Client.Resume(ctx, id, "draft", map[string]any{"approve": true},
		packtrail.WithResumedBy("ann")); err != nil {
		t.Fatal(err)
	}

	if st, werr := h.Client.Wait(ctx, id); werr != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v", st, werr)
	}

	ended := logs.find("stiggy: step ended", map[string]string{"exec": id, "outcome": "completed"})
	if len(ended) != 1 || ended[0]["review_outcome"] != "approved" || ended[0]["reviewed_by"] != "ann" ||
		ended[0]["review_kind"] != "review" || ended[0]["review_wait"] == "" {
		t.Fatalf("resumed step: %v", logs.find("stiggy: step ended", nil))
	}
}

// TestAskHumanStops checks a question to a human ends the agent's run as a
// requested stop: stop_reason stopped, and no span marked as failed.
func TestAskHumanStops(t *testing.T) {
	model := stiggytest.Model(func(stiggytest.Request) stiggytest.Response {
		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_human", Args: map[string]any{"question": "Which color?"}}}}
	})

	spans := tracetest.NewRecorder()
	logs := newLogRecorder(slog.LevelInfo)
	h := startYAML(t, `
models: {m: {provider: x}}
agents: {painter: {model: m}}
flows:
  paint:
    nodes:
      - {id: paint, type: task, agent: painter, routes: [human]}
`, map[string]llm.LLM{"m": model}, stiggy.WithLogger(slog.New(logs)), stiggy.WithTracing(trace.WithProcessor(spans)))

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "paint", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, id, "paint")

	ended := logs.find("stiggy: step ended", map[string]string{"exec": id, "outcome": "interrupted"})
	if len(ended) != 1 || ended[0]["stop_reason"] != string(trace.StopRequested) {
		t.Fatalf("step ended: %v", logs.find("stiggy: step ended", nil))
	}

	for _, s := range spans.Spans() {
		if s.Err != nil {
			t.Errorf("span %s failed: %v", s.Name, s.Err)
		}
	}

	if len(spans.ByName("invoke_agent painter")) != 1 {
		t.Fatalf("spans: %v", spanNames(spans.Spans()))
	}
}

// TestFleetTracing checks a fleet's tracing section exports its steps' spans,
// here to a jsonfile.
func TestFleetTracing(t *testing.T) {
	file := filepath.Join(t.TempDir(), "trace.ndjson")
	h := startYAML(t, `
tracing: {exporter: jsonfile, file: `+file+`}
models: {m: {provider: x}}
agents: {a: {model: m}}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: a}
`, map[string]llm.LLM{"m": stiggytest.Echo("echo: ")})

	st := h.Run(t, "f", map[string]any{"q": "?"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}

	for _, want := range []string{`"name":"stiggy.step s"`, `"name":"invoke_agent a"`, `"kind":"chat"`, st.ExecID} {
		if !strings.Contains(string(data), want) {
			t.Errorf("trace file lacks %s:\n%s", want, data)
		}
	}
}

func attr(s *trace.SpanData, key string) any {
	for _, a := range s.Attrs {
		if a.Key == key {
			return a.Value
		}
	}

	return nil
}

func spanNames(ss []*trace.SpanData) []string {
	out := make([]string, 0, len(ss))
	for _, s := range ss {
		out = append(out, s.Name)
	}

	return slices.Sorted(slices.Values(out))
}

func merge(a, b map[string]string) map[string]string {
	out := make(map[string]string, len(a)+len(b))
	for k, v := range a {
		out[k] = v
	}

	for k, v := range b {
		out[k] = v
	}

	return out
}
