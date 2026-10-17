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
	"errors"
	"time"

	phtrace "github.com/henomis/phero/v2/trace"
)

// Bounds of a step summary: it is stored with the step's event.
const (
	maxSummaryTools = 32
	maxSummaryError = 512
)

// StepSummary is what an agent step did: recorded on the attempt's event
// (packtrail's Result.Summary) and shown by `stiggy timeline`. It holds
// counts and timings only, never prompts, answers or tool arguments.
type StepSummary struct {
	Agent      string        `json:"agent"`
	Model      string        `json:"model,omitempty"`
	Iterations int           `json:"iterations,omitempty"`
	LLMCalls   int           `json:"llm_calls,omitempty"`
	ToolCalls  int           `json:"tool_calls,omitempty"`
	ToolErrors int           `json:"tool_errors,omitempty"`
	Tools      []ToolSummary `json:"tools,omitempty"`
	TokensIn   int           `json:"tokens_in,omitempty"`
	TokensOut  int           `json:"tokens_out,omitempty"`
	CostUSD    float64       `json:"cost_usd,omitempty"`
	// LatencyMS splits the run's time, in milliseconds.
	LatencyMS LatencySummary `json:"latency_ms"`
	// Delegates is what each delegate spent, by agent name.
	Delegates map[string]DelegateSummary `json:"delegates,omitempty"`
	// Route is the successor the agent chose; AskedHuman reports a question
	// to a human (the step then waits for the answer).
	Route      string `json:"route,omitempty"`
	AskedHuman bool   `json:"asked_human,omitempty"`
	Error      string `json:"error,omitempty"`
}

// ToolSummary counts the calls of one tool.
type ToolSummary struct {
	Name   string `json:"name"`
	Calls  int    `json:"calls"`
	Errors int    `json:"errors,omitempty"`
}

// LatencySummary splits a run's time in milliseconds.
type LatencySummary struct {
	Total  float64 `json:"total"`
	LLM    float64 `json:"llm,omitempty"`
	Tool   float64 `json:"tool,omitempty"`
	Memory float64 `json:"memory,omitempty"`
}

// DelegateSummary is what one delegate spent within a step.
type DelegateSummary struct {
	Runs      int     `json:"runs"`
	TokensIn  int     `json:"tokens_in,omitempty"`
	TokensOut int     `json:"tokens_out,omitempty"`
	CostUSD   float64 `json:"cost_usd,omitempty"`
}

// newStepSummary builds the summary of a step run by agentName: s is its
// own run's summary (nil when it never started), model the model that last
// answered it, delegated what its delegates spent.
func newStepSummary(agentName, model string, s *phtrace.RunSummary, delegated map[string]DelegateSummary,
	route string, asked bool, err error,
) *StepSummary {
	out := &StepSummary{Agent: agentName, Model: model, Delegates: delegated, Route: route, AskedHuman: asked}

	if s != nil {
		out.Iterations, out.LLMCalls, out.ToolCalls, out.ToolErrors = s.Iterations, s.LLMCalls, s.ToolCalls,
			s.ToolErrors
		out.TokensIn, out.TokensOut, out.CostUSD = s.Usage.InputTokens, s.Usage.OutputTokens, s.Usage.CostUSD
		out.LatencyMS = LatencySummary{
			Total: millis(s.Latency.Total), LLM: millis(s.Latency.LLM), Tool: millis(s.Latency.Tool),
			Memory: millis(s.Latency.Memory),
		}

		for _, t := range s.Tools[:min(len(s.Tools), maxSummaryTools)] {
			out.Tools = append(out.Tools, ToolSummary{Name: t.ToolName, Calls: t.Calls, Errors: t.Errors})
		}
	}

	if err != nil {
		out.Error = summaryError(err)
	}

	return out
}

// summaryError is how a summary names err: an answer that is not a JSON
// object by its sentinel alone (the error quotes the answer, and a summary
// holds no content), anything else cut to maxSummaryError bytes.
func summaryError(err error) string {
	if errors.Is(err, ErrNotJSONObject) {
		return ErrNotJSONObject.Error()
	}

	msg := err.Error()
	if len(msg) > maxSummaryError {
		msg = msg[:maxSummaryError] + "…"
	}

	return msg
}

func millis(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 } //nolint:mnd // µs to ms.
