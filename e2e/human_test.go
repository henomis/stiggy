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

package e2e_test

import (
	"strings"
	"testing"

	"github.com/henomis/packtrail"
)

const conciergeFleet = `
namespace: e2e-concierge
agents:
  concierge:
    model: main
    role: Restaurant concierge
    goal: >-
      Book dinner tables. You never know the party size or dietary needs in
      advance: before booking, you must ask the human with the ask_human
      tool. Then confirm the booking in at most three sentences.
flows:
  book:
    nodes:
      - id: book
        type: task
        agent: concierge
        prompt: "Book a table for tonight at {{.input.restaurant}}."
        routes: [human]
        human_input: true
`

// maxPauses bounds the conversation with the model, so a model that never
// complies fails the test instead of looping.
const maxPauses = 6

// TestConciergeHumanInTheLoop covers both kinds of human in the loop with a
// real model: the agent asks a question only a human can answer, its answer
// is reviewed and revised on feedback until approved. At the first review
// the execution is forked and the fork approved at once: it ends with the
// unrevised answer while the original goes on, untouched (time travel).
func TestConciergeHumanInTheLoop(t *testing.T) {
	h := start(t, conciergeFleet)
	ctx := testCtx(t)

	id, err := h.Client.Start(ctx, "book", map[string]any{"restaurant": "Trattoria Gialla"})
	if err != nil {
		t.Fatal(err)
	}

	var (
		asked, reviews int
		forked         bool
		st             = &packtrail.State{}
	)

	for range maxPauses {
		var in *interrupt

		st, in = waitPaused(t, h.Client, id, st.LastSeq)
		if in == nil {
			break
		}

		var reply any

		switch in.Kind {
		case "question":
			asked++

			t.Logf("concierge asked: %s", in.Question)

			reply = "We are 7 people, two of them vegetarian, and we would like to eat at 8 pm."
		case "review":
			reviews++
			answer := text(in.Answer)
			t.Logf("review %d: %s", reviews, answer)

			if !forked {
				forked = true

				forkApproved(t, h.Client, st, answer)
			}

			if strings.Contains(answer, "BK-7731") {
				reply = map[string]any{"approve": true}
			} else {
				reply = map[string]any{"feedback": "End the confirmation with the booking reference BK-7731."}
			}
		default:
			t.Fatalf("unexpected interrupt %+v", in)
		}

		if err = h.Client.Resume(ctx, id, in.Node, reply); err != nil {
			t.Fatal(err)
		}
	}

	if !st.Status.Terminal() {
		t.Fatalf("still waiting for a human after %d pauses (%d reviews)", maxPauses, reviews)
	}

	completed(t, st)

	if asked == 0 {
		t.Fatal("the concierge booked without asking the human")
	}

	if reviews < 2 {
		t.Fatalf("%d reviews: want the feedback round", reviews)
	}

	mustContain(t, "booking", text(st.Output), "BK-7731", "7")

	if st.Counters["agent_steps"] < 3 {
		t.Fatalf("counters %v: want the ask, the answer and the revision charged", st.Counters)
	}
}

// forkApproved forks the paused execution where it is, approves the fork's
// review and checks it ends with the answer under review.
func forkApproved(t *testing.T, c *packtrail.Client, st *packtrail.State, answer string) {
	t.Helper()

	ctx := testCtx(t)

	fork, err := c.Fork(ctx, st.ExecID, st.LastSeq)
	if err != nil {
		t.Fatal(err)
	}

	_, in := waitPaused(t, c, fork, 0)
	if in == nil || in.Kind != "review" || text(in.Answer) != answer {
		t.Fatalf("fork paused for %+v, want the same review", in)
	}

	if err = c.Resume(ctx, fork, in.Node, map[string]any{"approve": true}); err != nil {
		t.Fatal(err)
	}

	fst, err := c.Wait(ctx, fork)
	if err != nil {
		t.Fatal(err)
	}

	completed(t, fst)

	if text(fst.Output) != answer {
		t.Fatalf("fork output %q, want the approved answer %q", text(fst.Output), answer)
	}
}
