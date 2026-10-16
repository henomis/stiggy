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

package project

import (
	"slices"
	"strconv"
	"strings"
)

// teamVars carries the references of a team's texts into the team's own
// flow: each distinct reference becomes an input (v1, v2...) of the team,
// filled by the calling node.
type teamVars struct {
	w     *wfc
	names map[string]string
	order []string
	exprs map[string]string
}

// text rewrites s for a template run inside the team's flow.
func (tv *teamVars) text(s string) (string, error) {
	return tv.w.rewrite(s, func(t target) string {
		name, ok := tv.names[t.expr]
		if !ok {
			name = "v" + strconv.Itoa(len(tv.order)+1)
			tv.names[t.expr] = name
			tv.order = append(tv.order, name)
			tv.exprs[name] = t.expr
		}

		if t.scalar {
			return "{{.input." + name + "}}"
		}

		return "{{json .input." + name + "}}"
	})
}

// input is the calling node's input expression.
func (tv *teamVars) input() string {
	if len(tv.order) == 0 {
		return ""
	}

	parts := make([]string, 0, len(tv.order))
	for _, n := range tv.order {
		parts = append(parts, n+": "+tv.exprs[n])
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// teamBlock compiles a team into a crew or a pattern (a flow of its own) and
// a subflow node that runs it.
func (w *wfc) teamBlock(n *Node) {
	var tp TeamParams
	if !w.params(n, &tp) {
		return
	}

	loc := w.loc(n.ID, "")
	tv := &teamVars{w: w, names: map[string]string{}, exprs: map[string]string{}}

	members := map[string]string{}
	all := make([]string, 0, len(tp.Members))

	for i, m := range tp.Members {
		if m.Name == "" {
			w.problem(n.ID, "members."+strconv.Itoa(i), "name the member")

			continue
		}

		name := w.agent(n, m.Name, m.AgentConfig, nil, nil)
		members[m.Name] = name
		all = append(all, name)
	}

	if len(all) == 0 {
		w.problem(n.ID, "members", "add the team's members")
	}

	member := func(field, who string) string {
		if who == "" {
			w.problem(n.ID, field, "choose a member")

			return ""
		}

		name, ok := members[who]
		if !ok {
			w.problem(n.ID, field, "%q is not a member of the team", who)
		}

		return name
	}

	task, err := tv.text(tp.Task)
	if err != nil {
		w.problem(n.ID, "task", "%s", err)
	}

	flow := w.c.flowNames.take(w.flow + "_" + w.id(n))

	var entry omap

	if tp.Kind == TeamCrew {
		entry = w.crew(n, tp, tv, member)
		w.c.crews = append(w.c.crews, kv{flow, entry})
		w.c.source("crews."+flow, loc)
	} else {
		entry = w.pattern(n, tp, all, member)
	}

	if tp.Kind != TeamCrew && entry != nil {
		w.c.patterns = append(w.c.patterns, kv{flow, entry.set("prompt", task)})
		w.c.source("patterns."+flow, loc)
	}

	w.nodes = append(w.nodes, omap{}.
		set("id", w.id(n)).
		set("type", "subflow").
		set("flow", flow).
		set("input", tv.input()).
		set("next", w.next(n)))
}

func (w *wfc) crew(n *Node, tp TeamParams, tv *teamVars, member func(field, who string) string) omap {
	if len(tp.Tasks) == 0 {
		w.problem(n.ID, "tasks", "add the crew's tasks")
	}

	ids := names{}
	tasks := make([]any, 0, len(tp.Tasks))

	for i, t := range tp.Tasks {
		field := "tasks." + strconv.Itoa(i)

		desc, err := tv.text(t.Description)
		if err != nil {
			w.problem(n.ID, field, "%s", err)
		}

		if strings.TrimSpace(t.Description) == "" {
			w.problem(n.ID, field, "describe the task")
		}

		agent := member(field, t.Member)
		tasks = append(tasks, omap{}.
			set("id", ids.take(slug(t.Member, "task")+"_task")).
			set("agent", agent).
			set("description", desc).
			set("expected_output", t.ExpectedOutput).
			set("async", t.Async))
	}

	crew := omap{}
	if tp.Manager != "" {
		crew = crew.set("process", "hierarchical").set("manager", member("manager", tp.Manager))
	}

	return crew.set("tasks", tasks)
}

func without(all []string, x string) []string {
	return slices.DeleteFunc(slices.Clone(all), func(s string) bool { return s == x })
}

// pattern is the patterns: entry of every team kind but crew.
func (w *wfc) pattern(n *Node, tp TeamParams, all []string, member func(field, who string) string) omap {
	switch tp.Kind {
	case TeamSupervisor:
		lead := member("lead", tp.Lead)

		return omap{}.set("type", "supervisor").set("supervisor", lead).set("workers", without(all, lead))
	case TeamSwarm:
		return omap{}.set("type", "swarm").set("agents", all)
	case TeamEvaluator:
		return omap{}.set("type", "evaluator_optimizer").
			set("generator", member("generator", tp.Generator)).
			set("evaluator", member("evaluator", tp.Evaluator)).
			set("max_rounds", tp.Rounds)
	case TeamDebate:
		judge := member("judge", tp.Judge)

		return omap{}.set("type", "debate").set("debaters", without(all, judge)).set("judge", judge).
			set("rounds", tp.Rounds)
	case TeamPlanExecute:
		return omap{}.set("type", "plan_execute").
			set("planner", member("planner", tp.Planner)).
			set("executor", member("executor", tp.Executor)).
			set("synthesizer", member("synthesizer", tp.Synthesizer)).
			set("max_parallel", tp.MaxParallel)
	default:
		w.problem(n.ID, "kind", "choose the kind of team")

		return nil
	}
}
