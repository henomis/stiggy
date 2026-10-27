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
	"slices"
	"strings"

	natsagent "github.com/henomis/phero/v2/nats"
	"github.com/henomis/phero/v2/tool/permission"

	"github.com/henomis/stiggy/spec"
)

func (c *compiler) agents() {
	for _, name := range sortedKeys(c.fleet.Agents) {
		a := c.fleet.Agents[name]
		path := "agents." + name

		// Agent names become worker kinds and, when exposed, protocol subject
		// tokens: phero's rule is the stricter one.
		if err := natsagent.ValidateSubjectToken(name); err != nil {
			c.errorf(path, "agent name: %v", err)
		}

		c.modelRef(path+".model", a.Model, true)
		c.refs(path+".tools", "tool", a.Tools, func(n string) bool { _, ok := c.fleet.Tools[n]; return ok })
		c.refs(path+".knowledge", "knowledge", a.Knowledge,
			func(n string) bool { _, ok := c.fleet.Knowledge[n]; return ok })
		c.refs(path+".delegates", "agent or remote", a.Delegates, func(n string) bool {
			_, agent := c.fleet.Agents[n]
			_, remote := c.fleet.Remotes[n]

			return agent || remote
		})

		if slices.Contains(a.Delegates, name) {
			c.errorf(path+".delegates", "an agent cannot delegate to itself")
		}

		if a.MaxIterations < 0 {
			c.errorf(path+".max_iterations", "must not be negative")
		}

		if a.MaxParallelTools < 0 {
			c.errorf(path+".max_parallel_tools", "must not be negative")
		}

		c.compaction(path+".compaction", a.Compaction)
		c.permissions(path+".permissions", a.Permissions)
		c.memory(path+".memory", a.Memory)
		c.session(path+".memory.session", name, a.Memory)
		c.systemPrompt(path, name, a)
	}

	c.delegationCycles()
}

// permissions checks an agent's permission rules parse and its default is
// known.
func (c *compiler) permissions(path string, p *spec.Permissions) {
	if p == nil {
		return
	}

	switch p.Default {
	case "", spec.PermissionAllow, spec.PermissionDeny:
	default:
		c.errorf(path+".default", "must be %s or %s", spec.PermissionAllow, spec.PermissionDeny)
	}

	for _, r := range []struct {
		field string
		specs []string
		with  func(...string) permission.Option
	}{
		{"allow", p.Allow, permission.WithAllow},
		{"deny", p.Deny, permission.WithDeny},
	} {
		for i, rule := range r.specs {
			if _, err := permission.New(r.with(rule)); err != nil {
				c.errorf(fmt.Sprintf("%s.%s.%d", path, r.field, i), "%v", err)
			}
		}
	}
}

// compaction checks an agent's compaction settings; an empty model means
// the agent's own.
func (c *compiler) compaction(path string, cp *spec.Compaction) {
	if cp == nil {
		return
	}

	c.modelRef(path+".model", cp.Model, false)

	for _, f := range []struct {
		name  string
		value int
	}{
		{"at_tokens", cp.AtTokens},
		{"keep_recent", cp.KeepRecent},
		{"keep_tokens", cp.KeepTokens},
		{"max_summary_tokens", cp.MaxSummaryTokens},
	} {
		if f.value < 0 {
			c.errorf(path+"."+f.name, "must not be negative")
		}
	}

	// The kept messages alone would reach the threshold, so every call
	// would compact again.
	if cp.AtTokens > 0 && cp.KeepTokens >= cp.AtTokens {
		c.errorf(path+".keep_tokens", "must be below at_tokens")
	}
}

// systemPrompt checks an agent's system prompt fields, which are templates
// over the job context. An exposed agent answers prompts outside any
// execution, and its delegates run with it: their fields must be static.
func (c *compiler) systemPrompt(path, name string, a spec.Agent) {
	exposedBy := c.exposedBy(name)

	for _, f := range [][2]string{{"system", a.System}, {"role", a.Role}, {"goal", a.Goal}, {"backstory", a.Backstory}} {
		if f[1] == "" {
			continue
		}

		t, err := spec.ParsePrompt(f[0], f[1])
		if err != nil {
			c.errorf(path+"."+f[0], "%v", err)

			continue
		}

		switch {
		case spec.IsStatic(t) || exposedBy == "":
		case exposedBy == name:
			c.errorf(path+"."+f[0], "an exposed agent has no job context: its %s cannot be a template", f[0])
		default:
			c.errorf(path+"."+f[0], "a delegate of exposed agent %q has no job context: its %s cannot be a template",
				exposedBy, f[0])
		}
	}
}

// exposedBy returns the exposed agent that runs agent, itself or through
// delegation, or "" when none does.
func (c *compiler) exposedBy(agent string) string {
	if c.fleet.Expose == nil {
		return ""
	}

	for _, root := range c.fleet.Expose.Agents {
		seen := map[string]bool{}

		var reaches func(string) bool

		reaches = func(n string) bool {
			if n == agent {
				return true
			}

			if seen[n] {
				return false
			}

			seen[n] = true

			return slices.ContainsFunc(c.fleet.Agents[n].Delegates, reaches)
		}

		if reaches(root) {
			return root
		}
	}

	return ""
}

func (c *compiler) remotes() {
	for _, name := range sortedKeys(c.fleet.Remotes) {
		r := c.fleet.Remotes[name]
		path := "remotes." + name

		c.checkName(path, "remote", name)

		if _, clash := c.fleet.Agents[name]; clash {
			c.errorf(path, "%q is both an agent and a remote", name)
		}

		for _, f := range [][2]string{{"owner", r.Owner}, {"name", r.Name}} {
			if err := natsagent.ValidateSubjectToken(f[1]); err != nil {
				c.errorf(path+"."+f[0], "%v", err)
			}
		}
	}
}

func (c *compiler) expose() {
	e := c.fleet.Expose
	if e == nil {
		return
	}

	if err := natsagent.ValidateSubjectToken(c.fleet.OwnerOrDefault()); err != nil {
		c.errorf("expose.owner", "%v", err)
	}

	c.refs("expose.agents", "agent", e.Agents, func(n string) bool { _, ok := c.fleet.Agents[n]; return ok })
	c.refs("expose.flows", "flow", e.Flows, c.flowKnown)

	// Agents and flows share the owner's protocol names.
	for _, f := range e.Flows {
		if slices.Contains(e.Agents, f) {
			c.errorf("expose.flows", "%q is exposed both as an agent and as a flow", f)
		}

		if err := natsagent.ValidateSubjectToken(f); err != nil {
			c.errorf("expose.flows", "flow %q: %v", f, err)
		}
	}
}

// modelRef checks a reference to a fleet model.
func (c *compiler) modelRef(path, model string, required bool) {
	switch _, ok := c.fleet.Models[model]; {
	case model == "" && required:
		c.errorf(path, "model is required")
	case model != "" && !ok:
		c.errorf(path, "unknown model %q", model)
	}
}

// refs checks a list of references: each must exist, none twice.
func (c *compiler) refs(path, what string, names []string, exists func(string) bool) {
	for _, n := range names {
		if !exists(n) {
			c.errorf(path, "unknown %s %q", what, n)
		}
	}

	if dup := firstDuplicate(names); dup != "" {
		c.errorf(path, "%s %q listed twice", what, dup)
	}
}

// session checks a long-term memory's session template. An exposed agent
// answers prompts outside any execution, so it has no job context to render
// a template over.
func (c *compiler) session(path, agent string, m spec.Memory) {
	if !m.IsLongTerm() || m.Session == "" {
		return
	}

	t, err := spec.ParseSession(m.Session)
	if err != nil {
		c.errorf(path, "%v", err)

		return
	}

	if !t.Static() && c.fleet.Expose != nil && slices.Contains(c.fleet.Expose.Agents, agent) {
		c.errorf(path, "an exposed agent has no job context: its session cannot be a template")
	}
}

func (c *compiler) memory(path string, m spec.Memory) {
	typ := m.TypeOrDefault()

	if _, ok := m.Options["summarize"]; ok {
		c.errorf(path+".summarize", "memory summarization was replaced by the agent's compaction")

		return
	}

	if !m.IsLongTerm() {
		if len(m.Options) > 0 || m.Session != "" || m.PerExecution || m.Knowledge != "" {
			c.errorf(path, "memory %q takes no options", typ)
		}

		return
	}

	if !c.cat.HasMemoryType(typ) {
		c.errorf(path+".type", "unknown memory type %q", typ)

		return
	}

	if m.Knowledge != "" {
		if _, ok := c.fleet.Knowledge[m.Knowledge]; !ok {
			c.errorf(path+".knowledge", "unknown knowledge %q", m.Knowledge)
		}

		// Accepting them would promise a memory per user or per execution
		// while every job reads and writes the same collection.
		if m.Session != "" || m.PerExecution {
			c.errorf(path, "a memory stored in knowledge is one collection shared by every job: "+
				"session and per_execution do not apply")
		}
	}

	if v, ok := c.cat.(MemoryValidator); ok {
		if err := v.ValidateMemory(m); err != nil {
			c.errorf(path, "%v", err)
		}
	}
}

// delegationCycles rejects agents that delegate to themselves through
// others: each would build the next forever.
func (c *compiler) delegationCycles() {
	const (
		unseen = iota
		visiting
		done
	)

	state := map[string]int{}

	var visit func(name string, stack []string)

	visit = func(name string, stack []string) {
		switch state[name] {
		case done:
			return
		case visiting:
			c.errorf("agents."+stack[0]+".delegates", "delegation cycle: %s",
				strings.Join(append(stack, name), " -> "))

			return
		}

		state[name] = visiting

		for _, d := range c.fleet.Agents[name].Delegates {
			if _, ok := c.fleet.Agents[d]; ok && d != name {
				visit(d, append(stack, name))
			}
		}

		state[name] = done
	}

	for _, name := range sortedKeys(c.fleet.Agents) {
		if state[name] == unseen {
			visit(name, nil)
		}
	}
}
