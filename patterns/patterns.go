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

// Package patterns expands multi-agent patterns into ordinary packtrail flows:
// crews (sequential and hierarchical processes, async tasks, guardrails)
// and the supervisor, swarm, evaluator-optimizer, debate and
// plan-execute. They are macros: the result is a plain flow of agent steps,
// choices, fan-outs and maps, so `stiggy compile` shows exactly what runs, and
// every packtrail feature (fork, rerun, budgets, retries) applies. A
// pattern that finishes from a choice routes to flow.End and names the result
// it returns in the flow's output: the task before that choice is a checker,
// whose verdict is not the answer.
//
// Agent references are not checked here; the compiler checks the expanded
// flow like any other.
package patterns

import (
	"fmt"
	"strings"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// Expand returns the flow of a pattern.
func Expand(name string, p spec.Pattern) (*flow.Flow, error) {
	switch p.Type {
	case spec.PatternSupervisor:
		return Supervisor(name, p)
	case spec.PatternSwarm:
		return Swarm(name, p)
	case spec.PatternEvaluatorOptimizer:
		return EvaluatorOptimizer(name, p)
	case spec.PatternDebate:
		return Debate(name, p)
	case spec.PatternPlanExecute:
		return PlanExecute(name, p)
	default:
		return nil, perr("type", "unknown pattern type %q (want %s, %s, %s, %s or %s)", p.Type,
			spec.PatternSupervisor, spec.PatternSwarm, spec.PatternEvaluatorOptimizer, spec.PatternDebate,
			spec.PatternPlanExecute)
	}
}

// perr is a problem at a path relative to the pattern or crew.
func perr(path, format string, args ...any) error {
	return &spec.Error{Path: path, Msg: fmt.Sprintf(format, args...)}
}

// builder accumulates the nodes of a flow.
type builder struct {
	f   *flow.Flow
	err error
}

func newBuilder(name string, maxSteps int, budget map[string]float64) *builder {
	return &builder{f: &flow.Flow{Name: name, MaxSteps: maxSteps, Budget: budget}}
}

// agent adds an agent step and returns it for further settings.
func (b *builder) agent(id string, s spec.Step) *flow.Node {
	n := flow.Node{ID: id, Type: flow.NodeTask}
	if err := spec.ApplyStep(&n, s); err != nil && b.err == nil {
		b.err = err
	}

	b.f.Nodes = append(b.f.Nodes, n)

	return &b.f.Nodes[len(b.f.Nodes)-1]
}

// add appends a raw node.
func (b *builder) add(n flow.Node) { b.f.Nodes = append(b.f.Nodes, n) }

func (b *builder) done(start string) (*flow.Flow, error) {
	if b.err != nil {
		return nil, b.err
	}

	b.f.Start = start

	return b.f, nil
}

// resultOf is the output expression returning a node's latest result.
func resultOf(node string) string { return fmt.Sprintf("results[%q]", node) }

// required checks the named fields are set.
func required(fields ...[2]string) error {
	for _, f := range fields {
		if strings.TrimSpace(f[1]) == "" {
			return perr(f[0], "%s is required", f[0])
		}
	}

	return nil
}

// distinct checks a list of agents has at least min distinct entries.
func distinct(field string, names []string, minimum int) error {
	seen := map[string]bool{}

	for _, n := range names {
		if seen[n] {
			return perr(field, "%q listed twice", n)
		}

		seen[n] = true
	}

	if len(names) < minimum {
		return perr(field, "at least %d agents are required", minimum)
	}

	return nil
}

// others returns names without self.
func others(names []string, self string) []string {
	out := make([]string, 0, len(names))

	for _, n := range names {
		if n != self {
			out = append(out, n)
		}
	}

	return out
}
