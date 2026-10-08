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
	"slices"
	"strings"
	"sync"
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

// TestPatternPromptSeesReports checks that the agent in charge of a
// supervisor or swarm with a custom prompt sees the output of the agent that
// handed back to it, not the prompt alone.
func TestPatternPromptSeesReports(t *testing.T) {
	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		role := roleOf(r.System)

		switch {
		case len(r.ToolResults) > 0:
			return stiggytest.Response{Text: role + " routed"}
		case role == "Writer" && slices.Contains(r.Tools, "route_to_researcher"):
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_researcher", Args: map[string]any{}}}}
		case role == "Writer":
			return stiggytest.Response{Text: "Writer answered"}
		case strings.Contains(r.Prompt, "Writer routed") || strings.Contains(r.Prompt, "Writer answered"):
			return stiggytest.Response{Text: role + " done"}
		default:
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "route_to_writer", Args: map[string]any{}}}}
		}
	})

	h := startYAML(t, crewAgents+`
patterns:
  team: {type: supervisor, supervisor: boss, workers: [researcher, writer], prompt: "Handle {{.input.q}}", max_steps: 6}
  swarm: {type: swarm, agents: [researcher, writer], prompt: "Handle {{.input.q}}", max_steps: 6}
`, map[string]llm.LLM{"m": model})

	st := h.Run(t, "team", map[string]any{"q": 1})
	completed(t, st)

	if got := textOutput(t, st); got != "Manager done" {
		t.Fatalf("team: output %q, visits %v", got, st.Visits)
	}

	st = h.Run(t, "swarm", map[string]any{"q": 1})
	completed(t, st)

	if got := textOutput(t, st); got != "Researcher done" {
		t.Fatalf("swarm: output %q, visits %v", got, st.Visits)
	}
}

// TestPatternStepsSeeCustomPrompt checks every step that refers to the task
// sees a pattern's custom prompt, rendered, rather than only the input: the
// evaluator judges against its rules, plan_execute's executor and
// synthesizer work on it, and a debate's debaters and judge argue it.
func TestPatternStepsSeeCustomPrompt(t *testing.T) {
	var (
		mu   sync.Mutex
		seen = map[string][]string{} // role → prompts
	)

	model := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		role := roleOf(r.System)

		mu.Lock()

		seen[role] = append(seen[role], r.Prompt)
		mu.Unlock()

		switch role {
		case "Editor":
			return stiggytest.Response{Text: `{"pass": true, "feedback": ""}`}
		case "Manager":
			return stiggytest.Response{Text: `{"steps": ["one"]}`}
		default:
			return stiggytest.Response{Text: role + " done"}
		}
	})

	h := startYAML(t, crewAgents+`
patterns:
  polish: {type: evaluator_optimizer, generator: writer, evaluator: editor, prompt: "Write about {{.input.topic}}. Rule: RULE-42."}
  project: {type: plan_execute, planner: boss, executor: researcher, synthesizer: writer, prompt: "Ship {{.input.topic}}. Rule: RULE-43."}
  argue: {type: debate, debaters: [researcher, boss], judge: editor, rounds: 1, prompt: "Debate {{.input.topic}}. Rule: RULE-44."}
`, map[string]llm.LLM{"m": model})

	completed(t, h.Run(t, "polish", map[string]any{"topic": "nats"}))
	completed(t, h.Run(t, "project", map[string]any{"topic": "nats"}))
	completed(t, h.Run(t, "argue", map[string]any{"topic": "nats"}))

	mu.Lock()
	defer mu.Unlock()

	for role, want := range map[string][]string{
		"Editor":     {"Write about nats. Rule: RULE-42.", "Debate nats. Rule: RULE-44."},
		"Researcher": {"Ship nats. Rule: RULE-43.", "Debate nats. Rule: RULE-44."},
		"Writer":     {"Write about nats. Rule: RULE-42.", "Ship nats. Rule: RULE-43."},
		"Manager":    {"Debate nats. Rule: RULE-44."},
	} {
		all := strings.Join(seen[role], "\n---\n")
		for _, w := range want {
			if !strings.Contains(all, w) {
				t.Errorf("%s never saw %q; prompts:\n%s", role, w, all)
			}
		}
	}
}

// emptyOnce returns a model whose first call by role answers empty, as real
// models now and then do; every other call answers with the role's name, and
// the Editor passes what it judges.
func emptyOnce(role string) llm.LLM {
	var calls atomic.Int64

	return stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		got := roleOf(r.System)

		switch {
		case got == role && calls.Add(1) == 1:
			return stiggytest.Response{}
		case got == "Editor":
			return stiggytest.Response{Text: `{"pass": true, "valid": true, "feedback": ""}`}
		default:
			return stiggytest.Response{Text: got + " done"}
		}
	})
}

// TestPatternAndCrewRetry checks a crew's or pattern's retry reaches the
// steps it generates: an empty answer, which is retryable, is asked again,
// and without a retry it fails the execution.
func TestPatternAndCrewRetry(t *testing.T) {
	t.Run("pattern retry", func(t *testing.T) {
		h := startYAML(t, crewAgents+`
patterns:
  polish: {type: evaluator_optimizer, generator: writer, evaluator: editor, retry: {max_attempts: 2, delay: 10ms}}
`, map[string]llm.LLM{"m": emptyOnce("Writer")})

		st := h.Run(t, "polish", map[string]any{"task": "x"})
		completed(t, st)

		if got := textOutput(t, st); got != "Writer done" {
			t.Fatalf("output %q", got)
		}
	})

	t.Run("no retry", func(t *testing.T) {
		h := startYAML(t, crewAgents+`
patterns:
  polish: {type: evaluator_optimizer, generator: writer, evaluator: editor}
`, map[string]llm.LLM{"m": emptyOnce("Writer")})

		st := h.Run(t, "polish", map[string]any{"task": "x"})
		if st.Status != packtrail.StatusFailed || !strings.Contains(st.Error, "empty") {
			t.Fatalf("status %s: %s", st.Status, st.Error)
		}
	})

	t.Run("crew retry reaches the guardrail", func(t *testing.T) {
		h := startYAML(t, crewAgents+`
crews:
  launch:
    retry: {max_attempts: 2, delay: 10ms}
    tasks:
      - {id: draft, agent: writer, description: Write., guardrail: {agent: editor}}
`, map[string]llm.LLM{"m": emptyOnce("Editor")})

		completed(t, h.Run(t, "launch", map[string]any{}))
	})

	t.Run("task retry", func(t *testing.T) {
		h := startYAML(t, crewAgents+`
crews:
  launch:
    tasks:
      - {id: facts, agent: researcher, description: Research.}
      - {id: draft, agent: writer, description: Write., retry: {max_attempts: 2, delay: 10ms}}
`, map[string]llm.LLM{"m": emptyOnce("Writer")})

		st := h.Run(t, "launch", map[string]any{})
		completed(t, st)

		if got := textOutput(t, st); got != "Writer done" {
			t.Fatalf("output %q", got)
		}
	})
}
