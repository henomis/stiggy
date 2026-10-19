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

	"github.com/henomis/phero/v2/trace"
)

// maxLogged bounds the model text, tool arguments and tool results a debug
// line carries.
const maxLogged = 512

// Log keys of the debug lines.
const (
	keyAgent     = "agent"
	keyIteration = "iteration"
	keySpanID    = "span_id"
	keyDuration  = "duration"
	keyStop      = "stop_reason"
)

type loggerKey struct{}

// withLogger returns ctx whose spans the log processor writes to l.
func withLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// logProcessor is a trace.Processor that logs each model call, tool call,
// memory access and agent run at debug level, with the agent that made it,
// through the logger of the step or request the span belongs to.
type logProcessor struct {
	mu      sync.Mutex
	loggers map[trace.SpanID]logged
}

type logged struct {
	ctx    context.Context //nolint:containedctx // OnEnd gets no context: the one OnStart got.
	logger *slog.Logger
}

// NewLogProcessor returns the processor that writes stiggy's debug lines
// ("stiggy: llm request", "stiggy: tool result", …). A provider given to
// WithTracing should include it, first; spans started outside a stiggy step
// or exposed agent are not logged.
func NewLogProcessor() trace.Processor {
	return &logProcessor{loggers: map[trace.SpanID]logged{}}
}

// defaultProvider is the provider of a handler given none: it only logs.
func defaultProvider() *trace.Provider {
	return trace.NewProvider(trace.WithProcessor(NewLogProcessor()))
}

// OnStart implements trace.Processor.
func (p *logProcessor) OnStart(ctx context.Context, s *trace.SpanData) {
	l, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	if l == nil || !l.Enabled(ctx, slog.LevelDebug) {
		return
	}

	switch s.Payload.(type) {
	case *trace.LLMCall, *trace.ToolCall, *trace.Memory, *trace.Run:
	default:
		return
	}

	lg := logged{ctx: ctx, logger: l}

	p.mu.Lock()
	p.loggers[s.Context.SpanID] = lg
	p.mu.Unlock()

	switch e := s.Payload.(type) {
	case *trace.LLMCall:
		lg.log(s, "stiggy: llm request", keyIteration, e.Iteration, "messages", e.MessageOffset+len(e.Messages),
			"tools", len(e.Tools))
	case *trace.ToolCall:
		lg.log(s, "stiggy: tool call", keyIteration, e.Iteration, "tool", e.Name, "call_id", e.CallID,
			"args", clip(e.Arguments))
	}
}

// OnEnd implements trace.Processor.
func (p *logProcessor) OnEnd(s *trace.SpanData) {
	p.mu.Lock()
	lg, ok := p.loggers[s.Context.SpanID]
	delete(p.loggers, s.Context.SpanID)
	p.mu.Unlock()

	if !ok {
		return
	}

	switch e := s.Payload.(type) {
	case *trace.LLMCall:
		model := e.ResponseModel
		if model == "" {
			model = e.RequestModel
		}

		attrs := []any{
			keyIteration, e.Iteration, "model", model, keyDuration, s.Duration(),
			"tokens_in", e.Usage.InputTokens, "tokens_out", e.Usage.OutputTokens,
		}
		if e.Response != nil {
			attrs = append(attrs, "tool_calls", len(e.Response.ToolCalls), "text", clip(e.Response.TextContent()))
		}

		lg.log(s, "stiggy: llm response", withErr(attrs, s.Err)...)
	case *trace.ToolCall:
		attrs := []any{keyIteration, e.Iteration, "tool", e.Name, "call_id", e.CallID, keyDuration, s.Duration()}
		if s.Err == nil {
			attrs = append(attrs, "result", clip(e.Result))
		}

		lg.log(s, "stiggy: tool result", withErr(attrs, s.Err)...)
	case *trace.Memory:
		msg := "stiggy: memory retrieved"
		if e.Op == trace.MemorySave {
			msg = "stiggy: memory saved"
		}

		lg.log(s, msg, "messages", e.Count)
	case *trace.Run:
		attrs := []any{"iterations", e.Iterations, keyStop, e.StopReason, keyDuration, s.Duration()}
		lg.log(s, "stiggy: agent run ended", withErr(attrs, s.Err)...)
	}
}

// Shutdown implements trace.Processor.
func (*logProcessor) Shutdown(context.Context) error { return nil }

func (lg logged) log(s *trace.SpanData, msg string, attrs ...any) {
	lg.logger.DebugContext(lg.ctx, msg, append([]any{keyAgent, s.Agent, keySpanID, s.Context.SpanID.String()},
		attrs...)...)
}

// withErr adds err, clipped like the text it may echo (a tool's error).
func withErr(attrs []any, err error) []any {
	if err != nil {
		return append(attrs, "err", clip(err.Error()))
	}

	return attrs
}

// clip shortens s for a log line.
func clip(s string) string {
	if len(s) <= maxLogged {
		return s
	}

	return s[:maxLogged] + "…"
}
