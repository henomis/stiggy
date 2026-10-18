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
	"log/slog"
	"slices"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
)

// exposed serves an agent on the NATS Agent Protocol.
type exposed struct {
	a        Agent
	sessions *sessionCache
	logger   *slog.Logger
}

// Exposed returns a protocol handler (for phero's nats.Server) that answers
// each prompt with a fresh run of a: its tools, knowledge and delegates, and
// its long-term memory when it has one (one session for all callers; a
// per-execution memory has no execution here and uses that same session).
// Execution memory, routes and human input belong to flow steps and do not
// apply. Of the options, only WithLogger applies.
func Exposed(a Agent, opts ...Option) natsagent.Handler {
	r := &runner{}
	for _, opt := range opts {
		opt(r)
	}

	e := &exposed{a: a, logger: r.logger}
	if e.logger == nil {
		e.logger = slog.Default()
	}

	if a.Memory != nil {
		e.sessions = &sessionCache{build: a.Memory}
	}

	return e
}

// Run implements natsagent.Handler.
func (e *exposed) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	// An exposed agent has no job context: the compiler keeps its system
	// prompt and its delegates' static.
	system, err := SystemPrompt(e.a.Name, e.a.Spec, nil)
	if err != nil {
		return nil, err
	}

	ag, err := agent.New(e.a.LLM, e.a.Name, system)
	if err != nil {
		return nil, fmt.Errorf("agent %q: %w", e.a.Name, err)
	}

	logger := e.logger
	ag.SetTracer(newRunTracer(ctx, logger))

	delegates, err := delegateTools(e.a.Delegates, nil, newRunTracer(ctx, logger))
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

		// An exposed agent has no job context: the compiler only lets a
		// static session through.
		session, serr := SessionOf(e.a.Name, mem, nil, "")
		if serr != nil {
			return nil, fmt.Errorf("agent %q: memory: %w", e.a.Name, serr)
		}

		m, merr := e.sessions.get(ctx, session)
		if merr != nil {
			return nil, fmt.Errorf("agent %q: memory: %w", e.a.Name, merr)
		}

		ag.SetMemory(m)
	}

	return ag.Run(ctx, parts...)
}
