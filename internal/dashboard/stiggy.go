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

package dashboard

import "time"

// What stiggy's agent steps write, as the dashboard reads it. These mirror
// package agentrun, which the dashboard does not import so its binary does
// not carry the model providers; stiggy_test.go keeps them in step.

// Usage counters of agent steps (agentrun.Counter*).
const (
	counterAgentSteps      = "agent_steps"
	counterTokensIn        = "tokens_in"
	counterTokensOut       = "tokens_out"
	counterTokensCacheRead = "tokens_cache_read" //nolint:gosec // a counter name, not a credential.
	counterTokensReasoning = "tokens_reasoning"  //nolint:gosec // a counter name, not a credential.
	counterCostUSD         = "cost_usd"
	counterLLMCalls        = "llm_calls"
	counterToolCalls       = "tool_calls"
	counterToolErrors      = "tool_errors"
	counterByAgent         = "__agent__"
	counterByModel         = "__model__"
)

// Progress is one streamed event of an agent step (agentrun.Progress).
type Progress struct {
	Type         string `json:"type"`
	Agent        string `json:"agent"`
	Node         string `json:"node"`
	Text         string `json:"text,omitempty"`
	Tool         string `json:"tool,omitempty"`
	Args         string `json:"args,omitempty"`
	IsError      bool   `json:"is_error,omitempty"`
	Trigger      string `json:"trigger,omitempty"`
	TokensBefore int    `json:"tokens_before,omitempty"`
	TokensAfter  int    `json:"tokens_after,omitempty"`
	Todos        []Todo `json:"todos,omitempty"`
}

// Todo is one entry of an agent's task list.
type Todo struct {
	Content string `json:"content"`
	Status  string `json:"status"`
}

// Record is what one attempt of an agent step did (agentrun.Record).
type Record struct {
	RecordRun

	Version      int            `json:"version"`
	Level        string         `json:"level"`
	OutputSchema map[string]any `json:"output_schema,omitempty"`
	Truncated    bool           `json:"truncated,omitempty"`
}

// RecordRun is one agent run of a record (agentrun.RecordRun).
type RecordRun struct {
	Agent           string        `json:"agent"`
	Via             string        `json:"via,omitempty"`
	Tool            string        `json:"tool,omitempty"`
	Model           string        `json:"model,omitempty"`
	TraceID         string        `json:"trace_id,omitempty"`
	SpanID          string        `json:"span_id,omitempty"`
	System          string        `json:"system,omitempty"`
	SystemTruncated bool          `json:"system_truncated,omitempty"`
	ResponseFormat  string        `json:"response_format,omitempty"`
	Tools           []string      `json:"tools,omitempty"`
	Output          string        `json:"output,omitempty"`
	OutputTruncated bool          `json:"output_truncated,omitempty"`
	Start           time.Time     `json:"start,omitzero"`
	End             time.Time     `json:"end,omitzero"`
	StopReason      string        `json:"stop_reason,omitempty"`
	Iterations      int           `json:"iterations,omitempty"`
	MaxIterations   int           `json:"max_iterations,omitempty"`
	TTFTMs          int64         `json:"ttft_ms,omitempty"`
	Memory          *RecordMemory `json:"memory,omitempty"`
	Usage           RecordUsage   `json:"usage"`
	Calls           []RecordCall  `json:"calls,omitempty"`
	Children        []RecordRun   `json:"children,omitempty"`
	Error           string        `json:"error,omitempty"`
}

// RecordMemory is what a run read from and wrote to memory
// (agentrun.RecordMemory).
type RecordMemory struct {
	Retrieved      int             `json:"retrieved"`
	Saved          int             `json:"saved"`
	RetrievedItems []RecordMessage `json:"retrieved_items,omitempty"`
	SavedItems     []RecordMessage `json:"saved_items,omitempty"`
}

// RecordUsage is the tokens and cost of a call or a run
// (agentrun.RecordUsage).
type RecordUsage struct {
	InputTokens      int     `json:"input_tokens"`
	OutputTokens     int     `json:"output_tokens"`
	CacheReadTokens  int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int     `json:"cache_write_tokens,omitempty"`
	ReasoningTokens  int     `json:"reasoning_tokens,omitempty"`
	CostUSD          float64 `json:"cost_usd,omitempty"`
}

// RecordCall is one model call (agentrun.RecordCall).
type RecordCall struct {
	Start             time.Time        `json:"start,omitzero"`
	End               time.Time        `json:"end,omitzero"`
	TTFTMs            int64            `json:"ttft_ms,omitempty"`
	Iteration         int              `json:"iteration,omitempty"`
	Compaction        bool             `json:"compaction,omitempty"`
	Model             string           `json:"model,omitempty"`
	FinishReason      string           `json:"finish_reason,omitempty"`
	Usage             RecordUsage      `json:"usage"`
	MessageOffset     int              `json:"message_offset"`
	Messages          []RecordMessage  `json:"messages,omitempty"`
	Response          string           `json:"response,omitempty"`
	ResponseTruncated bool             `json:"response_truncated,omitempty"`
	Error             string           `json:"error,omitempty"`
	ToolCalls         []RecordToolCall `json:"tool_calls,omitempty"`
}

// RecordMessage is one message of a prompt (agentrun.RecordMessage).
type RecordMessage struct {
	Role       string   `json:"role"`
	Name       string   `json:"name,omitempty"`
	Text       string   `json:"text,omitempty"`
	Reasoning  string   `json:"reasoning,omitempty"`
	ToolCalls  []string `json:"tool_calls,omitempty"`
	ToolCallID string   `json:"tool_call_id,omitempty"`
	ToolError  bool     `json:"tool_error,omitempty"`
	Truncated  bool     `json:"truncated,omitempty"`
}

// RecordToolCall is one tool call (agentrun.RecordToolCall).
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
