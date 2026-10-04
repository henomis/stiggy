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

package patterns

import (
	"fmt"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// Defaults of crews.
const (
	defaultGuardRetries = 2
	// stepsPerTask bounds a hierarchical crew: a manager turn and a task turn
	// per assignment, with room for reassignments.
	stepsPerTask = 4
	managerNode  = "manager"
	guardSuffix  = "_guard"
	checkSuffix  = "_check"
	parSuffix    = "_parallel"
	joinSuffix   = "_join"
)

// Crew expands a crew. Task ids become node ids; the output is the
// result of the last task (sequential) or the manager's final answer
// (hierarchical).
func Crew(name string, c spec.Crew) (*flow.Flow, error) {
	if len(c.Tasks) == 0 {
		return nil, perr("tasks", "at least one task is required")
	}

	ids := map[string]bool{}

	for _, t := range c.Tasks {
		path := "tasks." + t.ID

		if err := required([2]string{path + ".id", t.ID}, [2]string{path + ".agent", t.Agent},
			[2]string{path + ".description", t.Description}); err != nil {
			return nil, err
		}

		if ids[t.ID] {
			return nil, perr(path, "task id %q used twice", t.ID)
		}

		ids[t.ID] = true
	}

	switch c.Process {
	case "", spec.ProcessSequential:
		return sequential(name, c)
	case spec.ProcessHierarchical:
		return hierarchical(name, c)
	default:
		return nil, perr("process", "unknown process %q (want %s or %s)", c.Process,
			spec.ProcessSequential, spec.ProcessHierarchical)
	}
}

// taskStep is the agent step of a task.
func taskStep(t spec.Task, context []string) spec.Step {
	return spec.Step{
		Agent: t.Agent, Prompt: t.Description, ExpectedOutput: t.ExpectedOutput,
		Context: context, HumanInput: t.HumanInput,
	}
}

// sequential chains the tasks; a run of consecutive async tasks becomes a
// fan-out joined before the next task.
func sequential(name string, c spec.Crew) (*flow.Flow, error) {
	groups := groupAsync(c.Tasks)
	b := newBuilder(name, c.MaxSteps, c.Budget)

	// entry returns the first node of group i ("" past the last).
	entry := func(i int) string {
		switch {
		case i >= len(groups):
			return ""
		case len(groups[i]) > 1:
			return groups[i][0].ID + parSuffix
		default:
			return groups[i][0].ID
		}
	}

	prev := []string(nil)

	for gi, g := range groups {
		next := entry(gi + 1)

		if len(g) == 1 {
			t := g[0]

			if err := addTask(b, t, defaultContext(t, prev), next); err != nil {
				return nil, err
			}

			prev = []string{t.ID}

			continue
		}

		branches := make([]string, 0, len(g))

		for _, t := range g {
			if t.Guardrail != nil {
				return nil, perr("tasks."+t.ID+".guardrail", "an async task cannot have a guardrail")
			}

			n := b.agent(t.ID, taskStep(t, defaultContext(t, prev)))
			n.OutputSchema = t.OutputSchema
			branches = append(branches, t.ID)
		}

		par, join := g[0].ID+parSuffix, g[0].ID+joinSuffix
		b.add(flow.Node{ID: par, Type: flow.NodeFanout, Branches: branches, Next: join})
		b.add(flow.Node{ID: join, Type: flow.NodeJoin, Next: next})

		prev = branches
	}

	b.f.Output = sequentialOutput(groups[len(groups)-1])

	return b.done(entry(0))
}

// sequentialOutput is the crew's output: the last task's result, or the
// results of a final async group by task.
func sequentialOutput(last []spec.Task) string {
	if len(last) == 1 {
		return resultOf(last[0].ID)
	}

	out := "{"

	for i, t := range last {
		if i > 0 {
			out += ", "
		}

		out += fmt.Sprintf("%q: %s", t.ID, resultOf(t.ID))
	}

	return out + "}"
}

func defaultContext(t spec.Task, prev []string) []string {
	if len(t.Context) > 0 {
		return t.Context
	}

	return prev
}

// addTask adds a sequential task, with its guardrail when it has one: the
// checker judges the output ({valid, feedback}) and a rejected output is
// redone, with the feedback in context, up to the retry limit.
func addTask(b *builder, t spec.Task, context []string, next string) error {
	g := t.Guardrail
	if g == nil {
		n := b.agent(t.ID, taskStep(t, context))
		n.OutputSchema, n.Next = t.OutputSchema, next

		return nil
	}

	if g.Agent == "" {
		return perr("tasks."+t.ID+".guardrail.agent", "agent is required")
	}

	retries := g.MaxRetries
	if retries <= 0 {
		retries = defaultGuardRetries
	}

	guard, check := t.ID+guardSuffix, t.ID+checkSuffix

	// On a retry the task sees the checker's feedback too.
	n := b.agent(t.ID, taskStep(t, append(append([]string{}, context...), guard)))
	n.OutputSchema, n.Next = t.OutputSchema, guard

	gn := b.agent(guard, spec.Step{
		Agent:   g.Agent,
		Context: []string{t.ID},
		Prompt: "Check the output below for this task. Set valid to true when it fulfils the task; " +
			"otherwise explain what is wrong in feedback.\n\nTask: " + t.Description +
			expectedLine(t.ExpectedOutput),
	})
	gn.OutputSchema, gn.Next = verdictSchema("valid"), check

	after := next
	if after == "" {
		after = flow.End
	}

	b.add(flow.Node{ID: check, Type: flow.NodeChoice, Rules: []flow.Rule{
		{When: fmt.Sprintf("%s.valid == true", resultOf(guard)), To: after},
		{When: fmt.Sprintf("visits[%q] <= %d", t.ID, retries), To: t.ID},
		{Default: true, To: after},
	}})

	return nil
}

func expectedLine(s string) string {
	if s == "" {
		return ""
	}

	return "\nExpected output: " + s
}

// groupAsync splits tasks into runs: a lone task, or consecutive async
// tasks.
func groupAsync(tasks []spec.Task) [][]spec.Task {
	var groups [][]spec.Task

	for _, t := range tasks {
		last := len(groups) - 1
		if t.Async && last >= 0 && groups[last][0].Async {
			groups[last] = append(groups[last], t)

			continue
		}

		groups = append(groups, []spec.Task{t})
	}

	return groups
}

// hierarchical has the manager agent assign one task at a time by routing
// to it; each task reports back, and the manager's answer without routing is
// the crew's output.
func hierarchical(name string, c spec.Crew) (*flow.Flow, error) {
	if err := required([2]string{"manager", c.Manager}); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(c.Tasks))

	for _, t := range c.Tasks {
		if t.ID == managerNode {
			return nil, perr("tasks."+t.ID, "task id %q is reserved for the manager", managerNode)
		}

		if t.Async || t.Guardrail != nil {
			return nil, perr("tasks."+t.ID, "async and guardrail apply to sequential crews only")
		}

		ids = append(ids, t.ID)
	}

	b := newBuilder(name, maxStepsOr(c.MaxSteps, stepsPerTask*len(c.Tasks)+stepsPerTask), c.Budget)
	b.agent(managerNode, spec.Step{
		Agent:  c.Manager,
		Routes: ids,
		Prompt: "You manage a crew working on:\n{{json .input}}\n\nTasks:\n" + taskList(c.Tasks) +
			"\nAssign the next task by routing to it, with instructions in your answer. " +
			"When every task is done, write the crew's final answer without routing.",
	})

	for _, t := range c.Tasks {
		n := b.agent(t.ID, taskStep(t, append([]string{managerNode}, t.Context...)))
		n.OutputSchema = t.OutputSchema
		n.Next = managerNode
	}

	b.f.Output = resultOf(managerNode)

	return b.done(managerNode)
}
