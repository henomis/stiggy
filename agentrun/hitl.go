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
	"encoding/json"
	"fmt"
	"maps"
	"strings"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/spec"
)

// Interrupt kinds.
const (
	kindQuestion = "question"
	kindReview   = "review"
)

// Interrupt is the payload of an agent step that waits for a human, shown in
// the execution state (State.Tasks[...].Interrupt). Resume the node with an
// answer:
//   - a question: "text" or {"answer": "..."};
//   - a review: {"approve": true}, or "text" / {"feedback": "..."} to have
//     the agent revise its answer.
type Interrupt struct {
	Kind     string `json:"kind"`
	Agent    string `json:"agent"`
	Node     string `json:"node"`
	Question string `json:"question,omitempty"`
	Answer   any    `json:"answer,omitempty"`
	// Stiggy is the step's state across pauses. It travels in the payload,
	// which packtrail hands back to every attempt of the resumed run, so it
	// lives in the event log: forks and reruns see exactly their own history.
	Stiggy record `json:"stiggy"`
}

// record is a step's exchanges with a human so far.
type record struct {
	Turns   []turn   `json:"turns,omitempty"`
	Pending *pending `json:"pending,omitempty"`
}

type turn struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Reply string `json:"reply"`
}

// pending is what waits for the human: a question, or an answer to review
// (with its parsed output and route, kept for approval).
type pending struct {
	Kind   string `json:"kind"`
	Text   string `json:"text"`
	Output any    `json:"output,omitempty"`
	Route  string `json:"route,omitempty"`
	// Memory is the execution-memory turn of the reviewed run, written when
	// the human approves.
	Memory []llm.Message `json:"memory,omitempty"`
}

type hitl struct {
	rec      record
	approved *pending
}

// openHITL reads the step's record from the interrupt payload of a resumed
// job and applies the human's reply. Any other job starts clean. It does no
// I/O, so every attempt of a resumed run derives the same state.
func openHITL(job *worker.Job) (*hitl, error) {
	h := &hitl{}

	var in Interrupt

	interrupted, err := job.Interrupted(&in)
	if err != nil {
		return nil, worker.Permanent(fmt.Errorf("agentrun: interrupt payload: %w", err))
	}

	if !interrupted {
		return h, nil
	}

	h.rec = in.Stiggy

	p := h.rec.Pending
	if p == nil {
		return h, nil
	}

	reply := parseReply(job.Context.Resume)

	if p.Kind == kindReview && reply.approves() {
		h.approved = p

		return h, nil
	}

	h.rec.Turns = append(h.rec.Turns, turn{Kind: p.Kind, Text: p.Text, Reply: reply.text()})
	h.rec.Pending = nil

	return h, nil
}

// interrupt pauses the step on p; the payload carries the whole record.
func (h *hitl) interrupt(p *pending, agentName, node string) error {
	rec := h.rec
	rec.Pending = p

	in := Interrupt{Kind: p.Kind, Agent: agentName, Node: node, Stiggy: rec}
	if p.Kind == kindQuestion {
		in.Question = p.Text
	} else {
		in.Answer = p.Output
	}

	return worker.Interrupt(in)
}

// finish completes the step with output, applying its writes and extra
// writes (execution memory). usage is what this run spent; earlier runs of
// the step reported their own.
func (h *hitl) finish(s spec.Step, output any, next string, usage map[string]float64, extra map[string]any,
) (*worker.Result, error) {
	writes, err := stepio.Writes(s, output)
	if err != nil {
		return nil, err
	}

	if len(extra) > 0 {
		if writes == nil {
			writes = make(map[string]any, len(extra))
		}

		maps.Copy(writes, extra)
	}

	return &worker.Result{Output: output, Writes: writes, Next: next, Usage: usage}, nil
}

// transcript renders the exchanges so far for the agent's prompt.
func (h *hitl) transcript() string {
	if len(h.rec.Turns) == 0 {
		return ""
	}

	var b strings.Builder

	for _, t := range h.rec.Turns {
		switch t.Kind {
		case kindQuestion:
			fmt.Fprintf(&b, "\n\nYou asked a human:\n%s\nThe human answered:\n%s", t.Text, t.Reply)
		case kindReview:
			fmt.Fprintf(&b, "\n\nYou answered:\n%s\nA human reviewed your answer and asked for changes:\n%s",
				t.Text, t.Reply)
		}
	}

	b.WriteString("\n\nContinue the task with this in mind.")

	return b.String()
}

// reply is a human's resume value.
type reply struct {
	Answer   string `json:"answer"`
	Feedback string `json:"feedback"`
	Approve  bool   `json:"approve"`
}

// parseReply accepts a JSON string or an object with answer, feedback or
// approve; anything else is used as raw text.
func parseReply(raw json.RawMessage) reply {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return reply{Answer: s}
	}

	var r reply
	if json.Unmarshal(raw, &r) == nil {
		return r
	}

	return reply{Answer: string(raw)}
}

func (r reply) text() string {
	if r.Feedback != "" {
		return r.Feedback
	}

	return r.Answer
}

func (r reply) approves() bool { return r.Approve && r.text() == "" }
