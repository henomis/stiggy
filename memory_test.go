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
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
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
	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/registry"
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

// TestCompaction checks an agent's conversation is compacted: the summary
// model replaces the older messages, the agent's model sees the summary
// instead of them, and a streaming agent reports the compaction as progress.
func TestCompaction(t *testing.T) {
	var summaries atomic.Int32

	summarizer := stiggytest.Model(func(stiggytest.Request) stiggytest.Response {
		summaries.Add(1)

		return stiggytest.Response{Text: "SUMMARY"}
	})

	var (
		mu   sync.Mutex
		seen [][]llm.Message
	)

	clerk := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		mu.Lock()
		defer mu.Unlock()

		seen = append(seen, r.Messages)

		return stiggytest.Response{Text: "noted"}
	})

	h := startYAML(t, `
models: {m: {provider: x}, s: {provider: x}}
agents:
  clerk:
    model: m
    stream: true
    compaction: {model: s, at_tokens: 1, keep_recent: 1}
    memory: {type: simple}
flows:
  note:
    nodes:
      - {id: note, type: task, agent: clerk}
`, map[string]llm.LLM{"m": clerk, "s": summarizer})

	if st := h.Run(t, "note", map[string]any{"x": 1}); st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	ctx := ctxTimeout(t)

	// Progress is not stored: subscribe before starting, with a known id.
	events, err := h.Client.Progress(ctx, "note-2")
	if err != nil {
		t.Fatal(err)
	}

	if _, err = h.Client.Start(ctx, "note", map[string]any{"x": 1}, packtrail.WithExecutionID("note-2")); err != nil {
		t.Fatal(err)
	}

	var compactions []agentrun.Progress

	for ev := range events {
		var p agentrun.Progress
		if json.Unmarshal(ev.Data, &p) == nil && p.Type == agentrun.ProgressCompaction {
			compactions = append(compactions, p)
		}
	}

	st, err := h.Client.Wait(ctx, "note-2")
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("second execution: %v, %v", st, err)
	}

	if summaries.Load() == 0 {
		t.Fatal("the summary model was never called")
	}

	// The summary call counts in the step, next to the clerk's own.
	if st.Counters["llm_calls"] != 2 {
		t.Errorf("llm_calls = %v, want the clerk's call and the summary's", st.Counters["llm_calls"])
	}

	// The messages here are so short that the summary is no smaller than
	// what it replaces: only the sizes' presence is checked.
	if len(compactions) != 1 || compactions[0].Trigger != "threshold" || compactions[0].IsError ||
		compactions[0].TokensBefore == 0 || compactions[0].TokensAfter == 0 {
		t.Fatalf("compaction progress = %+v, want one threshold compaction", compactions)
	}

	mu.Lock()
	defer mu.Unlock()

	last := seen[len(seen)-1]
	if !slices.ContainsFunc(last, func(m llm.Message) bool { return strings.Contains(m.TextContent(), "SUMMARY") }) ||
		slices.ContainsFunc(last, func(m llm.Message) bool { return m.Role == llm.RoleAssistant }) {
		t.Fatalf("second execution saw %v, want the summary instead of the first answer", last)
	}
}

// TestTemplatedMemorySession checks a session template gives each user a
// memory of their own, and a missing key fails the step instead of sharing
// a session.
func TestTemplatedMemorySession(t *testing.T) {
	lens := &historyLens{}

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {clerk: {model: m, memory: {type: simple, session: "user-{{.input.user}}"}}}
flows:
  note:
    nodes:
      - {id: note, type: task, agent: clerk}
`, map[string]llm.LLM{"m": lens.model(func(stiggytest.Request) string { return "noted" })})

	for _, user := range []string{"ann", "ann", "bob"} {
		if st := h.Run(t, "note", map[string]any{"user": user}); st.Status != packtrail.StatusCompleted {
			t.Fatalf("status %s: %s", st.Status, st.Error)
		}
	}

	if got := lens.seen(); len(got) != 3 || got[1] != 1 || got[2] != 0 {
		t.Fatalf("assistant messages seen = %v, want [0 1 0]: ann remembers, bob starts fresh", got)
	}

	for _, input := range []map[string]any{{}, {"user": "a b"}} {
		st := h.Run(t, "note", input)
		if st.Status != packtrail.StatusFailed || !strings.Contains(st.Error, "session") {
			t.Fatalf("input %v: status %s (%s), want a session failure", input, st.Status, st.Error)
		}
	}

	if got := lens.seen(); len(got) != 3 {
		t.Fatalf("model calls = %d, want none for a failed session", len(got))
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

// TestVectorStoreDefer checks that a vector store type may register its
// cleanup through Env.Defer while the app builds its knowledge.
func TestVectorStoreDefer(t *testing.T) {
	var closed atomic.Bool

	reg := registry.New()
	if err := reg.RegisterVectorStoreType("deferring", registry.NewVectorStoreType(
		func(_ context.Context, _ struct{}, env registry.Env) (vectorstore.Store, error) {
			env.Defer(func() error { closed.Store(true); return nil })

			return &memStore{}, nil
		})); err != nil {
		t.Fatal(err)
	}

	t.Run("run", func(t *testing.T) {
		startYAML(t, `
models: {m: {provider: x}}
vectorstores: {vs: {type: deferring}}
knowledge:
  docs: {embedder: words, vectorstore: vs, description: Docs.}
agents:
  support: {model: m, knowledge: [docs]}
flows:
  ask:
    nodes:
      - {id: a, type: task, agent: support}
`, map[string]llm.LLM{"m": stiggytest.Reply(func(string) string { return "ok" })},
			stiggy.WithRegistry(reg), stiggy.WithEmbedder("words", &wordEmbedder{}))
	})

	if !closed.Load() {
		t.Fatal("the vector store's deferred cleanup did not run")
	}
}

// TestTemplatedSystemPrompt checks system prompt fields render per job: a
// map gives each item its own persona, and a delegate's system prompt
// renders over the delegating job's context.
func TestTemplatedSystemPrompt(t *testing.T) {
	persona := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		return stiggytest.Response{Text: r.System}
	})

	host := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_guide", Args: map[string]any{"input": "hi"}}}}
		}

		return stiggytest.Response{Text: r.ToolResults[0]}
	})

	h := startYAML(t, `
models: {p: {provider: x}, h: {provider: x}}
agents:
  persona: {model: p, role: "{{.item.name}}", backstory: "You live in {{.input.city}}."}
  host: {model: h, delegates: [guide]}
  guide: {model: p, system: "Guide for {{.input.city}}."}
flows:
  town:
    channels: {voices: {reducer: append}}
    output: "{voices: channels.voices, host: results.host.text}"
    nodes:
      - {id: talk, type: map, over: input.people, agent: persona, writes: {voices: output.text}, next: host}
      - {id: host, type: task, agent: host}
`, map[string]llm.LLM{"p": persona, "h": host})

	st := h.Run(t, "town", map[string]any{"city": "Rome", "people": []any{map[string]any{"name": "Ada"}, map[string]any{"name": "Bo"}}})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	var out struct {
		Voices []string
		Host   string
	}
	if err := st.DecodeOutput(&out); err != nil {
		t.Fatal(err)
	}

	voices := strings.Join(out.Voices, " | ")
	for _, w := range []string{"Role: Ada\n\nBackstory: You live in Rome.", "Role: Bo\n\nBackstory: You live in Rome."} {
		if !strings.Contains(voices, w) {
			t.Errorf("voices = %q, want %q", voices, w)
		}
	}

	if !strings.Contains(out.Host, "Guide for Rome.") {
		t.Errorf("host = %q, want the delegate's rendered system prompt", out.Host)
	}
}

// TestJobFromContext checks a Go tool learns which step, map item and
// agent call it, also through a delegate, so it never relies on the model
// to say who it is.
func TestJobFromContext(t *testing.T) {
	whoami, err := llm.NewTool("whoami", "Tells who is asking.",
		func(ctx context.Context, _ *struct{}) (string, error) {
			job, ok := stiggy.JobFromContext(ctx)
			if !ok {
				return "", errors.New("no job")
			}

			var item struct{ Name string }
			if err := job.DecodeItem(&item); err != nil {
				return "", err
			}

			return fmt.Sprintf("%s/%s/%s/%d/%d/%s", job.Flow, job.Node, job.Agent, *job.Index, job.Attempt, item.Name), nil
		})
	if err != nil {
		t.Fatal(err)
	}

	// Each model calls the one tool it has, then answers with its result.
	callFirst := func(tool string) llm.LLM {
		return stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
			if len(r.ToolResults) == 0 {
				return stiggytest.Response{Calls: []stiggytest.Call{{Tool: tool, Args: map[string]any{"input": "who?"}}}}
			}

			return stiggytest.Response{Text: r.ToolResults[0]}
		})
	}

	h := startYAML(t, `
models: {direct: {provider: x}, boss: {provider: x}}
agents:
  persona: {model: direct, tools: [id]}
  lead: {model: boss, delegates: [aide]}
  aide: {model: direct, tools: [id]}
flows:
  town:
    channels: {voices: {reducer: append}}
    output: "{voices: channels.voices}"
    nodes:
      - {id: talk, type: map, over: input.people, agent: persona, writes: {voices: output.text}, next: relay}
      - {id: relay, type: map, over: input.people, agent: lead, writes: {voices: output.text}}
`, map[string]llm.LLM{"direct": callFirst("whoami"), "boss": callFirst("ask_aide")}, stiggy.WithTool("id", whoami))

	st := h.Run(t, "town", map[string]any{"people": []any{map[string]any{"name": "Ada"}, map[string]any{"name": "Bo"}}})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	var out struct{ Voices []string }
	if err = st.DecodeOutput(&out); err != nil {
		t.Fatal(err)
	}

	voices := strings.Join(out.Voices, " | ")
	for _, w := range []string{"town/talk/persona/0/1/Ada", "town/talk/persona/1/1/Bo", "town/relay/lead/0/1/Ada", "town/relay/lead/1/1/Bo"} {
		if !strings.Contains(voices, w) {
			t.Errorf("voices = %q, want %q", voices, w)
		}
	}
}

// TestStepID checks a tool's step id is stable across a retry of the step
// and changes on the next visit of the node: the idempotency key of a tool
// with side effects.
func TestStepID(t *testing.T) {
	type seen struct {
		step    string
		attempt int
	}

	var (
		mu    sync.Mutex
		calls []seen
		runs  atomic.Int32
	)

	record, err := llm.NewTool("record", "Records the call.", func(ctx context.Context, _ *struct{}) (string, error) {
		job, ok := stiggy.JobFromContext(ctx)
		if !ok {
			return "", errors.New("no job")
		}

		mu.Lock()

		calls = append(calls, seen{job.StepID, job.Attempt})

		mu.Unlock()

		// One store key per step: a retry overwrites its own write.
		key := fmt.Sprintf("%x", sha256.Sum256([]byte(job.StepID)))[:16]
		if err := job.Store.Put(ctx, "steps-"+job.ExecID, key, job.Attempt); err != nil {
			return "", err
		}

		return "recorded", nil
	})
	if err != nil {
		t.Fatal(err)
	}

	// Every run calls the tool; the first one then answers empty, so its
	// step is retried.
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "record", Args: map[string]any{}}}}
		}

		if runs.Add(1) == 1 {
			return stiggytest.Response{Text: ""}
		}

		return stiggytest.Response{Text: "done"}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {clerk: {model: m, tools: [rec]}}
flows:
  loop:
    start: act
    nodes:
      - {id: act, type: task, agent: clerk, retry: {max_attempts: 2, delay: 10ms}, next: again}
      - id: again
        type: choice
        rules:
          - {when: 'visits["act"] < 2', to: act}
          - {default: true, to: $end}
`, map[string]llm.LLM{"m": model}, stiggy.WithTool("rec", record))

	st := h.Run(t, "loop", map[string]any{})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	// The retried visit and the second visit: two keys, three writes.
	keys, err := h.Client.Store().Keys(ctxTimeout(t), "steps-"+st.ExecID)
	if err != nil || len(keys) != 2 {
		t.Fatalf("store keys = %v, %v; want one per step", keys, err)
	}

	mu.Lock()
	defer mu.Unlock()

	if len(calls) != 3 {
		t.Fatalf("calls = %v, want 3 (a retried visit, then a second visit)", calls)
	}

	if calls[0].step != calls[1].step || calls[0].attempt != 1 || calls[1].attempt != 2 {
		t.Errorf("retry: calls = %v, want one step id over attempts 1 and 2", calls)
	}

	if calls[2].step == calls[0].step || calls[2].attempt != 1 {
		t.Errorf("revisit: calls = %v, want a new step id at attempt 1", calls)
	}
}
