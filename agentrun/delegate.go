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

package agentrun

import (
	"fmt"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
)

// delegateToolPrefix starts the names of the tools that call delegates.
const delegateToolPrefix = "ask_"

// delegateTools builds one tool per delegate: a fresh phero agent (with its
// own tools and delegates, but no memory) exposed with agent.AsTool. Calls
// are not durable: if the step fails, they run again with it. A delegate's
// system prompt renders over the delegating job's context, data.
func delegateTools(delegates []Agent, data map[string]any) ([]*llm.Tool, error) {
	tools := make([]*llm.Tool, 0, len(delegates))

	for _, d := range delegates {
		system, err := SystemPrompt(d.Name, d.Spec, data)
		if err != nil {
			return nil, fmt.Errorf("delegate %q: %w", d.Name, err)
		}

		ag, err := agent.New(d.LLM, d.Name, system)
		if err != nil {
			return nil, fmt.Errorf("delegate %q: %w", d.Name, err)
		}

		if d.Spec.MaxIterations > 0 {
			ag.SetMaxIterations(d.Spec.MaxIterations)
		}

		sub, err := delegateTools(d.Delegates, data)
		if err != nil {
			return nil, err
		}

		for _, t := range append(append([]*llm.Tool{}, d.Tools...), sub...) {
			if err = ag.AddTool(t); err != nil {
				return nil, fmt.Errorf("delegate %q: %w", d.Name, err)
			}
		}

		desc := d.Spec.Description
		if desc == "" {
			desc = system
		}

		t, err := ag.AsTool(delegateToolPrefix+agent.SanitizeToolName(d.Name),
			"Ask agent "+d.Name+" to do a task and return its answer. "+desc)
		if err != nil {
			return nil, fmt.Errorf("delegate %q: %w", d.Name, err)
		}

		tools = append(tools, t)
	}

	return tools, nil
}
