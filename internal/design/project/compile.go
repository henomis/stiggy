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
	"encoding/json"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

const (
	// endNode is packtrail's "complete the execution" target.
	endNode = "$end"
	// fieldCredential is the settings field of a block's model provider.
	fieldCredential = "credential"
)

// Loc locates something in the project: a workflow, a block, an attachment
// of the block, a field of its settings. Empty parts are unknown.
type Loc struct {
	Workflow   string `json:"workflow,omitempty"`
	Node       string `json:"node,omitempty"`
	Attachment string `json:"attachment,omitempty"`
	Field      string `json:"field,omitempty"`
}

// Problem is one thing to fix, located in the project.
type Problem struct {
	Loc

	Msg string `json:"msg"`
}

// Result is a compiled project.
type Result struct {
	// YAML is the stiggy fleet. It is written even when there are problems,
	// so the page can show it.
	YAML []byte
	// Problems are the design's problems: they make the fleet invalid.
	Problems []Problem

	sources []source
}

// source ties a path of the fleet (flows.story.nodes.plan) to the project.
type source struct {
	prefix string
	loc    Loc
}

// Locate maps a path of the fleet, as in a stiggy error, back to the project.
func (r *Result) Locate(path string) Loc {
	best, loc := -1, Loc{}

	for _, s := range r.sources {
		if (path == s.prefix || strings.HasPrefix(path, s.prefix+".")) && len(s.prefix) > best {
			best, loc = len(s.prefix), s.loc
		}
	}

	if best < 0 {
		return Loc{}
	}

	if loc.Field == "" {
		rest := strings.TrimPrefix(path[best:], ".")
		first, _, _ := strings.Cut(rest, ".")
		loc.Field = fieldOf(first)
	}

	return loc
}

// fieldOf names the block setting behind a fleet key.
func fieldOf(k string) string {
	switch k {
	case "system":
		return "instructions"
	case "output_schema":
		return "output"
	case "provider", "api_key", "base_url":
		return fieldCredential
	case "tools", "knowledge":
		return "attachments"
	case "flow":
		return "workflow"
	case refInput:
		return "inputs"
	case "agent", "type", "id", "next", "kind":
		return ""
	default:
		return k
	}
}

// Compile turns a project into a stiggy fleet. Problems with the design are
// in the result; the error is for a project it cannot read at all.
func Compile(p *Project) (*Result, error) {
	c := &compiler{
		p:         p,
		res:       &Result{},
		creds:     map[string]Credential{},
		models:    newSection(),
		tools:     newSection(),
		agents:    newSection(),
		embedders: newSection(),
		stores:    newSection(),
		knowledge: newSection(),
		flowNames: names{},
		flowOf:    map[string]string{},
	}

	for _, cr := range p.Credentials {
		c.creds[cr.ID] = cr
	}

	// Flow names first: a block may run any workflow of the project.
	for _, wf := range p.Workflows {
		c.flowOf[wf.ID] = c.flowNames.take(slug(wf.Name, "workflow"))
	}

	for i := range p.Workflows {
		c.workflow(&p.Workflows[i])
	}

	ns := p.Namespace
	if ns == "" {
		ns = strings.ReplaceAll(slug(p.Name, "stiggy"), "_", "-")
	}

	doc := omap{}.
		set("namespace", ns).
		set("models", c.models.omap()).
		set("embedders", c.embedders.omap()).
		set("vectorstores", c.stores.omap()).
		set("knowledge", c.knowledge.omap()).
		set("tools", c.tools.omap()).
		set("agents", c.agents.omap()).
		set("flows", c.flows).
		set("crews", c.crews).
		set("patterns", c.patterns)

	out, err := encode(doc)
	if err != nil {
		return nil, err
	}

	c.res.YAML = out

	return c.res, nil
}

type compiler struct {
	p   *Project
	res *Result

	creds map[string]Credential

	models, tools, agents, embedders, stores, knowledge *section

	flows, crews, patterns omap

	// flowNames are shared by flows, crews and patterns: all are flows.
	flowNames names
	flowOf    map[string]string
}

func (c *compiler) source(prefix string, loc Loc) {
	c.res.sources = append(c.res.sources, source{prefix: prefix, loc: loc})
}

func (c *compiler) problem(loc Loc, format string, args ...any) {
	c.res.Problems = append(c.res.Problems, Problem{Loc: loc, Msg: fmt.Sprintf(format, args...)})
}

// wfc compiles one workflow.
type wfc struct {
	c    *compiler
	wf   *Workflow
	flow string

	byID map[string]*Node
	// ids are the flow node ids in use.
	ids names
	// idOf maps a block to its flow node id; Start and Output have none.
	idOf map[string]string
	// out are the targets of each block's ports.
	out map[string]map[string][]string
	in  map[string][]string

	start  *Node
	output *Node
	// branch marks blocks that are branches of a Parallel: their join owns
	// what follows.
	branch map[string]bool

	nodes []any
}

func (c *compiler) workflow(wf *Workflow) {
	w := &wfc{
		c:      c,
		wf:     wf,
		flow:   c.flowOf[wf.ID],
		byID:   map[string]*Node{},
		ids:    names{endNode: true},
		idOf:   map[string]string{},
		out:    map[string]map[string][]string{},
		in:     map[string][]string{},
		branch: map[string]bool{},
	}

	c.source("flows."+w.flow, Loc{Workflow: wf.ID})

	for i := range wf.Nodes {
		n := &wf.Nodes[i]
		w.byID[n.ID] = n

		switch n.Type {
		case TypeStart:
			if w.start != nil {
				w.problem(n.ID, "", "a workflow has one Start block")
			}

			w.start = n
		case TypeOutput:
			if w.output != nil {
				w.problem(n.ID, "", "a workflow has one Output block")
			}

			w.output = n
		default:
			w.idOf[n.ID] = w.ids.take(slug(n.Name, n.Type))
		}
	}

	for _, e := range wf.Edges {
		if w.byID[e.From.Node] == nil || w.byID[e.To.Node] == nil {
			continue
		}

		port := e.From.Port
		if port == "" {
			port = PortOut
		}

		if w.out[e.From.Node] == nil {
			w.out[e.From.Node] = map[string][]string{}
		}

		w.out[e.From.Node][port] = append(w.out[e.From.Node][port], e.To.Node)
		w.in[e.To.Node] = append(w.in[e.To.Node], e.From.Node)
	}

	w.compile()
}

func (w *wfc) loc(node, field string) Loc { return Loc{Workflow: w.wf.ID, Node: node, Field: field} }

func (w *wfc) problem(node, field, format string, args ...any) {
	w.c.problem(w.loc(node, field), format, args...)
}

// id is the flow node id of a block.
func (w *wfc) id(n *Node) string { return w.idOf[n.ID] }

// target is where a port leads: a flow node id, or "" for nowhere (the
// workflow ends there, also when it leads to the Output block).
func (w *wfc) target(n *Node, port string) string {
	ts := w.out[n.ID][port]
	if len(ts) == 0 {
		return ""
	}

	if len(ts) > 1 {
		w.problem(n.ID, "", "the %q port connects to %d blocks; one port leads to one block "+
			"(use Parallel to run steps side by side)", port, len(ts))
	}

	t := w.byID[ts[0]]

	switch t.Type {
	case TypeOutput:
		return ""
	case TypeStart:
		w.problem(n.ID, "", "nothing can lead back to Start")

		return ""
	}

	return w.id(t)
}

// orEnd is a target for fields where "nowhere" must be spelled out.
func orEnd(t string) string {
	if t == "" {
		return endNode
	}

	return t
}

func (w *wfc) compile() {
	flow := omap{}

	if w.start == nil {
		w.c.problem(Loc{Workflow: w.wf.ID}, "add a Start block: it is where the workflow begins")
	} else if first := w.target(w.start, PortOut); first == "" {
		w.problem(w.start.ID, "", "connect Start to the first step")
	} else {
		flow = flow.set("start", first)
	}

	w.markBranches()

	for _, n := range w.order() {
		w.c.source("flows."+w.flow+".nodes."+w.id(n), w.loc(n.ID, ""))
		w.block(n)
	}

	if w.output != nil {
		flow = flow.set("output", w.outputExpr())
	}

	flow = flow.set("nodes", w.nodes)
	if len(w.nodes) == 0 {
		flow = append(flow, kv{"nodes", []any{}})
	}

	w.c.flows = append(w.c.flows, kv{w.flow, flow})
}

// order lists the blocks that become flow nodes, from Start onwards, then
// any others in canvas order.
func (w *wfc) order() []*Node {
	var out []*Node

	seen := map[string]bool{}

	var visit func(id string)

	visit = func(id string) {
		n := w.byID[id]
		if seen[id] || n == nil {
			return
		}

		seen[id] = true

		if _, ok := w.idOf[id]; ok {
			out = append(out, n)
		}

		for _, port := range w.ports(n) {
			for _, t := range w.out[id][port] {
				visit(t)
			}
		}
	}

	if w.start != nil {
		visit(w.start.ID)
	}

	for i := range w.wf.Nodes {
		visit(w.wf.Nodes[i].ID)
	}

	return out
}

// ports lists a block's ports in a stable order.
func (w *wfc) ports(n *Node) []string {
	ports := slices.Sorted(func(yield func(string) bool) {
		for p := range w.out[n.ID] {
			if !yield(p) {
				return
			}
		}
	})

	return ports
}

func (w *wfc) block(n *Node) {
	switch n.Type {
	case TypeAgent:
		w.agentBlock(n)
	case TypeRouter:
		w.routerBlock(n)
	case TypeCondition:
		w.conditionBlock(n)
	case TypeLoop:
		w.loopBlock(n)
	case TypeParallel:
		w.parallelBlock(n)
	case TypeMerge:
		w.mergeBlock(n)
	case TypeApproval:
		w.approvalBlock(n)
	case TypeSubworkflow:
		w.subworkflowBlock(n)
	case TypeTeam:
		w.teamBlock(n)
	default:
		w.problem(n.ID, "", "unknown block type %q", n.Type)
	}
}

// params decodes a block's settings, reporting a problem when it cannot.
func (w *wfc) params(n *Node, v any) bool {
	if len(n.Params) == 0 {
		return true
	}

	if err := decodeStrict(n.Params, v); err != nil {
		w.problem(n.ID, "", "invalid settings: %v", err)

		return false
	}

	return true
}

// next is the static successor of a block, unless it is a Parallel branch.
func (w *wfc) next(n *Node) string {
	if w.branch[n.ID] {
		return ""
	}

	return w.target(n, PortOut)
}

// inputType is the type of a workflow input.
func (w *wfc) inputType(path string) string {
	if w.start == nil {
		return ""
	}

	var sp StartParams
	if len(w.start.Params) > 0 && json.Unmarshal(w.start.Params, &sp) != nil {
		return ""
	}

	name, _, _ := strings.Cut(path, ".")
	for _, f := range sp.Inputs {
		if f.Name == name {
			if strings.Contains(path, ".") {
				return ""
			}

			return f.Type
		}
	}

	return ""
}

// outputType is the type of a field of a block's answer: "text" for an
// agent's text answer, a structured field's type, or object.
func (w *wfc) outputType(n *Node, path string) string {
	switch n.Type {
	case TypeAgent, TypeRouter:
		var cfg AgentConfig
		if json.Unmarshal(n.Params, &cfg) != nil {
			return FieldObject
		}

		name, _, _ := strings.Cut(path, ".")
		if len(cfg.Output) == 0 && name == "text" {
			return FieldText
		}

		for _, f := range cfg.Output {
			if f.Name == name && !strings.Contains(path, ".") {
				return f.Type
			}
		}
	}

	return FieldObject
}

// outputExpr builds the workflow's result from the Output block's fields.
func (w *wfc) outputExpr() string {
	var op OutputParams
	if !w.params(w.output, &op) || len(op.Fields) == 0 {
		return ""
	}

	parts := make([]string, 0, len(op.Fields))

	for i, f := range op.Fields {
		e, err := w.textExpr(f.Value)
		if err != nil {
			w.problem(w.output.ID, "fields."+strconv.Itoa(i), "%s", err)
		}

		parts = append(parts, slug(f.Name, "value")+": "+e)
	}

	return "{" + strings.Join(parts, ", ") + "}"
}

// textExpr turns a value into an expression: one reference, or plain text.
// Expressions cannot join text and values; that is what prompts are for.
func (w *wfc) textExpr(s string) (string, error) {
	if r, ok := onlyRef(s); ok {
		t, err := w.resolve(r)

		return t.expr, err
	}

	if len(parseRefs(s)) > 0 {
		return strconv.Quote(s), fmt.Errorf("use either one value or plain text here " +
			"(to combine text and values, write them in an AI Agent's prompt)")
	}

	return strconv.Quote(s), nil
}

// objectExpr builds {name: value, ...} from mappings.
func (w *wfc) objectExpr(n *Node, field string, ms []Mapping) string {
	if len(ms) == 0 {
		return ""
	}

	parts := make([]string, 0, len(ms))

	for i, m := range ms {
		e, err := w.textExpr(m.Value)
		if err != nil {
			w.problem(n.ID, field+"."+strconv.Itoa(i), "%s", err)
		}

		parts = append(parts, slug(m.Name, "value")+": "+e)
	}

	return "{" + strings.Join(parts, ", ") + "}"
}
