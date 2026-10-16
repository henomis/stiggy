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
	"strings"

	"github.com/henomis/phero/v2/agent"
)

// schemaTypes are the JSON Schema types of the field types.
var schemaTypes = map[string]string{
	FieldText:    "string",
	FieldNumber:  "number",
	FieldBoolean: "boolean",
	FieldList:    "array",
	FieldObject:  "object",
}

// model adds the model an AI block uses and returns its name.
func (w *wfc) model(loc Loc, cfg AgentConfig) string {
	cr, ok := w.c.creds[cfg.Credential]

	switch {
	case cfg.Credential == "":
		loc.Field = fieldCredential
		w.c.problem(loc, "choose a model provider (add one under Providers)")

		return ""
	case !ok:
		loc.Field = fieldCredential
		w.c.problem(loc, "the model provider no longer exists")

		return ""
	}

	m := omap{}.set("provider", cr.Provider).set("model", cfg.Model)
	if cr.Env != "" {
		m = m.set("api_key", "${"+cr.Env+"}")
	}

	m = m.set("base_url", cr.BaseURL)
	if cfg.Temperature != nil {
		m = append(m, kv{"temperature", *cfg.Temperature})
	}

	name, added := w.c.models.add(slug(cfg.Model, slug(cr.Name, "model")), m)
	if added {
		w.c.source("models."+name, Loc{Workflow: loc.Workflow, Node: loc.Node, Attachment: loc.Attachment})
	}

	return name
}

// agent adds the agent an AI block runs and returns its name. tools and
// knowledge are the names its attachments compiled to.
func (w *wfc) agent(n *Node, name string, cfg AgentConfig, tools, knowledge []string) string {
	loc := w.loc(n.ID, "")

	model := w.model(loc, cfg)

	system, err := w.template(cfg.Instructions)
	if err != nil {
		w.problem(n.ID, "instructions", "%s", err)
	}

	a := omap{}.
		set("model", model).
		set("system", system).
		set("tools", tools).
		set("knowledge", knowledge).
		set("max_iterations", cfg.MaxSteps)
	if cfg.Remember {
		a = a.set("memory", "execution")
	}

	agentName, added := w.c.agents.add(slug(name, "agent"), a)
	if added {
		w.c.source("agents."+agentName, loc)
	}

	return agentName
}

// outputSchema is the JSON Schema of a structured answer.
func outputSchema(fields []Field) omap {
	if len(fields) == 0 {
		return nil
	}

	props := omap{}
	required := make([]string, 0, len(fields))

	for _, f := range fields {
		t := f.Type
		if t == "" {
			t = FieldText
		}

		p := omap{}.set("type", schemaTypes[t])
		if t == FieldList {
			p = p.set("items", omap{}.set("type", "string"))
		}

		p = p.set("description", f.Description)
		props = append(props, kv{f.Name, p})
		required = append(required, f.Name)
	}

	return omap{}.set("type", "object").set("properties", props).set("required", required)
}

// step is the shared part of the AI blocks' flow node.
func (w *wfc) step(n *Node, cfg AgentConfig) omap {
	tools, knowledge := w.attachments(n)

	prompt, err := w.template(cfg.Prompt)
	if err != nil {
		w.problem(n.ID, "prompt", "%s", err)
	}

	for _, f := range cfg.Output {
		if slug(f.Name, "") != f.Name {
			w.problem(n.ID, "output", "field name %q: use lower-case letters, digits and _", f.Name)
		}
	}

	return omap{}.
		set("agent", w.agent(n, n.Name, cfg, tools, knowledge)).
		set("prompt", prompt).
		set("output_schema", outputSchema(cfg.Output))
}

func (w *wfc) agentBlock(n *Node) {
	var cfg AgentConfig
	if !w.params(n, &cfg) {
		return
	}

	node := omap{}.set("id", w.id(n)).set("type", "task")
	node = append(node, w.step(n, cfg)...)
	w.nodes = append(w.nodes, node.set("next", w.next(n)))
}

func (w *wfc) routerBlock(n *Node) {
	var rp RouterParams
	if !w.params(n, &rp) {
		return
	}

	var (
		routes []string
		lines  []string
	)

	for _, r := range rp.Routes {
		t := w.target(n, r.Port)
		if t == "" {
			w.problem(n.ID, "routes", "connect the %q route to a block", r.Port)

			continue
		}

		routes = append(routes, t)

		when := strings.TrimSpace(r.When)
		if when == "" {
			when = "it is the right next step (" + r.Port + ")"
		}

		lines = append(lines, "- call route_to_"+agent.SanitizeToolName(t)+" when "+when)
	}

	if len(rp.Routes) == 0 {
		w.problem(n.ID, "routes", "add at least one route")
	}

	if rp.AskHuman {
		routes = append(routes, "human")
	}

	otherwise := w.target(n, PortOtherwise)

	// Route tools only carry the target's name: the conditions go in the prompt.
	if len(lines) > 0 {
		guide := "\n\nWhen you are done, decide how the workflow continues:\n" + strings.Join(lines, "\n")
		if otherwise != "" {
			guide += "\n- otherwise, do not call a route tool"
		}

		rp.Prompt = strings.TrimRight(rp.Prompt, "\n") + guide
	}

	node := omap{}.set("id", w.id(n)).set("type", "task")
	node = append(node, w.step(n, rp.AgentConfig)...)
	node = node.set("routes", routes)
	w.nodes = append(w.nodes, node.set("next", otherwise))
}

func (w *wfc) loopBlock(n *Node) {
	var lp LoopParams
	if !w.params(n, &lp) {
		return
	}

	over := ""

	if r, ok := onlyRef(lp.Over); !ok {
		w.problem(n.ID, "over", "choose the list to loop over")
	} else if t, err := w.resolve(r); err != nil {
		w.problem(n.ID, "over", "%s", err)
	} else {
		over = t.expr
	}

	node := omap{}.set("id", w.id(n)).set("type", "map").set("over", over).set("max_parallel", lp.MaxParallel)
	node = append(node, w.step(n, lp.AgentConfig)...)
	w.nodes = append(w.nodes, node.set("next", w.next(n)))
}
