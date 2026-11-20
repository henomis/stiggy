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
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/stiggytest"
)

// records returns the attempt records of node, oldest first.
func records(t *testing.T, h *stiggytest.Harness, id, node string) ([]packtrail.AttemptRecord, []agentrun.Record) {
	t.Helper()

	hist, err := h.Client.RecordHistory(ctxTimeout(t), id, node)
	if err != nil {
		t.Fatal(err)
	}

	out := make([]agentrun.Record, len(hist))

	for i, a := range hist {
		if err = json.Unmarshal(a.Record, &out[i]); err != nil {
			t.Fatalf("record %d: %v: %s", i, err, a.Record)
		}
	}

	return hist, out
}

// lastRequest is a model that remembers the requests it got.
type lastRequest struct {
	mu   sync.Mutex
	reqs []stiggytest.Request
}

func (l *lastRequest) model(fn func(stiggytest.Request) stiggytest.Response) llm.LLM {
	return stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		l.mu.Lock()
		l.reqs = append(l.reqs, r)
		l.mu.Unlock()

		return fn(r)
	})
}

// TestRecordPrompt checks a prompt record: the messages equal what the model
// received, with the system prompt, tools, output schema, timing, usage,
// stop reason and iterations.
func TestRecordPrompt(t *testing.T) {
	var seen lastRequest

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, system: "You write about {{.input.topic}}.", record: prompt}}
flows:
  write:
    nodes:
      - id: draft
        type: task
        agent: writer
        prompt: "Write about {{.input.topic}}."
        output_schema: {type: object, properties: {title: {type: string}}, required: [title]}
`, map[string]llm.LLM{"m": seen.model(func(stiggytest.Request) stiggytest.Response {
		return stiggytest.Response{Text: `{"title":"Go"}`}
	})})

	st := h.Run(t, "write", map[string]any{"topic": "go"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v", st.Status)
	}

	hist, recs := records(t, h, st.ExecID, "draft")
	if len(recs) != 1 || hist[0].Outcome != packtrail.OutcomeCompleted || hist[0].Attempt != 1 {
		t.Fatalf("records = %+v", hist)
	}

	r := recs[0]
	req := seen.reqs[0]

	if r.Version != agentrun.RecordVersion || r.Level != "prompt" || r.Agent != "writer" || r.System != req.System ||
		r.StopReason != "final" || r.Iterations != 1 || r.MaxIterations != 25 || r.OutputSchema == nil ||
		r.ResponseFormat == "" || r.Output != `{"title":"Go"}` || r.TraceID == "" || r.Start.IsZero() ||
		r.End.Before(r.Start) || r.Usage.InputTokens == 0 {
		t.Fatalf("record = %+v", r)
	}

	if len(r.Calls) != 1 {
		t.Fatalf("calls = %+v", r.Calls)
	}

	c := r.Calls[0]
	if c.Start.IsZero() || c.End.Before(c.Start) || c.Usage.InputTokens == 0 || c.Usage.OutputTokens == 0 ||
		c.Response != `{"title":"Go"}` || c.MessageOffset != 0 {
		t.Fatalf("call = %+v", c)
	}

	sameMessages(t, req, r, c)

	if !strings.Contains(c.Messages[len(c.Messages)-1].Text, "Write about go.") {
		t.Errorf("prompt = %q", c.Messages[len(c.Messages)-1].Text)
	}

	// The step's entry does not carry the record's tool calls with prompt.
	if len(c.ToolCalls) != 0 || len(r.Children) != 0 {
		t.Errorf("prompt record has tool calls or children: %+v", r)
	}
}

// sameMessages checks the record lists the system prompt and the messages
// the model got on the call c (the run's first).
func sameMessages(t *testing.T, req stiggytest.Request, r agentrun.Record, c agentrun.RecordCall) {
	t.Helper()

	if r.System != req.System {
		t.Errorf("system: record %q, model %q", r.System, req.System)
	}

	got := make([]llm.Message, 0, len(req.Messages))

	for _, m := range req.Messages {
		if m.Role != llm.RoleSystem {
			got = append(got, m)
		}
	}

	if len(got) != len(c.Messages) {
		t.Fatalf("messages: record %+v, model %+v", c.Messages, got)
	}

	for i, m := range got {
		if c.Messages[i].Role != m.Role || c.Messages[i].Text != m.TextContent() {
			t.Errorf("message %d: record %+v, model %+v", i, c.Messages[i], m)
		}
	}
}

// TestRecordDefaultPrompt checks the record of a step without a prompt
// template, with expected_output: the default prompt as the model got it.
func TestRecordDefaultPrompt(t *testing.T) {
	var seen lastRequest

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, record: prompt}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, expected_output: "Three bullet points."}
`, map[string]llm.LLM{"m": seen.model(func(stiggytest.Request) stiggytest.Response {
		return stiggytest.Response{Text: "- a\n- b\n- c"}
	})})

	st := h.Run(t, "write", map[string]any{"topic": "ants"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v", st.Status)
	}

	_, recs := records(t, h, st.ExecID, "draft")
	if len(recs) != 1 || len(recs[0].Calls) != 1 {
		t.Fatalf("records = %+v", recs)
	}

	r, req := recs[0], seen.reqs[0]
	sameMessages(t, req, r, r.Calls[0])

	p := r.Calls[0].Messages[len(r.Calls[0].Messages)-1].Text
	if p != req.Prompt || !strings.Contains(p, "ants") || !strings.Contains(p, "Expected output:\nThree bullet points.") {
		t.Errorf("prompt: record %q, model %q", p, req.Prompt)
	}
}

// TestRecordQuestion checks a step that asks a human a question: the
// interrupted attempt records the ask_human call, and the resumed run's
// prompt carries the answer.
func TestRecordQuestion(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if i := strings.Index(r.Prompt, "The human answered:\n"); i >= 0 {
			return stiggytest.Response{Text: "painting it " + strings.Fields(r.Prompt[i+len("The human answered:\n"):])[0]}
		}

		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_human", Args: map[string]any{"question": "Which color?"}}}}
	})

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {painter: {model: m, record: full}}
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

	waitInterrupted(t, h, id, "paint")

	if err = h.Client.Resume(ctx, id, "paint", "blue"); err != nil {
		t.Fatal(err)
	}

	if st, werr := h.Client.Wait(ctx, id); werr != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v", st, werr)
	}

	hist, recs := records(t, h, id, "paint")
	if len(recs) != 2 || hist[0].Outcome != packtrail.OutcomeInterrupted || hist[1].Outcome != packtrail.OutcomeCompleted {
		t.Fatalf("records = %+v", hist)
	}

	asked := recs[0].Calls[0].ToolCalls
	if len(asked) != 1 || asked[0].Tool != "ask_human" || !strings.Contains(asked[0].Args, "Which color?") ||
		recs[0].Error != "" {
		t.Errorf("question record = %+v", recs[0])
	}

	c := recs[1].Calls[0]
	if p := c.Messages[len(c.Messages)-1].Text; !strings.Contains(p, "The human answered:\nblue") ||
		recs[1].Output != "painting it blue" {
		t.Errorf("resumed record: prompt %q, output %q", p, recs[1].Output)
	}
}

// TestRecordFull checks a full record: tool calls with arguments and
// results, a failing tool, and a delegate as a child with its own calls.
func TestRecordFull(t *testing.T) {
	note, err := llm.NewTool("note", "Takes a note.", func(_ context.Context, in *struct {
		Input string `json:"input"`
	},
	) (string, error) {
		if in.Input == "bad" {
			return "", errors.New("cannot note that")
		}

		return "noted " + in.Input, nil
	})
	if err != nil {
		t.Fatal(err)
	}

	lead := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch len(r.ToolResults) {
		case 0:
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "note", Args: map[string]any{"input": "bad"}}}}
		case 1:
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_aide", Args: map[string]any{"input": "go"}}}}
		default:
			return stiggytest.Response{Text: "done"}
		}
	})
	aide := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "note", Args: map[string]any{"input": "x"}}}}
		}

		return stiggytest.Response{Text: "aide: " + r.ToolResults[0]}
	})

	h := startYAML(t, `
models: {direct: {provider: x}, boss: {provider: x}}
agents:
  lead: {model: boss, tools: [note], delegates: [aide], record: full}
  aide: {model: direct, tools: [note]}
flows:
  ask:
    nodes:
      - {id: answer, type: task, agent: lead}
`, map[string]llm.LLM{"boss": lead, "direct": aide}, stiggy.WithTool("note", note))

	st := h.Run(t, "ask", map[string]any{"q": "?"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v", st.Status)
	}

	_, recs := records(t, h, st.ExecID, "answer")
	if len(recs) != 1 {
		t.Fatalf("records = %d", len(recs))
	}

	r := recs[0]
	if r.Level != "full" || len(r.Calls) != 3 || r.Iterations != 3 {
		t.Fatalf("record = %+v", r)
	}

	failed := r.Calls[0].ToolCalls
	if len(failed) != 1 || failed[0].Tool != "note" || !strings.Contains(failed[0].Args, "bad") ||
		failed[0].Error == "" && !strings.Contains(failed[0].Result, "cannot note") {
		t.Errorf("failing tool call = %+v", failed)
	}

	// Later calls list the model's own messages and the tool results.
	roles := make([]string, 0, len(r.Calls[1].Messages))
	for _, m := range r.Calls[1].Messages {
		roles = append(roles, m.Role)
	}

	if strings.Join(roles, ",") != "assistant,tool" || r.Calls[1].MessageOffset == 0 {
		t.Errorf("second call: offset %d, roles %v", r.Calls[1].MessageOffset, roles)
	}

	del := r.Calls[1].ToolCalls
	if len(del) != 1 || del[0].Tool != "ask_aide" || !strings.Contains(del[0].Result, "aide: noted x") {
		t.Errorf("delegate call = %+v", del)
	}

	if len(r.Children) != 1 {
		t.Fatalf("children = %+v", r.Children)
	}

	c := r.Children[0]
	if c.Agent != "aide" || c.Via != agentrun.ViaDelegate || c.Tool != "ask_aide" || len(c.Calls) != 2 ||
		len(c.Calls[0].ToolCalls) != 1 || c.Calls[0].ToolCalls[0].Result != "noted x" || c.Output != "aide: noted x" {
		t.Errorf("child = %+v", c)
	}

	if r.Usage.InputTokens <= c.Usage.InputTokens {
		t.Errorf("usage: lead %+v, aide %+v; the lead's includes the aide's", r.Usage, c.Usage)
	}
}

// TestRecordOff checks an agent without record stores none. That a remote
// cannot be given one is checked by config's TestStrictDecoding.
func TestRecordOff(t *testing.T) {
	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer}
`, map[string]llm.LLM{"m": stiggytest.Replies("hi")})

	st := h.Run(t, "write", map[string]any{})

	if hist, _ := records(t, h, st.ExecID, "draft"); len(hist) != 0 {
		t.Fatalf("records = %+v", hist)
	}
}

// TestRecordTruncated checks a record over record_max_bytes is cut, with
// its marks, and still fits.
func TestRecordTruncated(t *testing.T) {
	long := strings.Repeat("abcdefgh", 4096)

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, record: full, record_max_bytes: 4096}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, prompt: "{{.input.text}}"}
`, map[string]llm.LLM{"m": stiggytest.Replies(long)})

	st := h.Run(t, "write", map[string]any{"text": long})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v", st.Status)
	}

	hist, recs := records(t, h, st.ExecID, "draft")
	if len(recs) != 1 || len(hist[0].Record) > 4096 {
		t.Fatalf("records: %d, size %d", len(recs), len(hist[0].Record))
	}

	r := recs[0]
	c := r.Calls[0]
	last := c.Messages[len(c.Messages)-1]

	if !r.Truncated || !r.OutputTruncated || !c.ResponseTruncated || !last.Truncated ||
		!strings.HasPrefix(long, c.Response) || len(c.Response) == 0 {
		t.Fatalf("record = %+v", r)
	}
}

// TestRecordAttempts checks each attempt has its own record: a failed one
// that is retried, an interrupted one, and those of a resumed run, told
// apart by generation; the approved-answer run records nothing.
func TestRecordAttempts(t *testing.T) {
	model := &flaky{LLM: stiggytest.Replies("draft", "draft")}

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, record: prompt}}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, human_input: true, retry: {max_attempts: 2, delay: 10ms}}
`, map[string]llm.LLM{"m": model})

	ctx := ctxTimeout(t)

	id, err := h.Client.Start(ctx, "write", map[string]any{})
	if err != nil {
		t.Fatal(err)
	}

	waitInterrupted(t, h, id, "draft")

	if err = h.Client.Resume(ctx, id, "draft", map[string]any{"feedback": "more"}); err != nil {
		t.Fatal(err)
	}

	waitInterruptedWith(t, h, id, "draft", "more")

	if err = h.Client.Resume(ctx, id, "draft", map[string]any{"approve": true}); err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v: %v", st, err)
	}

	hist, recs := records(t, h, id, "draft")

	got := make([]string, 0, len(hist))
	for _, a := range hist {
		got = append(got, a.Outcome)
	}

	// The first attempt's model call fails and is retried; the second
	// asks for review; the resumed run asks again; the approval runs no
	// model and records nothing.
	if strings.Join(got, ",") != "failed,interrupted,interrupted" {
		t.Fatalf("outcomes = %v", got)
	}

	if hist[0].Generation != hist[1].Generation || hist[0].Attempt != 1 || hist[1].Attempt != 2 ||
		hist[2].Generation == hist[1].Generation || hist[2].Attempt != 1 {
		t.Errorf("attempts = %+v", hist)
	}

	if recs[0].Error == "" || recs[0].StopReason != "error" || len(recs[0].Calls) != 1 ||
		recs[0].Calls[0].Error == "" {
		t.Errorf("failed record = %+v", recs[0])
	}

	// The revised run's prompt carries the transcript of the review.
	c := recs[2].Calls[0]
	if p := c.Messages[len(c.Messages)-1].Text; !strings.Contains(p, "asked for changes:\nmore") {
		t.Errorf("revised prompt = %q", p)
	}
}

// flaky fails its first call.
type flaky struct {
	llm.LLM

	calls atomic.Int64
}

func (f *flaky) Execute(ctx context.Context, msgs []llm.Message, opts ...llm.CallOption) (*llm.Result, error) {
	if f.calls.Add(1) == 1 {
		return nil, errors.New("flaky: try again")
	}

	return f.LLM.Execute(ctx, msgs, opts...)
}

// waitInterruptedWith waits until node is interrupted with a payload that
// contains text.
func waitInterruptedWith(t *testing.T, h *stiggytest.Harness, id, node, text string) {
	t.Helper()

	_, err := h.Client.WaitUntil(ctxTimeout(t), id, func(st *packtrail.State) bool {
		for _, task := range st.Tasks {
			if task.Node == node && task.Status == "interrupted" && strings.Contains(string(task.Interrupt), text) {
				return true
			}
		}

		return false
	})
	if err != nil {
		t.Fatal(err)
	}
}

// TestRecordMemoryAndMap checks the record of a step with execution memory
// (the retrieved turn is listed and is part of the first call's messages)
// and of each item of a map, each with its own prompt.
func TestRecordMemoryAndMap(t *testing.T) {
	var seen lastRequest

	h := startYAML(t, `
models: {m: {provider: x}}
agents: {writer: {model: m, memory: execution, record: prompt}}
flows:
  two:
    output: "{first: results.first.text}"
    nodes:
      - {id: first, type: task, agent: writer, prompt: "draft", next: each}
      - {id: each, type: map, over: input.items, agent: writer, prompt: "item {{.item}}"}
`, map[string]llm.LLM{"m": seen.model(func(r stiggytest.Request) stiggytest.Response {
		return stiggytest.Response{Text: "did " + r.Prompt}
	})})

	st := h.Run(t, "two", map[string]any{"items": []string{"a", "b"}})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	_, recs := records(t, h, st.ExecID, "each")
	if len(recs) != 2 {
		t.Fatalf("records = %d", len(recs))
	}

	prompts := map[string]bool{}

	for _, r := range recs {
		c := r.Calls[0]
		prompts[c.Messages[len(c.Messages)-1].Text] = true

		// The item's turn is saved; with prompt, the saved messages are
		// counted but not listed.
		if r.Memory == nil || r.Memory.Retrieved == 0 || len(r.Memory.RetrievedItems) != r.Memory.Retrieved ||
			r.Memory.Saved != 2 || len(r.Memory.SavedItems) != 0 {
			t.Fatalf("memory = %+v", r.Memory)
		}

		// The remembered turn comes first in the prompt.
		if c.Messages[0].Text != "draft" || c.Messages[1].Text != "did draft" {
			t.Errorf("messages = %+v", c.Messages)
		}
	}

	if !prompts["item a"] || !prompts["item b"] {
		t.Errorf("prompts = %v", prompts)
	}
}
