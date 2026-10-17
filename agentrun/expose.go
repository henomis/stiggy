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
	"strings"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
	"go.opentelemetry.io/otel/propagation"
)

// exposed serves an agent on the NATS Agent Protocol.
type exposed struct {
	a        Agent
	sessions *sessionCache
	logger   *slog.Logger
	tel      *Telemetry
}

// Exposed returns a protocol handler (for phero's nats.Server) that answers
// each prompt with a fresh run of a: its tools, knowledge and delegates, and
// its long-term memory when it has one (one session for all callers; a
// per-execution memory has no execution here and uses that same session).
// Execution memory, routes and human input belong to flow steps and do not
// apply. Of the options, WithLogger and WithTelemetry apply. A run continues
// the trace of a traceparent header the caller sent.
func Exposed(a Agent, opts ...Option) natsagent.Handler {
	r := &runner{}
	for _, opt := range opts {
		opt(r)
	}

	e := &exposed{a: a, logger: r.logger, tel: r.tel}
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

	ctx = RequestTraceContext(ctx)
	tr := newRunTracer(ctx, e.logger, e.tel, modeExposed, nil)
	delegated := newRunTracer(ctx, e.logger, e.tel, modeDelegate, tr)

	defer tr.finish()
	defer delegated.finish()

	ag.SetTracer(tr)

	delegates, err := delegateTools(e.a.Delegates, nil, delegated)
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

// RequestTraceContext returns ctx continuing the trace of the protocol
// request it serves: the W3C traceparent header the caller sent, if any.
func RequestTraceContext(ctx context.Context) context.Context {
	req, ok := natsagent.RequestFrom(ctx)
	if !ok || req.Header == nil {
		return ctx
	}

	// Header names are matched without case: an HTTP-minded caller may send
	// "Traceparent".
	carrier := propagation.MapCarrier{}

	for k, v := range req.Header {
		if lk := strings.ToLower(k); (lk == "traceparent" || lk == "tracestate") && len(v) > 0 {
			carrier[lk] = v[0]
		}
	}

	if carrier["traceparent"] == "" {
		return ctx
	}

	return propagation.TraceContext{}.Extract(ctx, carrier)
}
