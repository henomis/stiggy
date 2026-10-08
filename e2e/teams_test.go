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
	"github.com/henomis/packtrail/packtrailtest"
	natsagent "github.com/henomis/phero/v2/nats"
)

const legalFleet = `
namespace: e2e-legal
agents:
  counsel:
    model: main
    role: Legal counsel
    goal: Review product announcements for legal risks.
    backstory: >-
      Company policy LP-41: every announcement you approve gets the clearance
      tag ${E2E_CLEARANCE}. Whenever you approve a text, give that exact tag.
expose:
  owner: e2e-legal-team
  agents: [counsel]
`

const marketingFleet = `
namespace: e2e-marketing
remotes:
  legal: {owner: e2e-legal-team, name: counsel, description: Legal counsel. Reviews announcements and gives clearance tags.}
agents:
  writer: {model: main, role: Writer, goal: Write short product announcements.}
  publicist:
    model: main
    role: Publicist
    goal: >-
      Publish announcements only with a clearance tag from legal. You do not
      know the tag: first call the ask_legal tool with your draft to get it,
      then write the announcement ending with the exact tag legal gave you.
    delegates: [legal]
flows:
  announce:
    output: "{draft: results.draft.text, review: results.review}"
    nodes:
      - id: draft
        type: task
        agent: writer
        prompt: "Write a two-sentence announcement of {{.input.product}}."
        next: review
      - id: review
        type: task
        remote: legal
        prompt: "Review this announcement. If it is acceptable, approve it and give its clearance tag.\n\n{{.results.draft.text}}"
        output_schema:
          type: object
          properties: {approved: {type: boolean}, tag: {type: string}, notes: {type: string}}
          required: [approved, tag]
        retry: {max_attempts: 3, delay: 2s}
  release:
    nodes:
      - {id: release, type: task, agent: publicist, prompt: "Announce {{.input.product}} in two sentences."}
`

// TestCrossTeamRemote runs two teams on one NATS server, as two
// deployments would: legal serves its counsel on the NATS Agent Protocol,
// and marketing uses it as a remote step with a structured answer, and as a
// delegate its own agent consults through a tool. The clearance tag only
// legal's agent knows proves the answers crossed teams.
func TestCrossTeamRemote(t *testing.T) {
	live(t)

	tag := secret("CLR")
	t.Setenv("E2E_CLEARANCE", tag)

	srv := packtrailtest.Start(t)

	serve(t, srv.Connect(t), options(t, legalFleet)...)
	marketing := serve(t, srv.Connect(t), options(t, marketingFleet)...)

	t.Run("remote step", func(t *testing.T) {
		st := run(t, marketing.Client(), "announce", map[string]any{"product": "stiggy, durable agent fleets on NATS"})

		var out struct {
			Draft  string
			Review struct {
				Approved bool
				Tag      string
			}
		}
		if err := st.DecodeOutput(&out); err != nil {
			t.Fatal(err)
		}

		if strings.TrimSpace(out.Draft) == "" || !out.Review.Approved {
			t.Fatalf("output %s: want a draft legal approved", st.Output)
		}

		mustContain(t, "clearance tag", out.Review.Tag, tag)
	})

	t.Run("delegate", func(t *testing.T) {
		st := run(t, marketing.Client(), "release", map[string]any{"product": "stiggy 1.0"})

		mustContain(t, "announcement", text(st.Output), tag)

		if st.Counters["agent_steps"] < 1 {
			t.Fatalf("counters %v", st.Counters)
		}
	})
}

const crewFleet = `
namespace: e2e-crew
agents:
  researcher: {model: main, role: Researcher, goal: Find accurate facts. Answer with a bullet list of three facts.}
  writer: {model: main, role: Writer, goal: "Write short, clear paragraphs."}
  editor:
    model: main
    role: Editor
    goal: >-
      Accept a draft when it is one paragraph that uses the research. Reject
      it only for a clear failure, with one line of feedback.
crews:
  brief:
    tasks:
      - id: history
        agent: researcher
        description: "Research the history of {{.input.prompt}}."
        expected_output: Three facts as a bullet list.
        async: true
      - id: uses
        agent: researcher
        description: "Research what {{.input.prompt}} is used for today."
        expected_output: Three facts as a bullet list.
        async: true
      - id: write
        agent: writer
        description: Write a one-paragraph brief from the research.
        context: [history, uses]
        expected_output: One paragraph under 120 words.
        guardrail: {agent: editor, max_retries: 2}
    budget: {agent_steps: 14}
    retry: {max_attempts: 3, delay: 2s}
expose:
  owner: e2e-newsroom
  flows: [brief]
`

// TestCrewOverProtocol serves a crew (parallel research tasks, then a
// guarded writing task) on the NATS Agent Protocol and calls it the way any
// client would, from its own connection: the prompt starts an ordinary
// execution, whose output is the answer.
func TestCrewOverProtocol(t *testing.T) {
	h := start(t, crewFleet)
	ctx := testCtx(t)

	handles, err := natsagent.NewClient(h.Server.Connect(t)).Discover(ctx, natsagent.FilterByOwner("e2e-newsroom"))
	if err != nil {
		t.Fatal(err)
	}

	if len(handles) != 1 || handles[0].Name != "brief" {
		t.Fatalf("discovered %v, want the brief crew", handles)
	}

	stream, err := handles[0].Prompt(ctx, "the Go programming language")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()

	answer, err := stream.Text(ctx)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("crew answer: %s", answer)

	if strings.TrimSpace(answer) == "" {
		t.Fatal("empty answer")
	}

	sums, err := h.Client.List(ctx, packtrail.ListFilter{Flow: "brief"})
	if err != nil || len(sums) != 1 {
		t.Fatalf("executions of brief = %v, %v", sums, err)
	}

	st, err := h.Client.Get(ctx, sums[0].ExecID)
	if err != nil {
		t.Fatal(err)
	}

	completed(t, st)

	if st.Visits["history"] != 1 || st.Visits["uses"] != 1 || st.Visits["write"] < 1 || st.Visits["write"] > 3 {
		t.Fatalf("visits %v: want both research tasks once and one to three drafts", st.Visits)
	}
}

const patternsFleet = `
namespace: e2e-patterns
agents:
  copywriter: {model: main, role: Copywriter, goal: Write punchy product copy.}
  reviewer:
    model: main
    role: Copy reviewer
    goal: >-
      Check copy against the brief's rules strictly. Pass it only if it
      follows every rule; otherwise say exactly which rule it breaks.
  optimist: {model: main, role: Optimist, goal: Argue for the proposal in at most three sentences.}
  skeptic: {model: main, role: Skeptic, goal: Argue against the proposal in at most three sentences.}
  judge: {model: main, role: Judge, goal: Weigh both sides and give a verdict in at most four sentences.}
patterns:
  tagline:
    type: evaluator_optimizer
    generator: copywriter
    evaluator: reviewer
    max_rounds: 3
    retry: {max_attempts: 3, delay: 2s}
    prompt: >-
      Write a tagline for stiggy, a tool that runs durable AI agent fleets on
      NATS. Rules: at most ten words, must contain the word "durable", no
      exclamation marks. Answer with the tagline only.
  argue:
    type: debate
    debaters: [optimist, skeptic]
    judge: judge
    rounds: 2
    retry: {max_attempts: 3, delay: 2s}
    prompt: "Proposal: {{.input.proposal}}"
`

// TestEvaluatorOptimizerAndDebate runs two patterns whose control flow a
// real model drives: a generator revised until the evaluator passes it, and
// a two-round debate the judge settles.
func TestEvaluatorOptimizerAndDebate(t *testing.T) {
	h := start(t, patternsFleet)

	t.Run("evaluator_optimizer", func(t *testing.T) {
		st := run(t, h.Client, "tagline", map[string]any{})

		if n := st.Visits["generate"]; n < 1 || n > 3 {
			t.Fatalf("visits %v: want one to three generations", st.Visits)
		}

		tagline := text(st.Output)
		mustContain(t, "tagline", tagline, "durable")

		if strings.Contains(tagline, "!") {
			t.Logf("the evaluator passed a tagline that breaks a rule: %q", tagline)
		}
	})

	t.Run("debate", func(t *testing.T) {
		st := run(t, h.Client, "argue", map[string]any{"proposal": "Rewrite our monolith as microservices this quarter."})

		if st.Visits["round"] != 2 || st.Visits["judge"] != 1 || st.Visits["optimist"] != 2 || st.Visits["skeptic"] != 2 {
			t.Fatalf("visits %v: want two rounds of both debaters, then the judge", st.Visits)
		}

		if strings.TrimSpace(text(st.Output)) == "" {
			t.Fatal("the judge gave no verdict")
		}
	})
}
