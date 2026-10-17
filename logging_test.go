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
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
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
	m := map[string]string{"msg": rec.Message}
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
func startLogging(t *testing.T, level slog.Level, opts ...stiggy.Option) (*logRecorder, string) {
	t.Helper()

	rec := newLogRecorder(level)
	h := startLoggingFleet(t, append(opts, stiggy.WithLogger(slog.New(rec)))...)

	return rec, runAsk(context.Background(), t, h)
}

// startLoggingFleet starts loggingFleet with opts.
func startLoggingFleet(t *testing.T, opts ...stiggy.Option) *stiggytest.Harness {
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

	return startYAML(t, loggingFleet, map[string]llm.LLM{"direct": callFirst("note"), "boss": callFirst("ask_aide")},
		append(opts, stiggy.WithTool("note", note))...)
}

// runAsk runs the ask flow from parent (its trace, if any) to completion.
func runAsk(parent context.Context, t *testing.T, h *stiggytest.Harness) string {
	t.Helper()

	ctx, cancel := context.WithTimeout(parent, stiggytest.Timeout)
	defer cancel()

	id, err := h.Client.Start(ctx, "ask", map[string]any{"q": "?"})
	if err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v %s", st, err, st.Error)
	}

	return id
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
