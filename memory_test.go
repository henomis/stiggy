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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/phero/v2/embedding"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/vectorstore"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// historyLens records, per call, how many earlier assistant messages the
// model saw (memory) and the prompt it got.
type historyLens struct {
	mu    sync.Mutex
	calls []int
}

func (h *historyLens) model(answer func(r stiggytest.Request) string) llm.LLM {
	return stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		prior := 0

		for _, m := range r.Messages {
			if m.Role == llm.RoleAssistant {
				prior++
			}
		}

		h.mu.Lock()
		h.calls = append(h.calls, prior)
		h.mu.Unlock()

		return stiggytest.Response{Text: answer(r)}
	})
}

func (h *historyLens) seen() []int {
	h.mu.Lock()
	defer h.mu.Unlock()

	return append([]int(nil), h.calls...)
}

// TestExecutionMemory checks an agent remembers its earlier steps of the
// same execution, the memory stays out of the output, and a rerun sees the
// memory as of its own point in history (time travel).
func TestExecutionMemory(t *testing.T) {
	lens := &historyLens{}

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, memory: execution}}
flows:
  two:
    channels: {last: {}}
    nodes:
      - {id: first, type: task, agent: writer, prompt: "draft", next: second}
      - {id: second, type: task, agent: writer, prompt: "revise", writes: {last: output.text}}
`, map[string]llm.LLM{"m": lens.model(func(r stiggytest.Request) string { return "did " + r.Prompt })})

	st := h.Run(t, "two", map[string]any{})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	if got := lens.seen(); len(got) != 2 || got[0] != 0 || got[1] != 1 {
		t.Fatalf("assistant messages seen per step = %v, want [0 1]", got)
	}

	var out map[string]json.RawMessage
	if err := st.DecodeOutput(&out); err != nil {
		t.Fatal(err)
	}

	if _, leaked := out["_mem_writer"]; leaked || string(out["last"]) != `"did revise"` {
		t.Fatalf("output = %s, want only the user's channel", st.Output)
	}

	// Rerun the second step: its memory is as of before it ran, so the model
	// sees one earlier turn again, not two.
	ctx := ctxTimeout(t)

	id, err := h.Client.Rerun(ctx, st.ExecID, "second")
	if err != nil {
		t.Fatal(err)
	}

	if st, err = h.Client.Wait(ctx, id); err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("rerun: %v %v", st.Status, err)
	}

	if got := lens.seen(); len(got) != 3 || got[2] != 1 {
		t.Fatalf("assistant messages seen = %v, want the rerun to see 1", got)
	}
}

// TestExecutionMemoryAfterApproval checks the turn of a reviewed answer
// reaches execution memory once the human approves it.
func TestExecutionMemoryAfterApproval(t *testing.T) {
	lens := &historyLens{}

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, memory: execution}}
flows:
  two:
    nodes:
      - {id: first, type: task, agent: writer, prompt: "draft", human_input: true, next: second}
      - {id: second, type: task, agent: writer, prompt: "revise"}
`, map[string]llm.LLM{"m": lens.model(func(r stiggytest.Request) string { return "did " + r.Prompt })})

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "two", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, id, "first")

	if err = h.Client.Resume(ctx, id, "first", map[string]any{"approve": true}); err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v (%v)", st.Status, err)
	}

	if got := lens.seen(); len(got) != 2 || got[1] != 1 {
		t.Fatalf("assistant messages seen per call = %v, want the second step to see 1", got)
	}
}

// TestLongTermMemory checks a "simple" memory carries an agent's
// conversation across executions.
func TestLongTermMemory(t *testing.T) {
	lens := &historyLens{}

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {clerk: {model: m, memory: {type: simple, max_items: 50}}}
flows:
  note:
    nodes:
      - {id: note, type: task, agent: clerk}
`, map[string]llm.LLM{"m": lens.model(func(stiggytest.Request) string { return "noted" })})

	for range 2 {
		if st := h.Run(t, "note", map[string]any{"x": 1}); st.Status != packtrail.StatusCompleted {
			t.Fatalf("status %s: %s", st.Status, st.Error)
		}
	}

	if got := lens.seen(); len(got) != 2 || got[1] != 1 {
		t.Fatalf("assistant messages seen = %v, want the second execution to remember the first", got)
	}
}

// TestDelegates covers delegation: the planner asks a helper
// through a tool, within one step, and the helper's run is charged too.
func TestDelegates(t *testing.T) {
	planner := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_helper", Args: map[string]any{"input": "find x"}}}}
		}

		return stiggytest.Response{Text: "plan using: " + r.ToolResults[0]}
	})

	h := startYAML(t, `
models: {p: {provider: x}, h: {provider: x}}
agents:
  planner: {model: p, delegates: [helper]}
  helper: {model: h, description: Finds facts.}
flows:
  plan:
    nodes:
      - {id: plan, type: task, agent: planner}
`, map[string]llm.LLM{"p": planner, "h": stiggytest.Echo("found: ")})

	st := h.Run(t, "plan", map[string]any{})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	var out struct{ Text string }
	if err := st.DecodeOutput(&out); err != nil || !strings.Contains(out.Text, "found: find x") {
		t.Fatalf("output = %q, %v", out.Text, err)
	}

	if st.Counters["agent_steps"] != 2 {
		t.Fatalf("counters = %v, want the delegate's run charged", st.Counters)
	}
}

// memStore is an in-memory vector store for tests.
type memStore struct {
	mu     sync.Mutex
	points map[string]vectorstore.Point
}

func (s *memStore) EnsureCollection(context.Context) error { return nil }

func (s *memStore) Upsert(_ context.Context, pts []vectorstore.Point) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.points == nil {
		s.points = map[string]vectorstore.Point{}
	}

	for _, p := range pts {
		s.points[p.ID] = p
	}

	return nil
}

// Query returns every point whose text shares a word with the query vector's
// source; the fake embedder encodes words as positions.
func (s *memStore) Query(_ context.Context, q vectorstore.Vector, limit uint64, _ ...vectorstore.QueryOption,
) ([]vectorstore.ScoredPoint, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var out []vectorstore.ScoredPoint

	for _, p := range s.points {
		var score float32
		for i := range q {
			score += q[i] * p.Vector[i]
		}

		if score > 0 {
			out = append(out, vectorstore.ScoredPoint{ID: p.ID, Score: score, Payload: p.Payload})
		}
	}

	if uint64(len(out)) > limit {
		out = out[:limit]
	}

	return out, nil
}

func (s *memStore) Delete(context.Context, []string) error { return nil }
func (s *memStore) Clear(context.Context) error            { return nil }

func (s *memStore) Count(context.Context) (uint64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return uint64(len(s.points)), nil
}

// wordEmbedder embeds text as a bag of a few known words.
type wordEmbedder struct{ calls atomic.Int64 }

var embedWords = []string{"refund", "shipping", "warranty"}

func (e *wordEmbedder) Embed(_ context.Context, texts []string) ([]embedding.Vector, error) {
	e.calls.Add(1)

	out := make([]embedding.Vector, len(texts))

	for i, t := range texts {
		v := make(embedding.Vector, len(embedWords))
		for j, w := range embedWords {
			if strings.Contains(strings.ToLower(t), w) {
				v[j] = 1
			}
		}

		out[i] = v
	}

	return out, nil
}

// TestKnowledge covers knowledge (RAG): sources are ingested when the
// engine starts, and the agent searches them through a tool.
func TestKnowledge(t *testing.T) {
	dir := t.TempDir()

	doc := "# Policies\n\nRefunds are issued within 14 days.\n\nShipping takes 3 days.\n"
	if err := os.WriteFile(filepath.Join(dir, "policy.md"), []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	store := &memStore{}

	agentModel := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "search_policies", Args: map[string]any{"query": "refund"}}}}
		}

		return stiggytest.Response{Text: "answer from: " + r.ToolResults[0]}
	})

	step, err := stiggy.AgentTask("answer", "support", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	h := stiggytest.Start(t,
		stiggy.WithModel("m", agentModel),
		stiggy.WithEmbedder("words", &wordEmbedder{}),
		stiggy.WithVectorStore("mem", store),
		stiggy.WithKnowledge("policies", spec.Knowledge{
			Embedder: "words", VectorStore: "mem", Description: "Customer policies.",
			Sources: []spec.Source{{Path: filepath.Join(dir, "*.md"), ChunkSize: 40, ChunkOverlap: 5}},
		}),
		stiggy.WithAgent("support", spec.Agent{Model: "m", Knowledge: []string{"policies"}}),
		stiggy.WithFlow(&flow.Flow{Name: "ask", Nodes: []flow.Node{step}}),
	)

	if n, _ := store.Count(context.Background()); n == 0 {
		t.Fatal("the engine did not ingest the knowledge sources")
	}

	st := h.Run(t, "ask", map[string]any{"q": "refunds?"})

	var out struct{ Text string }
	if err = st.DecodeOutput(&out); err != nil || !strings.Contains(out.Text, "14 days") {
		t.Fatalf("output = %q, %v (status %s %s)", out.Text, err, st.Status, st.Error)
	}
}
