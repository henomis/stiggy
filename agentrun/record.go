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
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/spec"
)

// RecordVersion is the version of the record shape (Record.Version).
const RecordVersion = 1

// How a nested run was reached (RecordRun.Via).
const (
	// ViaDelegate is a run started by a tool call: a delegate.
	ViaDelegate = "delegate"
	// ViaAgent is any other run below the step's own.
	ViaAgent = "agent"
	// ViaRemote is the step's own run when a remote agent ran it: the
	// record has the prompt sent and the answer, not the remote's own calls.
	ViaRemote = "remote"
)

// Record is what one attempt of an agent step did, stored with the
// attempt's outcome (worker.Job.SetRecord) when the agent's record level is
// spec.RecordPrompt or spec.RecordFull. It is built from the spans of the
// attempt, for people to read: the dashboard shows it.
type Record struct {
	RecordRun

	// Version is RecordVersion when the record was written.
	Version int `json:"version"`
	// Level is the agent's record level, prompt or full.
	Level string `json:"level"`
	// OutputSchema is the step's output schema, when it has one.
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	// Truncated is set when the record was cut to the agent's
	// record_max_bytes: each cut item has its own truncated mark.
	Truncated bool `json:"truncated,omitempty"`
}

// RecordRun is one agent run: the step's own, or a nested one in Children.
type RecordRun struct {
	Agent string `json:"agent"`
	// Via and Tool say how a nested run was reached: by the delegate tool
	// Tool, or otherwise. Empty on the step's own run, but for a remote
	// agent's (ViaRemote).
	Via  string `json:"via,omitempty"`
	Tool string `json:"tool,omitempty"`
	// Remote is the owner/name of the remote agent that ran the step.
	Remote string `json:"remote,omitempty"`
	// Model is the model of the run's first call.
	Model   string `json:"model,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
	SpanID  string `json:"span_id,omitempty"`
	// System is the system prompt of the run's first call.
	System          string `json:"system,omitempty"`
	SystemTruncated bool   `json:"system_truncated,omitempty"`
	// ResponseFormat names the structured output format, when one is set.
	ResponseFormat string `json:"response_format,omitempty"`
	// Tools are the tools offered to the model, by name.
	Tools []string `json:"tools,omitempty"`
	// Output is the run's final answer.
	Output          string        `json:"output,omitempty"`
	OutputTruncated bool          `json:"output_truncated,omitempty"`
	Start           time.Time     `json:"start,omitzero"`
	End             time.Time     `json:"end,omitzero"`
	StopReason      string        `json:"stop_reason,omitempty"`
	Iterations      int           `json:"iterations,omitempty"`
	MaxIterations   int           `json:"max_iterations,omitempty"`
	TTFTMs          int64         `json:"ttft_ms,omitempty"`
	Memory          *RecordMemory `json:"memory,omitempty"`
	// Usage is what the run spent, nested runs included.
	Usage RecordUsage  `json:"usage"`
	Calls []RecordCall `json:"calls,omitempty"`
	// Children are the runs started below this one (full only).
	Children []RecordRun `json:"children,omitempty"`
	// Error is why the run failed; on the step's own run, why the attempt
	// failed.
	Error string `json:"error,omitempty"`
}

// RecordMemory is what a run read from and wrote to its memory.
type RecordMemory struct {
	Retrieved int `json:"retrieved"`
	Saved     int `json:"saved"`
	// RetrievedItems are the messages loaded; SavedItems those stored
	// (full only).
	RetrievedItems []RecordMessage `json:"retrieved_items,omitempty"`
	SavedItems     []RecordMessage `json:"saved_items,omitempty"`
}

// RecordUsage is the tokens and cost of a call or a run.
type RecordUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int     `json:"reasoning_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// RecordCall is one model call.
type RecordCall struct {
	Start     time.Time `json:"start,omitzero"`
	End       time.Time `json:"end,omitzero"`
	TTFTMs    int64     `json:"ttft_ms,omitempty"`
	Iteration int       `json:"iteration,omitempty"`
	// Compaction marks a call that summarized the conversation.
	Compaction   bool        `json:"compaction,omitempty"`
	Model        string      `json:"model,omitempty"`
	FinishReason string      `json:"finish_reason,omitempty"`
	Usage        RecordUsage `json:"usage"`
	// MessageOffset is the index of Messages[0] in the call's whole prompt:
	// a call lists only the messages added since the run's previous call
	// (the first call, its whole prompt but the system prompt). With
	// prompt, later calls leave out the model's own messages and the tool
	// results.
	MessageOffset int             `json:"message_offset"`
	Messages      []RecordMessage `json:"messages,omitempty"`
	// Response is the text the model answered. With prompt, only a final
	// answer (no tool calls) is kept.
	Response          string           `json:"response,omitempty"`
	ResponseTruncated bool             `json:"response_truncated,omitempty"`
	Error             string           `json:"error,omitempty"`
	ToolCalls         []RecordToolCall `json:"tool_calls,omitempty"`
}

// RecordMessage is one message of a prompt.
type RecordMessage struct {
	Role      string `json:"role"`
	Name      string `json:"name,omitempty"`
	Text      string `json:"text,omitempty"`
	Reasoning string `json:"reasoning,omitempty"`
	// ToolCalls name the tools an assistant message calls.
	ToolCalls  []string `json:"tool_calls,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	ToolError  bool     `json:"tool_error,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

// RecordToolCall is one tool call (full only).
type RecordToolCall struct {
	Tool      string    `json:"tool"`
	CallID    string    `json:"call_id,omitempty"`
	Args      string    `json:"args,omitempty"`
	Result    string    `json:"result,omitempty"`
	Error     string    `json:"error,omitempty"`
	Start     time.Time `json:"start,omitzero"`
	End       time.Time `json:"end,omitzero"`
	Truncated bool      `json:"truncated,omitempty"`
}

type recorderKey struct{}

// recorder collects the spans of one attempt, below its step span.
type recorder struct {
	level string
	max   int
	step  trace.SpanID

	mu     sync.Mutex
	spans  map[trace.SpanID]*trace.SpanData
	closed bool
}

// newRecorder returns the recorder of an attempt of an agent recording at
// its level, nil when it records nothing.
func newRecorder(a spec.Agent, step trace.SpanID) *recorder {
	level := a.RecordLevel()
	if level == spec.RecordOff {
		return nil
	}

	return &recorder{level: level, max: a.RecordLimit(), step: step, spans: map[trace.SpanID]*trace.SpanData{}}
}

func withRecorder(ctx context.Context, r *recorder) context.Context {
	if r == nil {
		return ctx
	}

	return context.WithValue(ctx, recorderKey{}, r)
}

func recorderFrom(ctx context.Context) *recorder {
	r, _ := ctx.Value(recorderKey{}).(*recorder)

	return r
}

// add keeps an ended span.
func (r *recorder) add(s *trace.SpanData) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.closed {
		r.spans[s.Context.SpanID] = s
	}
}

// write sets the attempt's record on job: its runs, and err, how the
// attempt ended. An attempt whose agent never ran records nothing. An
// encoding error is logged; the record never fails the step.
func (r *recorder) write(job *worker.Job, err error, schema map[string]any) {
	if r == nil {
		return
	}

	rec := r.build(err, schema)
	if rec == nil {
		return
	}

	if serr := job.SetRecord(rec); serr != nil {
		job.Logger().Warn("stiggy: attempt record not stored", "err", serr)
	}
}

// build makes the record from the spans kept so far; later spans are
// ignored. It returns nil when no agent run ended under the step.
func (r *recorder) build(err error, schema map[string]any) *Record {
	r.mu.Lock()
	r.closed = true
	spans := r.spans
	r.mu.Unlock()

	t := newSpanTree(spans, r.step)

	roots := t.runs[r.step]
	if len(roots) == 0 {
		return nil
	}

	full := r.level == spec.RecordFull
	top := t.run(roots[0], full)

	// More than one run straight under the step: the first is the step's,
	// the others are nested.
	if full {
		for _, s := range roots[1:] {
			top.Children = append(top.Children, t.run(s, full))
		}
	}

	if err != nil && !isInterrupt(err) {
		top.Error = err.Error()
	}

	rec := &Record{Version: RecordVersion, Level: r.level, RecordRun: top, OutputSchema: schema}
	rec.fit(r.max)

	return rec
}

func isInterrupt(err error) bool {
	var intr *worker.InterruptError

	return errors.As(err, &intr)
}

// spanTree indexes an attempt's spans by the run they belong to.
type spanTree struct {
	spans map[trace.SpanID]*trace.SpanData
	// runs, calls, tools and memory list the spans of each kind by their
	// nearest run (the step span for runs straight under it), by start.
	runs, calls, tools, memory map[trace.SpanID][]*trace.SpanData
}

func newSpanTree(spans map[trace.SpanID]*trace.SpanData, step trace.SpanID) *spanTree {
	t := &spanTree{
		spans: spans,
		runs:  map[trace.SpanID][]*trace.SpanData{}, calls: map[trace.SpanID][]*trace.SpanData{},
		tools: map[trace.SpanID][]*trace.SpanData{}, memory: map[trace.SpanID][]*trace.SpanData{},
	}

	for _, s := range spans {
		var into map[trace.SpanID][]*trace.SpanData

		switch s.Payload.(type) {
		case *trace.Run:
			into = t.runs
		case *trace.LLMCall:
			into = t.calls
		case *trace.ToolCall:
			into = t.tools
		case *trace.Memory:
			into = t.memory
		default:
			continue
		}

		if owner, ok := t.owner(s, step); ok {
			into[owner] = append(into[owner], s)
		}
	}

	for _, m := range []map[trace.SpanID][]*trace.SpanData{t.runs, t.calls, t.tools, t.memory} {
		for _, list := range m {
			slices.SortStableFunc(list, func(a, b *trace.SpanData) int { return a.Start.Compare(b.Start) })
		}
	}

	return t
}

// owner is the nearest run above s, or step when s is a run straight under
// the step span. A span whose chain leaves the attempt has none.
func (t *spanTree) owner(s *trace.SpanData, step trace.SpanID) (trace.SpanID, bool) {
	for id := s.Parent.SpanID; ; {
		if id == step {
			return step, true
		}

		p, ok := t.spans[id]
		if !ok {
			return trace.SpanID{}, false
		}

		if _, run := p.Payload.(*trace.Run); run {
			return id, true
		}

		id = p.Parent.SpanID
	}
}

// via says how the run s was reached: by the nearest tool call between it
// and the run above, or otherwise.
func (t *spanTree) via(s *trace.SpanData) (string, string) {
	for p := t.spans[s.Parent.SpanID]; p != nil; p = t.spans[p.Parent.SpanID] {
		switch e := p.Payload.(type) {
		case *trace.ToolCall:
			return ViaDelegate, e.Name
		case *trace.Run:
			return ViaAgent, ""
		}
	}

	return ViaAgent, ""
}

// underMemory reports whether a memory span lies between s and its run.
func (t *spanTree) underMemory(s *trace.SpanData) bool {
	for p := t.spans[s.Parent.SpanID]; p != nil; p = t.spans[p.Parent.SpanID] {
		switch p.Payload.(type) {
		case *trace.Memory:
			return true
		case *trace.Run:
			return false
		}
	}

	return false
}

// run records the run span s, its calls and, with full, its tool calls and
// nested runs.
func (t *spanTree) run(s *trace.SpanData, full bool) RecordRun {
	p, _ := s.Payload.(*trace.Run)
	id := s.Context.SpanID

	out := RecordRun{
		Agent: p.Agent, TraceID: s.Context.TraceID.String(), SpanID: id.String(),
		Output: partsText(p.Output), Start: s.Start, End: s.End, StopReason: string(p.StopReason),
		Iterations: p.Iterations, MaxIterations: p.MaxIterations, TTFTMs: p.TimeToFirstToken.Milliseconds(),
		Usage: usageRecord(s.Totals.Usage),
	}

	if s.Err != nil {
		out.Error = s.Err.Error()
	}

	out.Calls = t.runCalls(id, full, &out)
	out.Memory = t.runMemory(id, full)

	if full {
		for _, c := range t.runs[id] {
			child := t.run(c, full)
			child.Via, child.Tool = t.via(c)
			out.Children = append(out.Children, child)
		}
	}

	return out
}

// runCalls records the model calls of the run id, taking the run's system
// prompt, model, tools and response format from its first call.
func (t *spanTree) runCalls(id trace.SpanID, full bool, run *RecordRun) []RecordCall {
	calls := make([]RecordCall, 0, len(t.calls[id]))
	byIteration := map[int]int{}
	seenFirst := false

	for _, s := range t.calls[id] {
		e, _ := s.Payload.(*trace.LLMCall)
		compaction := t.underMemory(s)

		// The run's first call carries its whole prompt, recorded whole.
		first := !compaction && !seenFirst
		if first {
			seenFirst = true
			run.Model, run.ResponseFormat = cmp.Or(e.ResponseModel, e.RequestModel), e.ResponseFormat

			for _, d := range e.Tools {
				run.Tools = append(run.Tools, d.Name)
			}
		}

		if !compaction {
			if _, seen := byIteration[e.Iteration]; !seen {
				byIteration[e.Iteration] = len(calls)
			}
		}

		calls = append(calls, callRecord(s, e, compaction, first, full, run))
	}

	if full && len(calls) > 0 {
		t.attachTools(id, calls, byIteration)
	}

	return calls
}

// callRecord records one model call; the system prompt of the run's first
// call goes to run.
func callRecord(s *trace.SpanData, e *trace.LLMCall, compaction, first, full bool, run *RecordRun) RecordCall {
	c := RecordCall{
		Start: s.Start, End: s.End, Iteration: e.Iteration, Compaction: compaction,
		Model: cmp.Or(e.ResponseModel, e.RequestModel), FinishReason: string(e.FinishReason),
		Usage: callUsage(e), MessageOffset: e.MessageOffset,
	}

	if !e.FirstTokenAt.IsZero() {
		c.TTFTMs = e.FirstTokenAt.Sub(s.Start).Milliseconds()
	}

	if s.Err != nil {
		c.Error = s.Err.Error()
	}

	for _, m := range e.Messages {
		switch {
		case m.Role == llm.RoleSystem && first && run.System == "":
			run.System = m.TextContent()
		case m.Role == llm.RoleSystem && !compaction:
		case full, first, m.Role == llm.RoleUser:
			c.Messages = append(c.Messages, messageRecord(m))
		}
	}

	if r := e.Response; r != nil && (full || len(r.ToolCalls) == 0) {
		c.Response = r.TextContent()
	}

	return c
}

// attachTools adds the tool calls of the run id to the call of their
// iteration, or to the last call.
func (t *spanTree) attachTools(id trace.SpanID, calls []RecordCall, byIteration map[int]int) {
	for _, s := range t.tools[id] {
		e, _ := s.Payload.(*trace.ToolCall)

		tc := RecordToolCall{
			Tool: e.Name, CallID: e.CallID, Args: e.Arguments, Result: e.Result, Start: s.Start, End: s.End,
		}
		if s.Err != nil {
			tc.Error = s.Err.Error()
		}

		i, ok := byIteration[e.Iteration]
		if !ok {
			i = len(calls) - 1
		}

		calls[i].ToolCalls = append(calls[i].ToolCalls, tc)
	}
}

// runMemory records what the run id retrieved and, with full, saved.
func (t *spanTree) runMemory(id trace.SpanID, full bool) *RecordMemory {
	var m RecordMemory

	for _, s := range t.memory[id] {
		e, _ := s.Payload.(*trace.Memory)

		//nolint:exhaustive // compactions and snapshots are not reads or writes of the conversation.
		switch e.Op {
		case trace.MemoryRetrieve:
			m.Retrieved += e.Count
			for _, msg := range e.Messages {
				m.RetrievedItems = append(m.RetrievedItems, messageRecord(msg))
			}
		case trace.MemorySave:
			m.Saved += e.Count
			if full {
				for _, msg := range e.Messages {
					m.SavedItems = append(m.SavedItems, messageRecord(msg))
				}
			}
		}
	}

	if m.Retrieved == 0 && m.Saved == 0 {
		return nil
	}

	return &m
}

func messageRecord(m llm.Message) RecordMessage {
	r := RecordMessage{
		Role: m.Role, Name: m.Name, Text: partsText(m.Parts), Reasoning: m.ReasoningContent(),
		ToolCallID: m.ToolCallID, ToolError: m.ToolError,
	}

	for _, tc := range m.ToolCalls {
		r.ToolCalls = append(r.ToolCalls, tc.Function.Name)
	}

	return r
}

// partsText is the text of parts, with a placeholder for each image.
func partsText(parts []llm.ContentPart) string {
	var b strings.Builder

	for _, p := range parts {
		//nolint:exhaustive // reasoning is recorded apart, and redacted reasoning is not readable.
		switch p.Type {
		case llm.ContentTypeText:
			b.WriteString(p.Text)
		case llm.ContentTypeImageURL:
			b.WriteString("[image ")
			b.WriteString(p.ImageURL)
			b.WriteString("]")
		case llm.ContentTypeImageBase64:
			b.WriteString("[image ")
			b.WriteString(p.MIMEType)
			b.WriteString("]")
		}
	}

	return b.String()
}

func callUsage(e *trace.LLMCall) RecordUsage {
	u := e.Usage

	return RecordUsage{
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens, ReasoningTokens: u.ReasoningTokens, CostUSD: e.CostUSD,
	}
}

func usageRecord(u trace.UsageSummary) RecordUsage {
	return RecordUsage{
		InputTokens: u.InputTokens, OutputTokens: u.OutputTokens, CacheReadTokens: u.CacheReadTokens,
		CacheWriteTokens: u.CacheWriteTokens, ReasoningTokens: u.ReasoningTokens, CostUSD: u.CostUSD,
	}
}

// text is a text of a record that may be cut, and the mark of its item.
type text struct {
	s    *string
	mark *bool
}

// texts returns every text of the record that may be cut.
func (r *Record) texts() []text {
	var out []text

	var walk func(run *RecordRun)

	walk = func(run *RecordRun) {
		// An error has no mark of its own: the record's Truncated tells.
		out = append(out, text{&run.System, &run.SystemTruncated}, text{&run.Output, &run.OutputTruncated},
			text{&run.Error, nil})

		msgs := func(ms []RecordMessage) {
			for i := range ms {
				m := &ms[i]
				out = append(out, text{&m.Text, &m.Truncated}, text{&m.Reasoning, &m.Truncated})
			}
		}

		if run.Memory != nil {
			msgs(run.Memory.RetrievedItems)
			msgs(run.Memory.SavedItems)
		}

		for i := range run.Calls {
			c := &run.Calls[i]
			msgs(c.Messages)
			out = append(out, text{&c.Response, &c.ResponseTruncated}, text{&c.Error, nil})

			for j := range c.ToolCalls {
				tc := &c.ToolCalls[j]
				out = append(out, text{&tc.Args, &tc.Truncated}, text{&tc.Result, &tc.Truncated},
					text{&tc.Error, &tc.Truncated})
			}
		}

		for i := range run.Children {
			walk(&run.Children[i])
		}
	}

	walk(&r.RecordRun)

	return out
}

// fit cuts the record to at most limit encoded bytes: every text longer
// than the largest length that fits keeps its start and gets its item's
// truncated mark, so the largest items are cut first. When even empty
// texts do not fit, the calls, nested runs, memory, tool list and output
// schema go, and the texts left are cut again.
func (r *Record) fit(limit int) {
	if size(r) <= limit {
		return
	}

	r.Truncated = true

	if r.cutTexts(limit) {
		return
	}

	r.Calls, r.Children, r.Memory, r.Tools, r.OutputSchema = nil, nil, nil, nil, nil
	r.cutTexts(limit)
}

// cutTexts cuts every text of the record to the largest length at which
// the record fits in limit, and reports whether it fits.
func (r *Record) cutTexts(limit int) bool {
	texts := r.texts()

	orig := make([]string, len(texts))
	marked := make([]bool, len(texts))
	longest := 0

	for i, t := range texts {
		orig[i] = *t.s
		longest = max(longest, len(orig[i]))

		if t.mark != nil {
			marked[i] = *t.mark
		}
	}

	cut := func(n int) {
		for i, t := range texts {
			*t.s = cutText(orig[i], n)
			if t.mark != nil {
				*t.mark = marked[i]
			}
		}

		for i, t := range texts {
			if t.mark != nil && len(*t.s) < len(orig[i]) {
				*t.mark = true
			}
		}
	}

	// The largest per-text length whose record fits.
	lo, hi := 0, longest
	for lo < hi {
		mid := (lo + hi + 1) >> 1

		cut(mid)

		if size(r) <= limit {
			lo = mid
		} else {
			hi = mid - 1
		}
	}

	cut(lo)

	return size(r) <= limit
}

func size(r *Record) int {
	b, err := json.Marshal(r)
	if err != nil {
		return 0
	}

	return len(b)
}

// cutText returns the first n bytes of s, cut back to a rune boundary.
func cutText(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n]
}
