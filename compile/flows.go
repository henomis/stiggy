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

package compile

import (
	"fmt"
	"maps"
	"slices"
	"strconv"
	"strings"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

func (c *compiler) flows() []*flow.Flow {
	out := make([]*flow.Flow, 0, len(c.fleet.Flows))

	for _, name := range sortedKeys(c.fleet.Flows) {
		f := c.fleet.Flows[name]
		path := "flows." + name

		if f == nil {
			c.errorf(path, "flow is nil")

			continue
		}

		before := len(c.errs)

		c.checkName(path, "flow", name)

		for ch := range f.Channels {
			if strings.HasPrefix(ch, spec.MemoryChannelPrefix) {
				c.errorf(path+".channels."+ch, "channel names starting with %q are reserved for agent memory",
					spec.MemoryChannelPrefix)
			}
		}

		if f.Name != name {
			c.errorf(path+".name", "name %q does not match the flow key %q", f.Name, name)
		}

		fc := newFlowCtx(f)
		for i := range f.Nodes {
			c.node(path, fc, &f.Nodes[i], i)
		}

		// stiggy's own checks cover its fields with better messages; packtrail
		// would repeat some of them (a bad route is also a dangling dynamic
		// edge), so it only runs on flows stiggy accepts.
		if len(c.errs) > before {
			continue
		}

		// Clone normalizes the flow the way packtrail stores it (JSON shapes)
		// and validates the copy.
		clone, err := c.withMemoryChannels(f).Clone()
		if err != nil {
			c.packtrailErrors(path, err)

			continue
		}

		out = append(out, clone)
	}

	return out
}

// withMemoryChannels returns f with the hidden channel of every agent with
// execution memory that runs in it; f itself is not modified.
func (c *compiler) withMemoryChannels(f *flow.Flow) *flow.Flow {
	var add []string

	for i := range f.Nodes {
		step, ok, err := spec.StepOf(&f.Nodes[i])
		if err != nil || !ok || step.Agent == "" {
			continue
		}

		a := c.fleet.Agents[step.Agent]
		if a.Memory.TypeOrDefault() == spec.MemoryExecution {
			add = append(add, spec.MemoryChannel(step.Agent))
		}
	}

	if len(add) == 0 {
		return f
	}

	out := *f
	out.Channels = make(map[string]flow.Channel, len(f.Channels)+len(add))

	maps.Copy(out.Channels, f.Channels)

	// Keep memory out of the execution output, so it is what it would be
	// without memory: the user's channels, or the last node's result.
	if f.Output == "" {
		out.Output = userOutput(f.Channels)
	}

	for _, ch := range add {
		out.Channels[ch] = flow.Channel{Reducer: flow.ReducerAppend}
	}

	return &out
}

// userOutput is the output expression of a flow whose own channels are
// channels: those channels as an object, or the last node's result when it
// declares none.
func userOutput(channels map[string]flow.Channel) string {
	if len(channels) == 0 {
		return "results[last_node]"
	}

	fields := make([]string, 0, len(channels))
	for _, ch := range sortedKeys(channels) {
		fields = append(fields, fmt.Sprintf("%q: channels[%q]", ch, ch))
	}

	return "{" + strings.Join(fields, ", ") + "}"
}

// flowCtx holds what node checks need to know about their flow.
type flowCtx struct {
	ids      map[string]bool
	branches map[string]bool
	channels map[string]flow.Channel
}

func newFlowCtx(f *flow.Flow) *flowCtx {
	fc := &flowCtx{ids: map[string]bool{}, branches: map[string]bool{}, channels: f.Channels}

	for _, n := range f.Nodes {
		fc.ids[n.ID] = true

		if n.Type == flow.NodeFanout {
			for _, b := range n.Branches {
				fc.branches[b] = true
			}
		}
	}

	return fc
}

func nodePath(flowPath string, n *flow.Node, i int) string {
	if n.ID == "" {
		return flowPath + ".nodes." + strconv.Itoa(i)
	}

	return flowPath + ".nodes." + n.ID
}

func (c *compiler) node(flowPath string, fc *flowCtx, n *flow.Node, i int) {
	path := nodePath(flowPath, n, i)

	if (n.Type == flow.NodeSubflow || n.Type == flow.NodeMap) && n.Flow != "" {
		if _, ok := c.fleet.Flows[n.Flow]; !ok {
			c.errorf(path+".flow", "unknown flow %q", n.Flow)
		}
	}

	step, ok, err := spec.StepOf(n)

	switch {
	case err != nil:
		c.errorf(path+".meta", "%v", err)

		return
	case !ok:
		if spec.IsStiggyKind(n.Kind) {
			c.errorf(path+".kind", "kind %q uses a prefix reserved for stiggy steps (%s, %s); use agent or activity",
				n.Kind, spec.AgentKindPrefix, spec.ActivityKindPrefix)
		}

		return
	}

	c.stepTarget(path, n, step)
	c.stepAgentOnly(path, step)
	c.stepPrompt(path, step)
	c.stepContext(path, fc, step)
	c.stepWrites(path, fc, step)
	c.stepRoutes(path, fc, n, step)
}

func (c *compiler) stepTarget(path string, n *flow.Node, s spec.Step) {
	switch {
	case countTargets(s) != 1:
		c.errorf(path, "a step runs exactly one of agent, activity or remote")

		return
	case n.Kind != s.Kind() || !slices.Equal(n.Dynamic, s.DynamicTargets()):
		c.errorf(path, "kind and dynamic edges disagree with the step; build the node with spec.ApplyStep")

		return
	}

	if s.Agent != "" {
		if _, ok := c.fleet.Agents[s.Agent]; !ok {
			c.errorf(path+".agent", "unknown agent %q", s.Agent)

			return
		}

		c.kinds[n.Kind] = Worker{Kind: n.Kind, Agent: s.Agent}

		return
	}

	if s.Remote != "" {
		c.stepRemote(path, n, s)

		return
	}

	// The activity becomes part of a worker kind: check the kind, which is the
	// longer token.
	if err := packtrail.ValidateName("activity kind", n.Kind); err != nil {
		c.errorf(path+".activity", "%v", err)

		return
	}

	if !c.cat.HasActivity(s.Activity) {
		c.errorf(path+".activity", "unknown activity %q", s.Activity)

		return
	}

	c.kinds[n.Kind] = Worker{Kind: n.Kind, Activity: s.Activity}
}

func countTargets(s spec.Step) int {
	n := 0

	for _, t := range []string{s.Agent, s.Activity, s.Remote} {
		if t != "" {
			n++
		}
	}

	return n
}

// stepRemote checks a step that prompts a remote agent. A remote answers
// with text only, so the fields that need stiggy's tools are refused.
func (c *compiler) stepRemote(path string, n *flow.Node, s spec.Step) {
	if _, ok := c.fleet.Remotes[s.Remote]; !ok {
		c.errorf(path+".remote", "unknown remote %q", s.Remote)

		return
	}

	if len(s.Routes) > 0 {
		c.errorf(path+".routes", "routes need stiggy's route tools; a remote agent cannot use them")
	}

	if s.HumanInput {
		c.errorf(path+".human_input", "human_input applies to stiggy agents only")
	}

	c.kinds[n.Kind] = Worker{Kind: n.Kind, Remote: s.Remote}
}

// stepAgentOnly rejects the agent-only fields on activity steps.
func (c *compiler) stepAgentOnly(path string, s spec.Step) {
	if s.Activity == "" {
		return
	}

	for _, f := range []struct {
		name string
		set  bool
	}{
		{"prompt", s.Prompt != ""},
		{"routes", len(s.Routes) > 0},
		{"expected_output", s.ExpectedOutput != ""},
		{"context", len(s.Context) > 0},
		{"human_input", s.HumanInput},
	} {
		if f.set {
			c.errorf(path+"."+f.name, "%s applies to agent steps only", f.name)
		}
	}
}

func (c *compiler) stepContext(path string, fc *flowCtx, s spec.Step) {
	for _, id := range s.Context {
		if !fc.ids[id] {
			c.errorf(path+".context", "unknown node %q", id)
		}
	}

	if dup := firstDuplicate(s.Context); dup != "" {
		c.errorf(path+".context", "node %q listed twice", dup)
	}
}

func (c *compiler) stepPrompt(path string, s spec.Step) {
	if s.Prompt == "" {
		return
	}

	if _, err := spec.ParsePrompt(path, s.Prompt); err != nil {
		c.errorf(path+".prompt", "%v", err)
	}
}

func (c *compiler) stepWrites(path string, fc *flowCtx, s spec.Step) {
	for _, ch := range sortedKeys(s.Writes) {
		if _, ok := fc.channels[ch]; !ok {
			c.errorf(path+".writes", "channel %q is not declared in the flow's channels", ch)
		}

		if _, err := spec.ParseWritePath(s.Writes[ch]); err != nil {
			c.errorf(path+".writes", "channel %q: %v", ch, err)
		}
	}
}

func (c *compiler) stepRoutes(path string, fc *flowCtx, n *flow.Node, s spec.Step) {
	if len(s.Routes) == 0 {
		return
	}

	rpath := path + ".routes"

	// packtrail only accepts an agent-chosen next from a task that owns its
	// own control flow: a map item or a fan-out branch cannot route.
	switch {
	case n.Type == flow.NodeMap:
		c.errorf(rpath, "routes are not allowed on a map node: a map item cannot choose the next node")
	case fc.branches[n.ID]:
		c.errorf(rpath, "routes are not allowed on a fan-out branch: a branch cannot choose the next node")
	}

	if dup := firstDuplicate(s.Routes); dup != "" {
		c.errorf(rpath, "route %q listed twice", dup)
	}

	for _, r := range s.Routes {
		switch {
		case r == spec.RouteHuman && fc.ids[spec.RouteHuman]:
			c.errorf(rpath, "route %q is ambiguous: it is reserved for human handoff and also a node id",
				spec.RouteHuman)
		case r == spec.RouteHuman:
		case !fc.ids[r]:
			c.errorf(rpath, "unknown node %q", r)
		}
	}
}

// packtrailErrors turns packtrail's validation errors into path-located
// errors.
func (c *compiler) packtrailErrors(flowPath string, err error) {
	verrs := flow.ValidationErrors(err)
	if len(verrs) == 0 {
		c.errorf(flowPath, "%v", err)

		return
	}

	for _, ve := range verrs {
		path := flowPath
		if ve.Node != "" {
			path += ".nodes." + ve.Node
		}

		msg := ve.Msg
		if ve.Field != "" {
			path += "." + fieldPath(ve.Field)
			msg = ve.Field + ": " + msg
		}

		c.add(path, msg)
	}
}

// fieldPath converts packtrail's field notation ("rules[1].when") to the
// dotted paths config.Source records ("rules.1.when").
func fieldPath(field string) string {
	r := strings.NewReplacer("[", ".", "]", "")

	return r.Replace(field)
}
