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

package dashboard

import (
	"encoding/json"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// Task and interrupt values of packtrail and stiggy.
const (
	taskRetrying    = "retrying"
	taskInterrupted = "interrupted"
	waitQuestion    = "question"
	waitReview      = "review"
)

// Field kinds.
const (
	kindText   = "text"
	kindNumber = "number"
	kindBool   = "bool"
	kindList   = "list"
	kindObject = "object"
)

// Node states, as the page colours them.
const (
	StateIdle    = "idle"
	StateDone    = "done"
	StateRunning = "running"
	StateRetry   = "retrying"
	StateWaiting = "waiting"
	StateFailed  = "failed"
)

// Graph is a flow drawn as nodes and edges.
type Graph struct {
	Flow        string      `json:"flow"`
	Description string      `json:"description,omitempty"`
	Start       string      `json:"start"`
	Nodes       []GraphNode `json:"nodes"`
	Edges       []GraphEdge `json:"edges"`
}

// GraphNode is one node of a flow, with what runs it.
type GraphNode struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	// Role is agent, activity, remote, or empty for control nodes.
	Role string `json:"role,omitempty"`
	// Runs is the agent, activity or remote the node runs.
	Runs string `json:"runs,omitempty"`
	// Human is set when the agent may stop for a human.
	Human bool `json:"human,omitempty"`
	// Detail is a short description: a signal, a subflow, a policy.
	Detail string `json:"detail,omitempty"`
}

// Edge kinds.
const (
	EdgeNext    = "next"
	EdgeRoute   = "route"
	EdgeRule    = "rule"
	EdgeBranch  = "branch"
	EdgeFailure = "failure"
	EdgeTimeout = "timeout"
)

// GraphEdge is a possible transition.
type GraphEdge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Kind  string `json:"kind"`
	Label string `json:"label,omitempty"`
}

// GraphOf draws def. Edges to flow.End are dropped: the page draws an end
// marker for nodes without successors.
func GraphOf(def *flow.Flow) Graph {
	g := Graph{Flow: def.Name, Description: def.Description, Start: def.Start}

	if g.Start == "" && len(def.Nodes) > 0 {
		g.Start = entry(def)
	}

	seen := map[string]bool{}
	edge := func(from, to, kind, label string) {
		key := from + "\x00" + to

		if to == "" || to == flow.End || seen[key] {
			return
		}

		seen[key] = true

		g.Edges = append(g.Edges, GraphEdge{From: from, To: to, Kind: kind, Label: label})
	}

	for i := range def.Nodes {
		n := &def.Nodes[i]
		gn := GraphNode{ID: n.ID, Type: n.Type, Detail: detailOf(n)}

		gn.Runs, gn.Role, gn.Human = runnerOf(n)

		g.Nodes = append(g.Nodes, gn)

		edgesOf(def, n, edge)
	}

	if g.Nodes == nil {
		g.Nodes = []GraphNode{}
	}

	if g.Edges == nil {
		g.Edges = []GraphEdge{}
	}

	return g
}

// edgesOf adds the transitions out of n.
func edgesOf(def *flow.Flow, n *flow.Node, edge func(from, to, kind, label string)) {
	// A fan-out continues at its join once the branches end: draw
	// branch -> join rather than fanout -> join.
	if n.Type == flow.NodeFanout && len(n.Branches) > 0 {
		for _, b := range n.Branches {
			if bn := nodeByID(def, b); bn != nil && bn.Next == "" {
				edge(b, n.Next, EdgeNext, "")
			}
		}
	} else {
		edge(n.ID, n.Next, EdgeNext, "")
	}

	for _, to := range n.Dynamic {
		edge(n.ID, to, EdgeRoute, "")
	}

	for _, r := range n.Rules {
		edge(n.ID, r.To, EdgeRule, ruleLabel(r))
	}

	for _, b := range n.Branches {
		edge(n.ID, b, EdgeBranch, "")
	}

	edge(n.ID, n.OnFailure, EdgeFailure, "on failure")
	edge(n.ID, n.OnError, EdgeFailure, "on error")
	edge(n.ID, n.OnTimeout, EdgeTimeout, "on timeout")
}

func nodeByID(def *flow.Flow, id string) *flow.Node {
	for i := range def.Nodes {
		if def.Nodes[i].ID == id {
			return &def.Nodes[i]
		}
	}

	return nil
}

// Who runs a step.
const (
	roleAgent    = "agent"
	roleActivity = "activity"
	roleRemote   = "remote"
)

// runnerOf is the agent, activity or remote a stiggy step runs, its role,
// and whether it stops for a human; empty for other nodes.
func runnerOf(n *flow.Node) (name, role string, human bool) {
	s, ok, err := spec.StepOf(n)
	if !ok || err != nil {
		return "", "", false
	}

	switch {
	case s.Agent != "":
		return s.Agent, roleAgent, s.HumanInput
	case s.Activity != "":
		return s.Activity, roleActivity, s.HumanInput
	case s.Remote != "":
		return s.Remote, roleRemote, s.HumanInput
	default:
		return "", "", s.HumanInput
	}
}

// entry is the node with no inbound transition.
func entry(def *flow.Flow) string {
	in := map[string]bool{}

	for _, n := range def.Nodes {
		for _, to := range slices.Concat([]string{n.Next, n.OnFailure, n.OnError, n.OnTimeout}, n.Dynamic, n.Branches) {
			in[to] = true
		}

		for _, r := range n.Rules {
			in[r.To] = true
		}
	}

	for _, n := range def.Nodes {
		if !in[n.ID] {
			return n.ID
		}
	}

	return def.Nodes[0].ID
}

func ruleLabel(r flow.Rule) string {
	switch {
	case r.Name != "":
		return r.Name
	case r.Default:
		return "otherwise"
	default:
		return r.When
	}
}

func detailOf(n *flow.Node) string {
	switch n.Type {
	case flow.NodeAwait:
		return "waits for " + n.Signal
	case flow.NodeSubflow:
		return "runs " + n.Flow
	case flow.NodeJoin:
		if n.Policy != "" {
			return "joins " + n.Policy
		}

		return "joins all"
	case flow.NodeMap:
		return "for each " + n.Over
	default:
		return ""
	}
}

// NodeStatus is how far a node got in one execution.
type NodeStatus struct {
	State  string `json:"state"`
	Visits int    `json:"visits,omitempty"`
	// Attempt is the attempt running or retrying.
	Attempt int    `json:"attempt,omitempty"`
	Error   string `json:"error,omitempty"`
	// Done and Total are a map node's items.
	Done  int `json:"done,omitempty"`
	Total int `json:"total,omitempty"`
	// Child is the execution a subflow node runs.
	Child string `json:"child,omitempty"`
}

// NodeStatuses derives every node's state from st.
func NodeStatuses(st *packtrail.State) map[string]NodeStatus {
	out := map[string]NodeStatus{}
	get := func(node string) NodeStatus {
		ns, ok := out[node]
		if !ok {
			ns = NodeStatus{State: StateIdle, Visits: st.Visits[node]}
		}

		return ns
	}
	set := func(node, state string, f func(*NodeStatus)) {
		ns := get(node)
		if slices.Index(urgency, state) >= slices.Index(urgency, ns.State) {
			ns.State = state
		}

		if f != nil {
			f(&ns)
		}

		out[node] = ns
	}

	for node := range st.Visits {
		set(node, StateDone, nil)
	}

	for node := range st.Results {
		set(node, StateDone, nil)
	}

	settled(st, set)
	active(st, set)

	return out
}

// urgency orders node states: a node shows its most urgent one.
var urgency = []string{StateIdle, StateDone, StateFailed, StateRunning, StateRetry, StateWaiting}

type setState func(node, state string, f func(*NodeStatus))

// settled marks the failed nodes.
func settled(st *packtrail.State, set setState) {
	for node, e := range st.Errors {
		set(node, StateFailed, func(ns *NodeStatus) { ns.Error = e.Error })
	}

	if st.FailedNode != "" {
		set(st.FailedNode, StateFailed, func(ns *NodeStatus) {
			if ns.Error == "" {
				ns.Error = st.Error
			}
		})
	}
}

// active marks the nodes running or waiting.
func active(st *packtrail.State, set setState) {
	for _, t := range st.Tasks {
		state := StateRunning

		switch t.Status {
		case taskRetrying:
			state = StateRetry
		case taskInterrupted:
			state = StateWaiting
		}

		set(t.Node, state, func(ns *NodeStatus) { ns.Attempt = t.Attempt })
	}

	for node := range st.Awaits {
		set(node, StateWaiting, nil)
	}

	for node, m := range st.Maps {
		set(node, StateRunning, func(ns *NodeStatus) { ns.Done, ns.Total = m.Done, len(m.Items) })
	}

	for key, c := range st.Children {
		set(c.NodeOf(key), StateRunning, func(ns *NodeStatus) {
			if c.Owner == "" {
				ns.Child = c.ChildID
			}
		})
	}
}

// Usage is what an execution spent.
type Usage struct {
	TokensIn   float64 `json:"tokens_in"`
	TokensOut  float64 `json:"tokens_out"`
	CacheRead  float64 `json:"cache_read,omitempty"`
	Reasoning  float64 `json:"reasoning,omitempty"`
	CostUSD    float64 `json:"cost_usd"`
	LLMCalls   float64 `json:"llm_calls,omitempty"`
	ToolCalls  float64 `json:"tool_calls,omitempty"`
	ToolErrors float64 `json:"tool_errors,omitempty"`
	AgentSteps float64 `json:"agent_steps,omitempty"`
	Steps      float64 `json:"steps,omitempty"`
	// ByAgent and ByModel split tokens and cost.
	ByAgent []Share `json:"by_agent,omitempty"`
	ByModel []Share `json:"by_model,omitempty"`
}

// Share is one agent's or model's part of the usage.
type Share struct {
	Name      string  `json:"name"`
	TokensIn  float64 `json:"tokens_in"`
	TokensOut float64 `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// Add sums o into u (shares included).
func (u *Usage) Add(o Usage) {
	u.TokensIn += o.TokensIn
	u.TokensOut += o.TokensOut
	u.CacheRead += o.CacheRead
	u.Reasoning += o.Reasoning
	u.CostUSD += o.CostUSD
	u.LLMCalls += o.LLMCalls
	u.ToolCalls += o.ToolCalls
	u.ToolErrors += o.ToolErrors
	u.AgentSteps += o.AgentSteps
	u.Steps += o.Steps
	u.ByAgent = mergeShares(u.ByAgent, o.ByAgent)
	u.ByModel = mergeShares(u.ByModel, o.ByModel)
}

func mergeShares(a, b []Share) []Share {
	if len(b) == 0 {
		return a
	}

	idx := map[string]int{}
	out := slices.Clone(a)

	for i, s := range out {
		idx[s.Name] = i
	}

	for _, s := range b {
		i, ok := idx[s.Name]
		if !ok {
			idx[s.Name] = len(out)
			out = append(out, Share{Name: s.Name})
			i = len(out) - 1
		}

		out[i].TokensIn += s.TokensIn
		out[i].TokensOut += s.TokensOut
		out[i].CostUSD += s.CostUSD
	}

	sortShares(out)

	return out
}

func sortShares(s []Share) {
	sort.SliceStable(s, func(i, j int) bool {
		if s[i].CostUSD != s[j].CostUSD {
			return s[i].CostUSD > s[j].CostUSD
		}

		ti, tj := s[i].TokensIn+s[i].TokensOut, s[j].TokensIn+s[j].TokensOut
		if ti != tj {
			return ti > tj
		}

		return s[i].Name < s[j].Name
	})
}

// UsageOf reads the counters stiggy's agent steps report.
func UsageOf(counters map[string]float64) Usage {
	u := Usage{
		TokensIn:   counters[counterTokensIn],
		TokensOut:  counters[counterTokensOut],
		CacheRead:  counters[counterTokensCacheRead],
		Reasoning:  counters[counterTokensReasoning],
		CostUSD:    counters[counterCostUSD],
		LLMCalls:   counters[counterLLMCalls],
		ToolCalls:  counters[counterToolCalls],
		ToolErrors: counters[counterToolErrors],
		AgentSteps: counters[counterAgentSteps],
		Steps:      counters["steps"],
	}

	agents, models := map[string]*Share{}, map[string]*Share{}

	for k, v := range counters {
		splits := map[string]map[string]*Share{counterByAgent: agents, counterByModel: models}
		for sep, into := range splits {
			base, name, ok := strings.Cut(k, sep)
			if !ok || name == "" {
				continue
			}

			s := into[name]
			if s == nil {
				s = &Share{Name: name}
				into[name] = s
			}

			switch base {
			case counterTokensIn:
				s.TokensIn = v
			case counterTokensOut:
				s.TokensOut = v
			case counterCostUSD:
				s.CostUSD = v
			}
		}
	}

	u.ByAgent, u.ByModel = sharesOf(agents), sharesOf(models)

	return u
}

func sharesOf(m map[string]*Share) []Share {
	if len(m) == 0 {
		return nil
	}

	out := make([]Share, 0, len(m))
	for _, s := range m {
		out = append(out, *s)
	}

	sortShares(out)

	return out
}

// Wait is an agent step waiting for a human.
type Wait struct {
	Node  string `json:"node"`
	Agent string `json:"agent,omitempty"`
	// Kind is question (the agent asks) or review (approve or give feedback).
	Kind string `json:"kind"`
	// Text is the question, or the answer to review.
	Text string `json:"text"`
	// Route is where an approved review goes next, and why.
	Route       string    `json:"route,omitempty"`
	RouteReason string    `json:"route_reason,omitempty"`
	AskedAt     time.Time `json:"asked_at,omitzero"`
	// Turns are the earlier exchanges of this step with a human.
	Turns []Turn `json:"turns,omitempty"`
}

// Turn is an earlier exchange: what was asked and what the human replied.
type Turn struct {
	Kind  string `json:"kind"`
	Text  string `json:"text"`
	Reply string `json:"reply"`
}

// interrupt mirrors agentrun.Interrupt's JSON.
type interrupt struct {
	Kind     string `json:"kind"`
	Agent    string `json:"agent"`
	Node     string `json:"node"`
	Question string `json:"question"`
	Stiggy   struct {
		Turns   []Turn `json:"turns"`
		Pending *struct {
			Kind        string    `json:"kind"`
			Text        string    `json:"text"`
			Route       string    `json:"route"`
			RouteReason string    `json:"route_reason"`
			AskedAt     time.Time `json:"asked_at"`
		} `json:"pending"`
	} `json:"stiggy"`
}

// WaitsOf lists the steps of st waiting for a human, by node.
func WaitsOf(st *packtrail.State) []Wait {
	var out []Wait

	for _, t := range st.Tasks {
		if t.Status != taskInterrupted {
			continue
		}

		w := Wait{Node: t.Node, Kind: waitQuestion}

		var in interrupt
		if len(t.Interrupt) > 0 && json.Unmarshal(t.Interrupt, &in) == nil {
			w.Agent, w.Kind, w.Text, w.Turns = in.Agent, in.Kind, in.Question, in.Stiggy.Turns

			if p := in.Stiggy.Pending; p != nil {
				w.Kind, w.Text, w.Route, w.RouteReason, w.AskedAt = p.Kind, p.Text, p.Route, p.RouteReason, p.AskedAt
			}
		}

		if w.Kind == "" {
			w.Kind = waitQuestion
		}

		out = append(out, w)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Node < out[j].Node })

	return out
}

// Field is one entry of an execution's input or output, ready to show.
type Field struct {
	Key string `json:"key"`
	// Kind is text, number, bool, list or object.
	Kind string `json:"kind"`
	Text string `json:"text"`
	// Items are a list's entries or an object's fields, one level down.
	Items []Field `json:"items,omitempty"`
}

// scalarKey names the field of a value that is not an object.
const scalarKey = "value"

// maxDepth bounds how deep FieldsOf unfolds nested values.
const maxDepth = 3

// FieldsOf turns a JSON value into fields: an object gives one field per
// key, anything else a single field named "value".
func FieldsOf(raw json.RawMessage) []Field {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return []Field{{Key: scalarKey, Kind: kindText, Text: string(raw)}}
	}

	if m, ok := v.(map[string]any); ok {
		return objectFields(m, 0)
	}

	return []Field{fieldOf(scalarKey, v, 0)}
}

func objectFields(m map[string]any, depth int) []Field {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}

	sort.Strings(keys)

	out := make([]Field, 0, len(keys))
	for _, k := range keys {
		out = append(out, fieldOf(k, m[k], depth))
	}

	return out
}

func fieldOf(key string, v any, depth int) Field {
	switch x := v.(type) {
	case nil:
		return Field{Key: key, Kind: kindText, Text: "—"}
	case string:
		return Field{Key: key, Kind: kindText, Text: x}
	case bool:
		return Field{Key: key, Kind: kindBool, Text: strconv.FormatBool(x)}
	case float64:
		return Field{Key: key, Kind: kindNumber, Text: strconv.FormatFloat(x, 'f', -1, 64)}
	case []any:
		f := Field{Key: key, Kind: kindList, Text: plural(len(x), "item")}

		if depth < maxDepth {
			for i, e := range x {
				f.Items = append(f.Items, fieldOf(strconv.Itoa(i+1), e, depth+1))
			}
		}

		return f
	case map[string]any:
		f := Field{Key: key, Kind: kindObject, Text: plural(len(x), "field")}

		if depth < maxDepth {
			f.Items = objectFields(x, depth+1)
		}

		return f
	default:
		return Field{Key: key, Kind: kindText, Text: fmt.Sprint(x)}
	}
}

// previewMax bounds an input preview, in runes.
const previewMax = 140

// PreviewOf is a JSON value in one line, for lists: "topic: ants, n: 3",
// nested values shown by size, clipped to a sensible length.
func PreviewOf(raw json.RawMessage) string {
	fields := FieldsOf(raw)

	// Text says most, then numbers and booleans; nested values last.
	sort.SliceStable(fields, func(i, j int) bool { return previewRank(fields[i]) < previewRank(fields[j]) })

	parts := make([]string, 0, len(fields))

	for _, f := range fields {
		text := strings.Join(strings.Fields(f.Text), " ")
		if f.Key == scalarKey && len(fields) == 1 {
			parts = append(parts, text)
		} else {
			parts = append(parts, f.Key+": "+text)
		}
	}

	out := []rune(strings.Join(parts, ", "))
	if len(out) > previewMax {
		return string(out[:previewMax-1]) + "…"
	}

	return string(out)
}

// previewRank orders preview fields: text, then scalars, then nested values.
func previewRank(f Field) int {
	const (
		text = iota
		scalar
		nested
	)

	switch f.Kind {
	case kindText:
		return text
	case kindList, kindObject:
		return nested
	default:
		return scalar
	}
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}

	return strconv.Itoa(n) + " " + word + "s"
}
