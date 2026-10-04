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
	"fmt"
	"slices"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
)

// exposed serves an agent on the NATS Agent Protocol.
type exposed struct {
	a        Agent
	sessions *sessionCache
}

// Exposed returns a protocol handler (for phero's nats.Server) that answers
// each prompt with a fresh run of a: its tools, knowledge and delegates, and
// its long-term memory when it has one (one session for all callers; a
// per-execution memory has no execution here and uses that same session).
// Execution memory, routes and human input belong to flow steps and do not
// apply.
func Exposed(a Agent) natsagent.Handler {
	e := &exposed{a: a}
	if a.Memory != nil {
		e.sessions = &sessionCache{build: a.Memory}
	}

	return e
}

// Run implements natsagent.Handler.
func (e *exposed) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	ag, err := agent.New(e.a.LLM, e.a.Name, SystemPrompt(e.a.Name, e.a.Spec))
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
	}

	delegates, err := delegateTools(e.a.Delegates, &usageMeter{})
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
	}

	for _, t := range slices.Concat(e.a.Tools, delegates) {
		if err = ag.AddTool(t); err != nil {
			return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
		}
	}

	if e.a.Spec.MaxIterations > 0 {
		ag.SetMaxIterations(e.a.Spec.MaxIterations)
	}

	if e.sessions != nil {
		mem := e.a.Spec.Memory
		mem.PerExecution = false

		m, merr := e.sessions.get(ctx, SessionOf(e.a.Name, mem, ""))
		if merr != nil {
			return nil, fmt.Errorf("agent %q: memory: %w", e.a.Name, merr)
		}

		ag.SetMemory(m)
	}

	return ag.Run(ctx, parts...)
}
