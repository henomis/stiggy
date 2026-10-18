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
	"errors"
	"log/slog"
	"sync"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// logRecorder is a slog.Handler keeping every record with its attributes,
// those bound with Logger.With included, flattened to strings.
type logRecorder struct {
	level slog.Level
	mu    *sync.Mutex
	recs  *[]map[string]string
	attrs []slog.Attr
}

func newLogRecorder(level slog.Level) *logRecorder {
	return &logRecorder{level: level, mu: &sync.Mutex{}, recs: &[]map[string]string{}}
}

func (r *logRecorder) Enabled(_ context.Context, l slog.Level) bool { return l >= r.level }

func (r *logRecorder) Handle(_ context.Context, rec slog.Record) error {
	m := map[string]string{"msg": rec.Message, "level": rec.Level.String()}
	for _, a := range r.attrs {
		m[a.Key] = a.Value.String()
	}

	rec.Attrs(func(a slog.Attr) bool {
		m[a.Key] = a.Value.String()

		return true
	})

	r.mu.Lock()
	*r.recs = append(*r.recs, m)
	r.mu.Unlock()

	return nil
}

func (r *logRecorder) WithAttrs(as []slog.Attr) slog.Handler {
	c := *r
	c.attrs = append(append([]slog.Attr{}, r.attrs...), as...)

	return &c
}

func (r *logRecorder) WithGroup(string) slog.Handler { return r }

// find returns the records with message msg whose attributes include want.
func (r *logRecorder) find(msg string, want map[string]string) []map[string]string {
	r.mu.Lock()
	defer r.mu.Unlock()

	var out []map[string]string

next:
	for _, m := range *r.recs {
		if m["msg"] != msg {
			continue
		}

		for k, v := range want {
			if m[k] != v {
				continue next
			}
		}

		out = append(out, m)
	}

	return out
}

const loggingFleet = `
models: {direct: {provider: x}, boss: {provider: x}}
agents:
  lead: {model: boss, delegates: [aide]}
  aide: {model: direct, tools: [note]}
flows:
  ask:
    nodes:
      - {id: answer, type: task, agent: lead}
`

// startLogging runs loggingFleet: lead delegates to aide, which calls the
// note tool; the tool logs through its job's logger.
func startLogging(t *testing.T, level slog.Level) (*logRecorder, string) {
	t.Helper()

	note, err := llm.NewTool("note", "Takes a note.", func(ctx context.Context, _ *struct{}) (string, error) {
		job, ok := stiggy.JobFromContext(ctx)
		if !ok || job.Logger == nil {
			return "", errors.New("no job logger")
		}

		job.Logger.InfoContext(ctx, "noted")

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

	rec := newLogRecorder(level)
	h := startYAML(t, loggingFleet, map[string]llm.LLM{"direct": callFirst("note"), "boss": callFirst("ask_aide")},
		stiggy.WithTool("note", note), stiggy.WithLogger(slog.New(rec)))

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "ask", map[string]any{"q": "?"})
	if err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v %s", st, err, st.Error)
	}

	return rec, id
}

// TestStepLogs checks a step logs its start and end through its job's
// logger, and at debug level each model call, tool call (a delegate's
// included) and the delegate's run, all on the execution's keys.
func TestStepLogs(t *testing.T) {
	rec, id := startLogging(t, slog.LevelDebug)
	step := map[string]string{"exec": id, "flow": "ask", "node": "answer", "agent": "lead", "attempt": "1"}

	if len(rec.find("stiggy: step started", step)) != 1 {
		t.Fatalf("no step started line for %v", step)
	}

	ended := rec.find("stiggy: step ended", step)
	if len(ended) != 1 || ended[0]["outcome"] != "completed" || ended[0]["tokens_in"] == "" ||
		ended[0]["duration"] == "" {
		t.Fatalf("step ended: %v", ended)
	}

	if len(rec.find("noted", step)) != 1 {
		t.Fatalf("the tool's line lacks the step's keys: %v", rec.find("noted", nil))
	}

	inStep := map[string]string{"exec": id, "node": "answer"}
	for msg, agent := range map[string]string{
		"stiggy: llm request": "lead", "stiggy: llm response": "aide", "stiggy: agent run ended": "aide",
	} {
		if len(rec.find(msg, map[string]string{"exec": id, "agent": agent})) == 0 {
			t.Errorf("no %q line for agent %s", msg, agent)
		}
	}

	calls := rec.find("stiggy: tool call", inStep)
	if len(calls) != 2 { // lead asks aide, aide takes a note
		t.Fatalf("tool calls: %v", calls)
	}

	results := rec.find("stiggy: tool result", map[string]string{"exec": id, "agent": "aide", "tool": "note"})
	if len(results) != 1 || results[0]["result"] != "noted" || results[0]["duration"] == "" {
		t.Fatalf("tool result: %v", results)
	}
}

// TestStepLogsAtInfo checks info keeps the step lines and drops the rest.
func TestStepLogsAtInfo(t *testing.T) {
	rec, id := startLogging(t, slog.LevelInfo)

	if len(rec.find("stiggy: step ended", map[string]string{"exec": id})) != 1 {
		t.Fatal("no step ended line at info")
	}

	for _, msg := range []string{"stiggy: llm request", "stiggy: llm response", "stiggy: tool call", "stiggy: tool result"} {
		if got := rec.find(msg, nil); len(got) != 0 {
			t.Fatalf("%q logged at info: %v", msg, got)
		}
	}
}

// TestExposedAgentLogs checks an exposed agent logs every request it
// answers at info, on the agent's name, with the outcome and the usage, and
// a failed request at warn with err.
func TestExposedAgentLogs(t *testing.T) {
	srv := packtrailtest.Start(t)
	nc := srv.Connect(t)
	rec := newLogRecorder(slog.LevelInfo)

	broken := llm.Func(func(context.Context, []llm.Message, ...llm.CallOption) (*llm.Result, error) {
		return nil, errors.New("model down")
	})

	app, err := stiggy.New(nc,
		stiggy.WithNamespace("exposed-logs"),
		stiggy.WithLogger(slog.New(rec)),
		stiggy.WithModel("m", stiggytest.Echo("echo: ")),
		stiggy.WithModel("broken", broken),
		stiggy.WithAgent("a", spec.Agent{Model: "m"}),
		stiggy.WithAgent("b", spec.Agent{Model: "broken"}),
		stiggy.WithExpose(spec.Expose{Owner: "o", Agents: []string{"a", "b"}}),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := ctxTimeout(t)
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() { done <- app.Run(runCtx) }()

	t.Cleanup(func() {
		stop()
		<-done
	})

	<-app.Ready()

	ask := func(name string) error {
		handles, derr := natsagent.NewClient(nc).Discover(ctx, natsagent.FilterByName(name))
		if derr != nil || len(handles) != 1 {
			t.Fatalf("discover %s: %v %v", name, handles, derr)
		}

		stream, perr := handles[0].Prompt(ctx, "hi")
		if perr != nil {
			return perr
		}
		defer stream.Close()

		_, perr = stream.Text(ctx)

		return perr
	}

	if err = ask("a"); err != nil {
		t.Fatal(err)
	}

	if len(rec.find("stiggy: request started", map[string]string{"agent": "a"})) != 1 {
		t.Fatalf("no request started line: %v", rec.find("stiggy: request started", nil))
	}

	ended := rec.find("stiggy: request ended", map[string]string{"agent": "a", "outcome": "completed"})
	if len(ended) != 1 || ended[0]["tokens_in"] == "" || ended[0]["duration"] == "" || ended[0]["err"] != "" {
		t.Fatalf("request ended: %v", rec.find("stiggy: request ended", nil))
	}

	if err = ask("b"); err == nil {
		t.Fatal("the broken agent answered")
	}

	failed := rec.find("stiggy: request ended", map[string]string{"agent": "b", "outcome": "failed", "level": "WARN"})
	if len(failed) != 1 || failed[0]["err"] == "" {
		t.Fatalf("failed request: %v", rec.find("stiggy: request ended", nil))
	}

	if got := rec.find("stiggy: llm request", nil); len(got) != 0 {
		t.Fatalf("debug lines at info: %v", got)
	}
}
