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
	"log/slog"
	"sync"
	"time"

	"github.com/henomis/phero/v2/trace"
)

// maxLogged bounds the model text, tool arguments and tool results a debug
// line carries.
const maxLogged = 512

// Log keys of the debug lines.
const (
	keyAgent     = "agent"
	keyIteration = "iteration"
)

// runTracer receives the trace events of phero runs. It keeps their
// summaries, which become the step's usage, and logs each model call, tool
// call and memory access at debug level, with the agent that made it.
//
// phero's Tracer gets no context, so a runTracer is built per run around the
// run's: the job's, or the protocol request's for an exposed agent.
type runTracer struct {
	ctx    context.Context //nolint:containedctx // phero's Tracer takes no context.
	logger *slog.Logger

	mu    sync.Mutex
	last  *trace.RunSummary
	total map[string]float64
	llmAt map[llmCall]time.Time
	tool  map[string]time.Time
}

type llmCall struct {
	agent     string
	iteration int
}

func newRunTracer(ctx context.Context, logger *slog.Logger) *runTracer {
	if logger == nil {
		logger = slog.Default()
	}

	return &runTracer{
		ctx: ctx, logger: logger, llmAt: map[llmCall]time.Time{}, tool: map[string]time.Time{},
	}
}

// Trace implements trace.Tracer. Tools may run in parallel: it locks.
func (t *runTracer) Trace(ev trace.Event) {
	if e, ok := ev.(trace.AgentRunSummaryEvent); ok {
		t.mu.Lock()
		defer t.mu.Unlock()

		t.last = &e.Summary

		if t.total == nil {
			t.total = map[string]float64{}
		}

		for k, v := range usageOf(&e.Summary) {
			t.total[k] += v
		}

		return
	}

	if !t.logger.Enabled(t.ctx, slog.LevelDebug) {
		return
	}

	t.debug(ev)
}

//nolint:gocyclo // one case per event type.
func (t *runTracer) debug(ev trace.Event) {
	switch e := ev.(type) {
	case trace.LLMRequestEvent:
		t.mu.Lock()
		t.llmAt[llmCall{e.AgentName, e.Iteration}] = e.Timestamp
		t.mu.Unlock()

		t.log("stiggy: llm request", keyAgent, e.AgentName, keyIteration, e.Iteration, "messages", e.MessageCount,
			"tools", len(e.ToolNames))
	case trace.LLMResponseEvent:
		t.mu.Lock()
		at, ok := t.llmAt[llmCall{e.AgentName, e.Iteration}]
		delete(t.llmAt, llmCall{e.AgentName, e.Iteration})
		t.mu.Unlock()

		attrs := []any{keyAgent, e.AgentName, keyIteration, e.Iteration, "model", e.Model}
		if ok {
			attrs = append(attrs, "duration", e.Timestamp.Sub(at))
		}

		if e.Usage != nil {
			attrs = append(attrs, "tokens_in", e.Usage.InputTokens, "tokens_out", e.Usage.OutputTokens)
		}

		if e.Message != nil {
			attrs = append(attrs, "tool_calls", len(e.Message.ToolCalls), "text", clip(e.Message.TextContent()))
		}

		t.log("stiggy: llm response", attrs...)
	case trace.ToolCallEvent:
		t.mu.Lock()
		t.tool[e.CallID] = e.Timestamp
		t.mu.Unlock()

		t.log("stiggy: tool call", keyAgent, e.AgentName, keyIteration, e.Iteration, "tool", e.ToolName,
			"call_id", e.CallID, "args", clip(e.Arguments))
	case trace.ToolResultEvent:
		t.mu.Lock()
		at, ok := t.tool[e.CallID]
		delete(t.tool, e.CallID)
		t.mu.Unlock()

		attrs := []any{keyAgent, e.AgentName, keyIteration, e.Iteration, "tool", e.ToolName, "call_id", e.CallID}
		if ok {
			attrs = append(attrs, "duration", e.Timestamp.Sub(at))
		}

		if e.Err != nil {
			attrs = append(attrs, "err", e.Err)
		} else {
			attrs = append(attrs, "result", clip(e.Result))
		}

		t.log("stiggy: tool result", attrs...)
	case trace.MemoryRetrieveEvent:
		t.log("stiggy: memory retrieved", keyAgent, e.AgentName, "messages", e.Count)
	case trace.MemorySaveEvent:
		t.log("stiggy: memory saved", keyAgent, e.AgentName, "messages", e.Count)
	case trace.AgentEndEvent:
		attrs := []any{keyAgent, e.AgentName, "iterations", e.Iterations}
		if e.Err != nil {
			attrs = append(attrs, "err", e.Err)
		}

		t.log("stiggy: agent run ended", attrs...)
	}
}

func (t *runTracer) log(msg string, attrs ...any) {
	t.logger.DebugContext(t.ctx, msg, attrs...)
}

// summary is the last run's summary (the step agent's own run), nil when
// it never started.
func (t *runTracer) summary() *trace.RunSummary {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.last
}

// usage adds up every run traced (a step's delegates), nil when none ran.
func (t *runTracer) usage() map[string]float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	return t.total
}

// clip shortens s for a log line.
func clip(s string) string {
	if len(s) <= maxLogged {
		return s
	}

	return s[:maxLogged] + "…"
}
