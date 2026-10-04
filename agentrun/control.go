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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/spec"
)

// Names of the tools that steer the workflow.
const (
	routeToolPrefix = "route_to_"
	askHumanTool    = "ask_human"
)

// errAskedHuman stops the agent run once it has asked a human: the step is
// interrupted until the answer arrives.
var errAskedHuman = errors.New("agentrun: waiting for a human")

// control holds what an agent decides about the workflow during one run:
// the next node it routes to and the question it asks a human.
//
// Routes are tools rather than phero handoffs: a phero handoff ends the run
// with the handoff tool's own result as the answer and drops the handoff
// context, while a route here is recorded and the agent
// still gives its answer.
type control struct {
	cancel context.CancelCauseFunc

	mu    sync.Mutex
	next  string
	asked string
}

type routeInput struct {
	Reason string `json:"reason" jsonschema:"Why the workflow should continue with this step."`
}

type askInput struct {
	Question string `json:"question" jsonschema:"The question for the human, with the context they need to answer."`
}

// tools returns the steering tools the step enables.
func (c *control) tools(s spec.Step) ([]*llm.Tool, error) {
	var tools []*llm.Tool

	for _, target := range s.DynamicTargets() {
		t, err := llm.NewTool(routeToolPrefix+agent.SanitizeToolName(target),
			fmt.Sprintf("Continue the workflow with step %q after your answer, instead of the default next step. "+
				"Call it at most once, then give your final answer.", target),
			func(_ context.Context, _ *routeInput) (string, error) {
				c.mu.Lock()
				c.next = target
				c.mu.Unlock()

				return fmt.Sprintf("Recorded: the workflow continues with %q. Now give your final answer.", target), nil
			})
		if err != nil {
			return nil, err
		}

		tools = append(tools, t)
	}

	if s.HasRoute(spec.RouteHuman) {
		t, err := llm.NewTool(askHumanTool,
			"Ask a human when you cannot continue without their decision or information. "+
				"Your work pauses until they answer.",
			func(_ context.Context, in *askInput) (string, error) {
				q := strings.TrimSpace(in.Question)
				if q == "" {
					return "", errors.New("question must not be empty")
				}

				c.mu.Lock()
				c.asked = q
				c.mu.Unlock()
				c.cancel(errAskedHuman)

				return "Your question was sent to a human.", nil
			})
		if err != nil {
			return nil, err
		}

		tools = append(tools, t)
	}

	return tools, nil
}

// instructions explains the steering tools in the system prompt.
func (c *control) instructions(s spec.Step) string {
	var b strings.Builder

	if len(s.DynamicTargets()) > 0 {
		b.WriteString("\n\nThis task is one step of a workflow. When you are done, the workflow moves on " +
			"to its default next step; to choose another step, call the matching " + routeToolPrefix +
			"tool before your final answer.")
	}

	if s.HasRoute(spec.RouteHuman) {
		b.WriteString("\n\nIf you cannot proceed without a human decision or information, call " +
			askHumanTool + " with your question.")
	}

	return b.String()
}

func (c *control) route() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.next
}

func (c *control) question() string {
	c.mu.Lock()
	defer c.mu.Unlock()

	return c.asked
}
