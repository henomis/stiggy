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
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/stiggytest"
)

// roleOf extracts the "Role: X" line the agents' system prompts carry.
func roleOf(system string) string {
	for line := range strings.SplitSeq(system, "\n") {
		if r, ok := strings.CutPrefix(line, "Role: "); ok {
			return r
		}
	}

	return ""
}

const crewAgents = `
models: {m: {provider: x}}
agents:
  researcher: {model: m, role: Researcher}
  writer: {model: m, role: Writer}
  editor: {model: m, role: Editor}
  boss: {model: m, role: Manager}
`

func completed(t *testing.T, st *packtrail.State) {
	t.Helper()

	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s (%s): %s", st.Status, st.Reason, st.Error)
	}
}

func textOutput(t *testing.T, st *packtrail.State) string {
	t.Helper()

	var out struct{ Text string }
	if err := st.DecodeOutput(&out); err != nil {
		t.Fatalf("output %s: %v", st.Output, err)
	}

	return out.Text
}

// TestSequentialCrew runs async tasks in parallel, then a guarded task the
// checker rejects once and accepts on the retry.
func TestSequentialCrew(t *testing.T) {
	var checks atomic.Int64

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch roleOf(r.System) {
		case "Researcher":
			return stiggytest.Response{Text: "facts: " + r.Prompt[:min(len(r.Prompt), 30)]}
		case "Writer":
			if strings.Contains(r.Prompt, "too long") {
				return stiggytest.Response{Text: "short post"}
			}

			return stiggytest.Response{Text: "a very long post"}
		case "Editor":
			if checks.Add(1) == 1 {
				return stiggytest.Response{Text: `{"valid": false, "feedback": "too long"}`}
			}

			return stiggytest.Response{Text: `{"valid": true, "feedback": ""}`}
		}

		return stiggytest.Response{Text: "?"}
	})

	h := startYAML(t, crewAgents+`
crews:
  launch:
    tasks:
      - {id: market, agent: researcher, description: "Market for {{.input.product}}", async: true}
      - {id: users, agent: researcher, description: "Users of {{.input.product}}", async: true}
      - {id: draft, agent: writer, description: Write the post., guardrail: {agent: editor, max_retries: 2}}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "launch", map[string]any{"product": "stiggy"})
	completed(t, st)

	if got := textOutput(t, st); got != "short post" {
		t.Fatalf("output = %q, want the revised draft", got)
	}

	if st.Visits["draft"] != 2 || st.Visits["market"] != 1 || st.Visits["users"] != 1 {
		t.Fatalf("visits = %v", st.Visits)
	}
}

// TestHierarchicalCrew has the manager assign both tasks, then answer.
func TestHierarchicalCrew(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if roleOf(r.System) != "Manager" {
			return stiggytest.Response{Text: roleOf(r.System) + " done"}
		}

		switch {
		case len(r.ToolResults) > 0:
			return stiggytest.Response{Text: "assigned"}
		case strings.Contains(r.Prompt, "(not done yet)\n- write"):
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_research", Args: map[string]any{}}}}
		case strings.Contains(r.Prompt, "(not done yet)"):
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_write", Args: map[string]any{}}}}
		default:
			return stiggytest.Response{Text: "final answer"}
		}
	})

	h := startYAML(t, crewAgents+`
crews:
  managed:
    process: hierarchical
    manager: boss
    tasks:
      - {id: research, agent: researcher, description: Research.}
      - {id: write, agent: writer, description: Write.}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "managed", map[string]any{"topic": "nats"})
	completed(t, st)

	if got := textOutput(t, st); got != "final answer" || st.Visits["manager"] != 3 {
		t.Fatalf("output %q, visits %v", got, st.Visits)
	}
}

// TestEvaluatorOptimizer regenerates once with the evaluator's feedback.
func TestEvaluatorOptimizer(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if roleOf(r.System) == "Editor" {
			if strings.Contains(r.Prompt, "v2") {
				return stiggytest.Response{Text: `{"pass": true, "feedback": ""}`}
			}

			return stiggytest.Response{Text: `{"pass": false, "feedback": "add detail"}`}
		}

		if strings.Contains(r.Prompt, "add detail") {
			return stiggytest.Response{Text: "v2"}
		}

		return stiggytest.Response{Text: "v1"}
	})

	h := startYAML(t, crewAgents+`
patterns:
  polish: {type: evaluator_optimizer, generator: writer, evaluator: editor, max_rounds: 3}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "polish", map[string]any{"task": "explain nats"})
	completed(t, st)

	if got := textOutput(t, st); got != "v2" || st.Visits["generate"] != 2 {
		t.Fatalf("output %q, visits %v", got, st.Visits)
	}
}

// TestDebate runs two rounds of two debaters, then the judge sees all four
// arguments.
func TestDebate(t *testing.T) {
	var judgeSaw atomic.Int64

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch roleOf(r.System) {
		case "Editor":
			judgeSaw.Store(int64(strings.Count(r.Prompt, "argues")))

			return stiggytest.Response{Text: "verdict"}
		default:
			return stiggytest.Response{Text: roleOf(r.System) + " argues"}
		}
	})

	h := startYAML(t, crewAgents+`
patterns:
  argue: {type: debate, debaters: [researcher, writer], judge: editor, rounds: 2}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "argue", map[string]any{"topic": "tabs vs spaces"})
	completed(t, st)

	if got := textOutput(t, st); got != "verdict" || judgeSaw.Load() != 4 {
		t.Fatalf("output %q, judge saw %d arguments", got, judgeSaw.Load())
	}
}

// TestPlanExecute plans three steps, runs each, and synthesizes.
func TestPlanExecute(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch roleOf(r.System) {
		case "Manager":
			return stiggytest.Response{Text: `{"steps": ["a", "b", "c"]}`}
		case "Researcher":
			return stiggytest.Response{Text: "did " + strings.SplitN(strings.TrimPrefix(r.Prompt, "Carry out this step:\n"), "\n", 2)[0]}
		default:
			var results []string

			if i := strings.Index(r.Prompt, "Step results:\n"); i >= 0 {
				_ = json.Unmarshal([]byte(r.Prompt[i+len("Step results:\n"):]), &results)
			}

			return stiggytest.Response{Text: strings.Join(results, ",")}
		}
	})

	h := startYAML(t, crewAgents+`
patterns:
  project: {type: plan_execute, planner: boss, executor: researcher, synthesizer: writer}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "project", map[string]any{"goal": "ship"})
	completed(t, st)

	got := textOutput(t, st)
	for _, want := range []string{"did a", "did b", "did c"} {
		if !strings.Contains(got, want) {
			t.Fatalf("synthesis %q lacks %q", got, want)
		}
	}
}

// TestSupervisorAndSwarm checks the routing patterns end when the agent in
// charge answers without routing.
func TestSupervisorAndSwarm(t *testing.T) {
	var routed atomic.Bool

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		role := roleOf(r.System)

		if (role == "Manager" || role == "Researcher") && len(r.ToolResults) == 0 && routed.CompareAndSwap(false, true) {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_writer", Args: map[string]any{}}}}
		}

		return stiggytest.Response{Text: role + " answered"}
	})

	h := startYAML(t, crewAgents+`
patterns:
  team: {type: supervisor, supervisor: boss, workers: [researcher, writer]}
  swarm: {type: swarm, agents: [researcher, writer]}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "team", map[string]any{"q": 1})
	completed(t, st)

	if got := textOutput(t, st); got != "Manager answered" || st.Visits["writer"] != 1 || st.Visits["boss"] != 2 {
		t.Fatalf("team: output %q, visits %v", got, st.Visits)
	}

	routed.Store(false)

	st = h.Run(t, "swarm", map[string]any{"q": 1})
	completed(t, st)

	if got := textOutput(t, st); got != "Writer answered" {
		t.Fatalf("swarm: output %q, visits %v", got, st.Visits)
	}
}
