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
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"
)

const (
	// defaultApprovalTimeout is how long a Human approval waits by default.
	defaultApprovalTimeout = "24h"
	// minBranches is the fewest branches a Parallel block makes sense with.
	minBranches = 2
	// matchAny makes a Condition true when any rule matches.
	matchAny = "any"
)

func sortedKeys[V any](m map[string]V) []string { return slices.Sorted(maps.Keys(m)) }

func (w *wfc) conditionBlock(n *Node) {
	var cp ConditionParams
	if !w.params(n, &cp) {
		return
	}

	if len(cp.Rules) == 0 {
		w.problem(n.ID, "rules", "add a rule")
	}

	parts := make([]string, 0, len(cp.Rules))

	for i, r := range cp.Rules {
		e, err := w.rule(r)
		if err != nil {
			w.problem(n.ID, "rules."+strconv.Itoa(i), "%s", err)

			continue
		}

		parts = append(parts, e)
	}

	join := " && "
	if cp.Match == matchAny {
		join = " || "
	}

	when := strings.Join(parts, join)
	if len(parts) > 1 {
		when = "(" + strings.Join(parts, ")"+join+"(") + ")"
	}

	w.nodes = append(w.nodes, omap{}.
		set("id", w.id(n)).
		set("type", "choice").
		set("rules", []any{
			omap{}.set("when", when).set("to", orEnd(w.target(n, PortTrue))),
			omap{}.set("default", true).set("to", orEnd(w.target(n, PortFalse))),
		}))
}

// cmpOp is a binary comparison: its expression operator, and whether its
// value must be a number.
type cmpOp struct {
	expr    string
	numeric bool
}

var cmpOps = map[string]cmpOp{
	OpEquals:     {expr: "=="},
	OpNotEquals:  {expr: "!="},
	OpContains:   {expr: "contains"},
	OpStartsWith: {expr: "startsWith"},
	OpEndsWith:   {expr: "endsWith"},
	OpGreater:    {expr: ">", numeric: true},
	OpGreaterEq:  {expr: ">=", numeric: true},
	OpLess:       {expr: "<", numeric: true},
	OpLessEq:     {expr: "<=", numeric: true},
}

// rule is one comparison as an expression.
func (w *wfc) rule(r Rule) (string, error) {
	lr, ok := onlyRef(r.Left)
	if !ok {
		return "", errors.New("choose the value to check")
	}

	lt, err := w.resolve(lr)
	if err != nil {
		return "", err
	}

	left := lt.expr

	switch r.Op {
	case OpIsTrue:
		return left + " == true", nil
	case OpIsFalse:
		return left + " != true", nil
	case OpIsEmpty:
		return "(" + left + ` == nil || ` + left + ` == "")`, nil
	case OpNotEmpty:
		return "(" + left + ` != nil && ` + left + ` != "")`, nil
	case OpNotContains:
		v, verr := w.ruleValue(r.Value, false)

		return "not (" + left + " contains " + v + ")", verr
	}

	op, ok := cmpOps[r.Op]
	if !ok {
		return "", errors.New("choose a comparison")
	}

	v, err := w.ruleValue(r.Value, op.numeric)
	if err != nil {
		return "", err
	}

	return left + " " + op.expr + " " + v, nil
}

// ruleValue is the right-hand side of a comparison: a reference, a number,
// a boolean or text.
func (w *wfc) ruleValue(s string, numeric bool) (string, error) {
	if r, ok := onlyRef(s); ok {
		t, err := w.resolve(r)

		return t.expr, err
	}

	v := strings.TrimSpace(s)
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return v, nil
	}

	if numeric {
		return "", fmt.Errorf("%q is not a number", s)
	}

	if v == "true" || v == "false" {
		return v, nil
	}

	return strconv.Quote(s), nil
}

// markBranches finds the Parallel branches before any block compiles: a
// branch hands its successor over to the Merge.
func (w *wfc) markBranches() {
	for i := range w.wf.Nodes {
		n := &w.wf.Nodes[i]
		if n.Type != TypeParallel {
			continue
		}

		for _, ts := range w.out[n.ID] {
			for _, t := range ts {
				w.branch[t] = true
			}
		}
	}
}

func (w *wfc) parallelBlock(n *Node) {
	var pp ParallelParams
	if !w.params(n, &pp) {
		return
	}

	var (
		branches []string
		merge    string
	)

	for _, port := range pp.Branches {
		t := w.out[n.ID][port]
		if len(t) == 0 {
			continue
		}

		b := w.byID[t[0]]

		switch b.Type {
		case TypeAgent, TypeSubworkflow, TypeTeam:
		default:
			w.problem(n.ID, "branches", "branch %q starts with %q: a branch is one AI Agent, Agent team "+
				"or Run workflow block, followed by a Merge", port, b.Name)

			continue
		}

		branches = append(branches, w.id(b))

		after := w.out[b.ID][PortOut]

		switch {
		case len(after) != 1 || w.byID[after[0]].Type != TypeMerge:
			w.problem(b.ID, "", "this block is a branch of %q: connect it to a Merge block", n.Name)
		case merge == "":
			merge = after[0]
		case merge != after[0]:
			w.problem(n.ID, "branches", "all branches must lead to the same Merge block")
		}
	}

	if len(branches) < minBranches {
		w.problem(n.ID, "branches", "connect at least two branches")
	}

	next := ""
	if merge != "" {
		next = w.id(w.byID[merge])
	}

	w.nodes = append(w.nodes, omap{}.
		set("id", w.id(n)).
		set("type", "fanout").
		set("branches", branches).
		set("next", next))
}

func (w *wfc) mergeBlock(n *Node) {
	var mp MergeParams
	if !w.params(n, &mp) {
		return
	}

	policy := ""

	switch mp.Wait {
	case "first":
		policy = "any"
	case "min":
		least := 1
		if mp.Min != nil {
			least = *mp.Min
		}

		if least < 0 {
			w.problem(n.ID, "min", "must be 0 or more")
		}

		policy = fmt.Sprintf("min:%d", least)
	}

	w.nodes = append(w.nodes, omap{}.
		set("id", w.id(n)).
		set("type", "join").
		set("policy", policy).
		set("next", w.target(n, PortOut)))
}

// approvalBlock waits for a signal named after the block, then decides on
// its payload: {"approved": true} goes to approved, anything else (or the
// timeout, unless that port is connected) to rejected.
func (w *wfc) approvalBlock(n *Node) {
	var ap ApprovalParams
	if !w.params(n, &ap) {
		return
	}

	id := w.id(n)
	decision := w.ids.take(id + "_decision")
	w.c.source("flows."+w.flow+".nodes."+decision, w.loc(n.ID, ""))

	timeout := ap.Timeout
	if timeout == "" {
		timeout = defaultApprovalTimeout
	}

	onTimeout := w.target(n, PortTimeout)
	if onTimeout == "" {
		onTimeout = decision
	}

	var meta omap
	if ap.Message != "" {
		meta = omap{}.set("message", ap.Message)
	}

	w.nodes = append(w.nodes,
		omap{}.
			set("id", id).
			set("type", "await").
			set("signal", id).
			set("timeout", timeout).
			set("on_timeout", onTimeout).
			set("meta", meta).
			set("next", decision),
		omap{}.
			set("id", decision).
			set("type", "choice").
			set("rules", []any{
				omap{}.set("when", "signals."+id+"?.approved == true").set("to", orEnd(w.target(n, PortApproved))),
				omap{}.set("default", true).set("to", orEnd(w.target(n, PortRejected))),
			}))
}

func (w *wfc) subworkflowBlock(n *Node) {
	var sp SubworkflowParams
	if !w.params(n, &sp) {
		return
	}

	flow, ok := w.c.flowOf[sp.Workflow]

	switch {
	case sp.Workflow == "":
		w.problem(n.ID, "workflow", "choose the workflow to run")
	case !ok:
		w.problem(n.ID, "workflow", "the workflow no longer exists")
	case sp.Workflow == w.wf.ID:
		w.problem(n.ID, "workflow", "a workflow cannot run itself")
	}

	w.nodes = append(w.nodes, omap{}.
		set("id", w.id(n)).
		set("type", "subflow").
		set("flow", flow).
		set("input", w.objectExpr(n, "inputs", sp.Inputs)).
		set("next", w.next(n)))
}
