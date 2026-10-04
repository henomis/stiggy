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
	"slices"
	"strings"

	natsagent "github.com/henomis/phero/v2/nats"

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

		c.memory(path+".memory", a.Memory)
	}

	c.delegationCycles()
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
	c.refs("expose.flows", "flow", e.Flows, func(n string) bool { _, ok := c.fleet.Flows[n]; return ok })

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

func (c *compiler) memory(path string, m spec.Memory) {
	typ := m.TypeOrDefault()

	if !m.IsLongTerm() {
		if len(m.Options) > 0 || m.Session != "" || m.PerExecution || m.Summarize != nil || m.Knowledge != "" {
			c.errorf(path, "memory %q takes no options", typ)
		}

		return
	}

	if !c.cat.HasMemoryType(typ) {
		c.errorf(path+".type", "unknown memory type %q", typ)

		return
	}

	if s := m.Summarize; s != nil {
		c.modelRef(path+".summarize.model", s.Model, true)

		if s.Threshold == 0 || s.Size == 0 || s.Size >= s.Threshold {
			c.errorf(path+".summarize", "threshold and size must be positive, with size below threshold")
		}
	}

	if m.Knowledge != "" {
		if _, ok := c.fleet.Knowledge[m.Knowledge]; !ok {
			c.errorf(path+".knowledge", "unknown knowledge %q", m.Knowledge)
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
