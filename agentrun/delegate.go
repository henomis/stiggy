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
	"sync"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"
)

// delegateToolPrefix starts the names of the tools that call delegates.
const delegateToolPrefix = "ask_"

// usageMeter adds up the summaries of every delegate run of one job, so
// tokens spent inside delegation reach the step's usage.
type usageMeter struct {
	mu    sync.Mutex
	usage map[string]float64
}

func (m *usageMeter) Trace(ev trace.Event) {
	e, ok := ev.(trace.AgentRunSummaryEvent)
	if !ok {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.usage == nil {
		m.usage = map[string]float64{}
	}

	for k, v := range usageOf(&e.Summary) {
		m.usage[k] += v
	}
}

func (m *usageMeter) total() map[string]float64 {
	m.mu.Lock()
	defer m.mu.Unlock()

	return m.usage
}

// delegateTools builds one tool per delegate: a fresh phero agent (with its
// own tools and delegates, but no memory) exposed with agent.AsTool. Calls
// are not durable: if the step fails, they run again with it.
func delegateTools(delegates []Agent, meter *usageMeter) ([]*llm.Tool, error) {
	tools := make([]*llm.Tool, 0, len(delegates))

	for _, d := range delegates {
		ag, err := agent.New(d.LLM, d.Name, SystemPrompt(d.Name, d.Spec))
		if err != nil {
			return nil, fmt.Errorf("delegate %q: %w", d.Name, err)
		}

		ag.SetTracer(meter)

		if d.Spec.MaxIterations > 0 {
			ag.SetMaxIterations(d.Spec.MaxIterations)
		}

		sub, err := delegateTools(d.Delegates, meter)
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
			desc = SystemPrompt(d.Name, d.Spec)
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

// addUsage adds b to a.
func addUsage(a, b map[string]float64) map[string]float64 {
	if len(b) == 0 {
		return a
	}

	out := make(map[string]float64, len(a)+len(b))

	for k, v := range a {
		out[k] += v
	}

	for k, v := range b {
		out[k] += v
	}

	return out
}
