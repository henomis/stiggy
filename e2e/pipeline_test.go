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
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy"
)

var errEmptyBrief = errors.New("the brief channel is empty")

const researchFleet = `
namespace: e2e-research
agents:
  planner: {model: main, role: Research planner, goal: Split a subject into focused research topics.}
  researcher: {model: main, role: Researcher, goal: "Write one precise, factual sentence about a topic."}
  editor: {model: main, role: Editor, goal: Turn research notes into a short brief.}
flows:
  research:
    channels: {notes: {reducer: append}, brief: {}}
    output: "{brief: channels.brief, notes: channels.notes, url: results.publish.url}"
    nodes:
      - id: plan
        type: task
        agent: planner
        prompt: "List exactly three research topics about {{.input.subject}}."
        output_schema:
          type: object
          properties: {topics: {type: array, items: {type: string}, minItems: 2, maxItems: 4}}
          required: [topics]
        retry: {max_attempts: 3, delay: 2s}
        next: dig
      - id: dig
        type: map
        over: results.plan.topics
        max_parallel: 2
        agent: researcher
        prompt: "Write one factual sentence about {{.item}}, for a brief on {{.input.subject}}."
        writes: {notes: output.text}
        next: brief
      - id: brief
        type: task
        agent: editor
        prompt: |-
          Write a brief of at most four sentences on {{.input.subject}} from these notes:
          {{json .channels.notes}}
          End the brief with this exact line: Ref: {{.input.ref}}
        writes: {brief: output.text}
        next: publish
      - id: publish
        type: task
        activity: publish
`

// TestResearchPipeline is map-reduce with a real model: a planner answers
// structured topics, a researcher runs once per topic in parallel, an
// append channel gathers the notes, an editor writes a brief from them, and
// a Go activity publishes what the channels hold.
func TestResearchPipeline(t *testing.T) {
	var published atomic.Value

	h := start(t, researchFleet, stiggy.WithActivity("publish",
		func(_ context.Context, j *worker.Job) (*worker.Result, error) {
			var brief string
			if err := j.Channel("brief", &brief); err != nil {
				return nil, worker.Permanent(err)
			}

			if strings.TrimSpace(brief) == "" {
				return nil, worker.Permanent(errEmptyBrief)
			}

			published.Store(brief)

			return &worker.Result{Output: map[string]any{"url": "https://news.example.com/briefs/1"}}, nil
		}))

	st := run(t, h.Client, "research", map[string]any{"subject": "the NATS messaging system", "ref": "RX-2207"})

	var plan struct{ Topics []string }
	if err := st.Result("plan", &plan); err != nil {
		t.Fatal(err)
	}

	var out struct {
		Brief string
		Notes []string
		URL   string
	}
	if err := st.DecodeOutput(&out); err != nil {
		t.Fatal(err)
	}

	if len(plan.Topics) < 2 || len(out.Notes) != len(plan.Topics) {
		t.Fatalf("topics %q, notes %q: want one note per topic", plan.Topics, out.Notes)
	}

	for i, n := range out.Notes {
		if strings.TrimSpace(n) == "" {
			t.Fatalf("note %d is empty", i)
		}
	}

	mustContain(t, "brief", out.Brief, "Ref: RX-2207")

	if published.Load() != out.Brief || out.URL == "" {
		t.Fatalf("the activity published %q, output %+v", published.Load(), out)
	}

	if least := float64(len(plan.Topics) + 2); st.Counters["agent_steps"] < least || st.Counters["tokens_out"] == 0 {
		t.Fatalf("counters %v, want at least %v agent steps and some tokens", st.Counters, least)
	}
}

const distributedFleet = `
namespace: e2e-distributed
agents:
  lead: {model: main, role: Project lead, goal: Plan work as two to four short independent steps.}
  engineer: {model: main, role: Engineer, goal: Carry out one step and report the result in at most two sentences.}
  writer: {model: main, role: Technical writer, goal: Combine step results into a short final answer.}
patterns:
  project:
    type: plan_execute
    planner: lead
    executor: engineer
    synthesizer: writer
    max_parallel: 2
    prompt: "Plan how to {{.input.goal}}. Use at most four steps."
    budget: {agent_steps: 16}
    retry: {max_attempts: 3, delay: 2s}
`

// TestDistributedPlanExecute runs plan_execute across three processes on
// one NATS server, as a deployment would: an engine without workers, one
// process running the planner and the synthesizer, and another running the
// executor. Together they complete the flow.
func TestDistributedPlanExecute(t *testing.T) {
	live(t)

	srv := packtrailtest.Start(t)

	engine := serve(t, srv.Connect(t), options(t, distributedFleet, stiggy.WithRole(stiggy.RoleEngine))...)
	coordinators := serve(t, srv.Connect(t), options(t, distributedFleet,
		stiggy.WithRole(stiggy.RoleWorkers), stiggy.WithOnly("lead", "writer"))...)
	executors := serve(t, srv.Connect(t), options(t, distributedFleet,
		stiggy.WithRole(stiggy.RoleWorkers), stiggy.WithOnly("engineer"))...)

	if n, m := len(coordinators.Plan().Workers), len(executors.Plan().Workers); n != 2 || m != 1 {
		t.Fatalf("workers per process = %d and %d, want 2 and 1", n, m)
	}

	st := run(t, engine.Client(), "project",
		map[string]any{"goal": "add a health-check endpoint to a small Go web service"})

	var plan struct{ Steps []string }
	if err := st.Result("plan", &plan); err != nil {
		t.Fatal(err)
	}

	var results []string
	if err := st.Channel("results", &results); err != nil {
		t.Fatal(err)
	}

	if len(plan.Steps) == 0 || len(results) != len(plan.Steps) {
		t.Fatalf("steps %q, results %d: want one result per step", plan.Steps, len(results))
	}

	if st.Visits["synthesize"] != 1 || strings.TrimSpace(result(t, st, "synthesize")) == "" {
		t.Fatalf("visits %v: want one non-empty synthesis", st.Visits)
	}
}
