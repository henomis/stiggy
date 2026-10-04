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
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// startYAML runs a YAML fleet whose models are replaced by the given fakes.
func startYAML(t *testing.T, doc string, models map[string]llm.LLM, opts ...stiggy.Option) *stiggytest.Harness {
	t.Helper()

	fleet, src, err := config.Parse([]byte(doc))
	if err != nil {
		t.Fatalf("%s", strings.Join(src.Describe(err), "\n"))
	}

	for name, l := range models {
		delete(fleet.Models, name)

		opts = append(opts, stiggy.WithModel(name, l))
	}

	return stiggytest.Start(t, append([]stiggy.Option{stiggy.WithFleet(fleet)}, opts...)...)
}

func ctxTimeout(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), stiggytest.Timeout)
	t.Cleanup(cancel)

	return ctx
}

// TestRoutes covers agent-chosen routing, as in a review loop: the critic
// sends the draft back once, then lets the flow continue.
func TestRoutes(t *testing.T) {
	var reviews atomic.Int64

	critic := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if !slices.Contains(r.Tools, "route_to_write") || !strings.Contains(r.System, "route_to_") {
			return stiggytest.Response{Text: "route tools missing"}
		}

		if len(r.ToolResults) == 0 && reviews.Add(1) == 1 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_write", Args: map[string]any{"reason": "too long"}}}}
		}

		return stiggytest.Response{Text: "looks good"}
	})

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
		stiggy.WithActivity("done", func(context.Context, *worker.Job) (*worker.Result, error) {
			return &worker.Result{Output: map[string]any{"ok": true}}, nil
		}))

	st := h.Run(t, "draft", map[string]any{"topic": "nats"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	if st.Visits["write"] != 2 || st.Visits["review"] != 2 {
		t.Fatalf("visits = %v, want write and review twice", st.Visits)
	}

	var rev struct{ Text string }
	if err := st.Result("review", &rev); err != nil || rev.Text != "looks good" {
		t.Fatalf("review = %+v, %v", rev, err)
	}
}

// TestOutputSchemaMapAndWrites is map-reduce: a planner returns structured topics, a researcher runs
// once per topic, and an append channel gathers the notes.
func TestOutputSchemaMapAndWrites(t *testing.T) {
	var sawSchema atomic.Bool

	planner := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		sawSchema.Store(r.Schema)

		return stiggytest.Response{Text: "```json\n{\"topics\": [\"jetstream\", \"kv\"]}\n```"}
	})

	h := startYAML(t, `
models: {p: {provider: x}, r: {provider: x}}
agents:
  planner: {model: p}
  researcher: {model: r}
flows:
  research:
    channels: {notes: {reducer: append}}
    nodes:
      - id: plan
        type: task
        agent: planner
        expected_output: A list of topics.
        output_schema:
          type: object
          properties: {topics: {type: array, items: {type: string}}}
          required: [topics]
        next: dig
      - id: dig
        type: map
        over: results.plan.topics
        max_parallel: 2
        agent: researcher
        prompt: "notes on {{.item}}"
        writes: {notes: output.text}
`, map[string]llm.LLM{"p": planner, "r": stiggytest.Echo("")})

	st := h.Run(t, "research", map[string]any{"goal": "learn nats"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	if !sawSchema.Load() {
		t.Error("the planner was not given the output schema")
	}

	var notes []string
	if err := st.Channel("notes", &notes); err != nil {
		t.Fatal(err)
	}

	slices.Sort(notes)

	if !slices.Equal(notes, []string{"notes on jetstream", "notes on kv"}) {
		t.Fatalf("notes = %v", notes)
	}
}

// TestOutputSchemaViolationFails checks packtrail rejects an answer that is
// JSON but breaks the schema.
func TestOutputSchemaViolationFails(t *testing.T) {
	h := startYAML(t, `
models: {p: {provider: x}}
agents: {planner: {model: p}}
flows:
  f:
    nodes:
      - id: plan
        type: task
        agent: planner
        output_schema: {type: object, required: [topics]}
`, map[string]llm.LLM{"p": stiggytest.Replies(`{"other": 1}`)})

	st := h.Run(t, "f", map[string]any{})
	if st.Status != packtrail.StatusFailed {
		t.Fatalf("status %s, want failed", st.Status)
	}
}

// waitInterrupted waits until node is paused for a human and returns the
// interrupt payload.
func waitInterrupted(t *testing.T, h *stiggytest.Harness, id, node string) map[string]any {
	t.Helper()

	var payload map[string]any

	_, err := h.Client.WaitUntil(ctxTimeout(t), id, func(st *packtrail.State) bool {
		for _, task := range st.Tasks {
			if task.Node == node && task.Status == "interrupted" {
				return json.Unmarshal(task.Interrupt, &payload) == nil
			}
		}

		return false
	})
	if err != nil {
		t.Fatalf("waiting for %s to be interrupted: %v", node, err)
	}

	return payload
}

// TestAskHuman covers ask_human: the agent asks a question, the
// execution pauses, and the resumed agent sees the answer.
func TestAskHuman(t *testing.T) {
	var promptBytes atomic.Int64

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		promptBytes.Add(int64(len(r.Prompt)))

		if i := strings.Index(r.Prompt, "The human answered:\n"); i >= 0 {
			return stiggytest.Response{Text: "painting it " + strings.Fields(r.Prompt[i+len("The human answered:\n"):])[0]}
		}

		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_human", Args: map[string]any{"question": "Which color?"}}}}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {painter: {model: m}}
flows:
  paint:
    nodes:
      - {id: paint, type: task, agent: painter, routes: [human]}
`, map[string]llm.LLM{"m": model})

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "paint", map[string]any{"wall": "north"})
	if err != nil {
		t.Fatal(err)
	}

	payload := waitInterrupted(t, h, id, "paint")
	if payload["kind"] != "question" || payload["question"] != "Which color?" {
		t.Fatalf("payload = %v", payload)
	}

	if err = h.Client.Resume(ctx, id, "paint", "blue"); err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v (%v)", st.Status, err)
	}

	var out struct{ Text string }
	if err = st.DecodeOutput(&out); err != nil || out.Text != "painting it blue" {
		t.Fatalf("output = %+v, %v", out, err)
	}

	// Both runs are charged: the one that asked (stopped by ask_human) and
	// the resumed one. The fake reports one input token per prompt byte.
	if st.Counters["agent_steps"] != 2 || st.Counters["tokens_in"] != float64(promptBytes.Load()) {
		t.Fatalf("counters = %v, want agent_steps 2 and tokens_in %d", st.Counters, promptBytes.Load())
	}
}

// TestHumanReview covers human_input: the answer is reviewed, revised
// on feedback, and kept once approved.
func TestHumanReview(t *testing.T) {
	var calls atomic.Int64

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		calls.Add(1)

		if strings.Contains(r.Prompt, "asked for changes:\nshorter") {
			return stiggytest.Response{Text: "short draft"}
		}

		return stiggytest.Response{Text: "a very long draft"}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, human_input: true}
`, map[string]llm.LLM{"m": model})

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "write", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	p := waitInterrupted(t, h, id, "draft")
	if p["kind"] != "review" {
		t.Fatalf("payload = %v", p)
	}

	if err = h.Client.Resume(ctx, id, "draft", map[string]any{"feedback": "shorter"}); err != nil {
		t.Fatal(err)
	}

	// The revised answer is reviewed again.
	_, err = h.Client.WaitUntil(ctx, id, func(st *packtrail.State) bool {
		for _, task := range st.Tasks {
			if task.Node == "draft" && task.Status == "interrupted" && strings.Contains(string(task.Interrupt), "short draft") {
				return true
			}
		}

		return false
	})
	if err != nil {
		t.Fatal(err)
	}

	if err = h.Client.Resume(ctx, id, "draft", map[string]any{"approve": true}); err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v (%v)", st.Status, err)
	}

	var out struct{ Text string }
	if err = st.DecodeOutput(&out); err != nil || out.Text != "short draft" {
		t.Fatalf("output = %+v, %v", out, err)
	}

	if calls.Load() != 2 || st.Counters["agent_steps"] != 2 {
		t.Fatalf("model calls = %d, agent_steps = %v; approval must not re-run the agent",
			calls.Load(), st.Counters["agent_steps"])
	}
}

// TestBudget is the recursion limit: an agent that always loops back is
// stopped by its agent_steps budget.
func TestBudget(t *testing.T) {
	loop := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_think", Args: map[string]any{}}}}
		}

		return stiggytest.Response{Text: "again"}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {thinker: {model: m}}
flows:
  loop:
    start: think
    budget: {agent_steps: 3}
    nodes:
      - {id: think, type: task, agent: thinker, routes: [think]}
`, map[string]llm.LLM{"m": loop})

	st := h.Run(t, "loop", map[string]any{})
	if st.Status != packtrail.StatusFailed || !strings.Contains(st.Reason, "budget") {
		t.Fatalf("status %s, reason %q", st.Status, st.Reason)
	}
}

// TestStreaming checks a streaming agent publishes its text as progress.
func TestStreaming(t *testing.T) {
	step, err := stiggy.AgentTask("say", "talker", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	h := stiggytest.Start(t,
		stiggy.WithModel("m", stiggytest.Echo("streamed: ")),
		stiggy.WithAgent("talker", spec.Agent{Model: "m", Stream: true}),
		stiggy.WithFlow(&flow.Flow{Name: "talk", Nodes: []flow.Node{step}}),
	)

	ctx := ctxTimeout(t)

	// Progress is not stored: subscribe before starting, with a known id.
	events, err := h.Client.Progress(ctx, "talk-1")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = h.Client.Start(ctx, "talk", map[string]any{"x": 1}, packtrail.WithExecutionID("talk-1")); err != nil {
		t.Fatal(err)
	}

	var text strings.Builder

	for ev := range events {
		var p struct{ Type, Text, Agent string }
		if json.Unmarshal(ev.Data, &p) == nil && p.Type == "text" && p.Agent == "talker" {
			text.WriteString(p.Text)
		}
	}

	if !strings.HasPrefix(text.String(), "streamed: ") {
		t.Fatalf("streamed text = %q", text.String())
	}
}

// TestForkWhilePaused checks the human-in-the-loop state lives in the event
// log: a fork of an execution paused on a question, resumed in the fork,
// still has the question in its transcript (time travel).
func TestForkWhilePaused(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if strings.Contains(r.Prompt, "You asked a human:\nWhich color?\nThe human answered:\ngreen") {
			return stiggytest.Response{Text: "ok, green"}
		}

		if strings.Contains(r.Prompt, "The human answered:") {
			return stiggytest.Response{Text: "transcript lost: " + r.Prompt}
		}

		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_human", Args: map[string]any{"question": "Which color?"}}}}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {painter: {model: m}}
flows:
  paint:
    nodes:
      - {id: paint, type: task, agent: painter, routes: [human]}
`, map[string]llm.LLM{"m": model})

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "paint", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, id, "paint")

	st, err := h.Client.Get(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	fork, err := h.Client.Fork(ctx, id, st.LastSeq)
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, fork, "paint")

	if err = h.Client.Resume(ctx, fork, "paint", "green"); err != nil {
		t.Fatal(err)
	}

	fst, err := h.Client.Wait(ctx, fork)
	if err != nil || fst.Status != packtrail.StatusCompleted {
		t.Fatalf("fork status %v (%v)", fst.Status, err)
	}

	var out struct{ Text string }
	if err = fst.DecodeOutput(&out); err != nil || out.Text != "ok, green" {
		t.Fatalf("fork output = %q, %v", out.Text, err)
	}

	// The source is untouched and still waiting.
	waitInterrupted(t, h, id, "paint")
}

// TestFailedAttemptsAreCharged checks every failed attempt's tokens reach
// the counters: an answer that never parses as the schema's JSON is retried,
// and all attempts are charged.
func TestFailedAttemptsAreCharged(t *testing.T) {
	h := startYAML(t, `
models: {m: {provider: x}}
agents: {planner: {model: m}}
flows:
  f:
    nodes:
      - id: plan
        type: task
        agent: planner
        retry: {max_attempts: 3, delay: 10ms}
        output_schema: {type: object}
`, map[string]llm.LLM{"m": stiggytest.Echo("not json: ")})

	st := h.Run(t, "f", map[string]any{"q": 1})
	if st.Status != packtrail.StatusFailed {
		t.Fatalf("status %s, want failed", st.Status)
	}

	if st.Counters["agent_steps"] != 3 || st.Counters["tokens_out"] == 0 {
		t.Fatalf("counters = %v, want 3 charged attempts", st.Counters)
	}
}

// TestBudgetStopsBeforeAsking checks a pause whose run already exceeds the
// budget fails the execution instead of waiting for a human.
func TestBudgetStopsBeforeAsking(t *testing.T) {
	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m}}
flows:
  write:
    budget: {tokens_out: 5}
    nodes:
      - {id: draft, type: task, agent: writer, human_input: true}
`, map[string]llm.LLM{"m": stiggytest.Replies("a draft far longer than five tokens")})

	st := h.Run(t, "write", map[string]any{})
	if st.Status != packtrail.StatusFailed || !strings.Contains(st.Reason, "budget") {
		t.Fatalf("status %s, reason %q", st.Status, st.Reason)
	}

	if st.FailedNode != "draft" {
		t.Fatalf("failed node = %q, want draft", st.FailedNode)
	}
}
