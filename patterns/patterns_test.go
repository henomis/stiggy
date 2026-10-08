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

package patterns_test

import (
	"maps"
	"slices"
	"testing"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/patterns"
	"github.com/henomis/stiggy/spec"
)

// retries returns the max_attempts of every node of f that has a retry.
func retries(f *flow.Flow) map[string]int {
	out := map[string]int{}

	for _, n := range f.Nodes {
		if n.Retry != nil {
			out[n.ID] = n.Retry.MaxAttempts
		}
	}

	return out
}

// TestRetryReachesEveryStep checks a crew's or pattern's retry lands on
// every agent step it generates (tasks and maps), never on choices,
// fan-outs or joins, and that a task's own retry overrides the crew's.
func TestRetryReachesEveryStep(t *testing.T) {
	r := &flow.Retry{MaxAttempts: 3}

	for _, tc := range []struct {
		name  string
		build func() (*flow.Flow, error)
		want  map[string]int
	}{
		{
			name: "evaluator_optimizer",
			build: func() (*flow.Flow, error) {
				return patterns.Expand("p", spec.Pattern{
					Type: spec.PatternEvaluatorOptimizer, Generator: "g", Evaluator: "e", Retry: r,
				})
			},
			want: map[string]int{"generate": 3, "evaluate": 3},
		},
		{
			name: "plan_execute",
			build: func() (*flow.Flow, error) {
				return patterns.Expand("p", spec.Pattern{
					Type: spec.PatternPlanExecute, Planner: "p", Executor: "x", Synthesizer: "s", Retry: r,
				})
			},
			want: map[string]int{"plan": 3, "execute": 3, "synthesize": 3},
		},
		{
			name: "debate",
			build: func() (*flow.Flow, error) {
				return patterns.Expand("p", spec.Pattern{
					Type: spec.PatternDebate, Debaters: []string{"a", "b"}, Judge: "j", Retry: r,
				})
			},
			want: map[string]int{"a": 3, "b": 3, "judge": 3},
		},
		{
			name: "supervisor",
			build: func() (*flow.Flow, error) {
				return patterns.Expand("p", spec.Pattern{
					Type: spec.PatternSupervisor, Supervisor: "boss", Workers: []string{"w"}, Retry: r,
				})
			},
			want: map[string]int{"boss": 3, "w": 3},
		},
		{
			name: "sequential crew",
			build: func() (*flow.Flow, error) {
				return patterns.Crew("c", spec.Crew{Retry: r, Tasks: []spec.Task{
					{ID: "a1", Agent: "a", Description: "d", Async: true},
					{ID: "a2", Agent: "a", Description: "d", Async: true},
					{ID: "w", Agent: "a", Description: "d", Guardrail: &spec.Guardrail{Agent: "e"},
						Retry: &flow.Retry{MaxAttempts: 5}},
				}})
			},
			// a1_parallel, a1_join and w_check run no step.
			want: map[string]int{"a1": 3, "a2": 3, "w": 5, "w_guard": 3},
		},
		{
			name: "hierarchical crew",
			build: func() (*flow.Flow, error) {
				return patterns.Crew("c", spec.Crew{Process: spec.ProcessHierarchical, Manager: "m", Retry: r,
					Tasks: []spec.Task{{ID: "t", Agent: "a", Description: "d"}}})
			},
			want: map[string]int{"manager": 3, "t": 3},
		},
		{
			name: "task retry without a crew retry",
			build: func() (*flow.Flow, error) {
				return patterns.Crew("c", spec.Crew{Tasks: []spec.Task{
					{ID: "a", Agent: "a", Description: "d"},
					{ID: "b", Agent: "a", Description: "d", Retry: &flow.Retry{MaxAttempts: 2}},
				}})
			},
			want: map[string]int{"b": 2},
		},
		{
			name: "no retry",
			build: func() (*flow.Flow, error) {
				return patterns.Expand("p", spec.Pattern{Type: spec.PatternEvaluatorOptimizer, Generator: "g", Evaluator: "e"})
			},
			want: map[string]int{},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, err := tc.build()
			if err != nil {
				t.Fatal(err)
			}

			if got := retries(f); !maps.Equal(got, tc.want) {
				t.Fatalf("retries = %v, want %v (nodes %v)", got, tc.want, slices.Collect(maps.Keys(nodes(f))))
			}

			if err = f.Validate(); err != nil {
				t.Fatalf("the generated flow is invalid: %v", err)
			}
		})
	}
}

func nodes(f *flow.Flow) map[string]bool {
	out := map[string]bool{}
	for _, n := range f.Nodes {
		out[n.ID] = true
	}

	return out
}

// TestRetryNodesShareNothing checks every node gets its own copy of the
// policy.
func TestRetryNodesShareNothing(t *testing.T) {
	f, err := patterns.Expand("p", spec.Pattern{
		Type: spec.PatternEvaluatorOptimizer, Generator: "g", Evaluator: "e", Retry: &flow.Retry{MaxAttempts: 3},
	})
	if err != nil {
		t.Fatal(err)
	}

	var seen []*flow.Retry

	for _, n := range f.Nodes {
		if n.Retry != nil {
			if slices.Contains(seen, n.Retry) {
				t.Fatalf("node %s shares its retry", n.ID)
			}

			seen = append(seen, n.Retry)
		}
	}
}
