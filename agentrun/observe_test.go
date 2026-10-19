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
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/internal/report"
)

func TestUsageOfTotals(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := usageOf(&trace.Totals{
		AgentRuns: 2, LLMCalls: 3, ToolCalls: 1,
		Usage: trace.UsageSummary{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 6, ReasoningTokens: 2, CostUSD: 0.5},
		ByAgent: map[string]trace.AgentTotals{
			"lead": {Usage: trace.UsageSummary{InputTokens: 7, OutputTokens: 3}},
			long:   {Usage: trace.UsageSummary{InputTokens: 3}},
		},
		ByModel: map[string]trace.ModelTotals{"gpt-4.1/mini": {Usage: trace.UsageSummary{InputTokens: 10, CostUSD: 0.5}}},
	})

	for k, want := range map[string]float64{
		CounterAgentSteps: 2, CounterTokensIn: 10, CounterTokensOut: 4, CounterTokensCacheRead: 6,
		CounterTokensReasoning: 2, CounterCostUSD: 0.5, CounterLLMCalls: 3, CounterToolCalls: 1,
		"tokens_in__agent__lead": 7, "tokens_out__agent__lead": 3,
		"tokens_in__model__gpt-4_1_mini": 10, "cost_usd__model__gpt-4_1_mini": 0.5,
	} {
		if got[k] != want {
			t.Errorf("%s = %v, want %v", k, got[k], want)
		}
	}

	for _, k := range []string{CounterTokensCacheWrite, CounterToolErrors, "tokens_out__agent__" + long[:10]} {
		if _, ok := got[k]; ok {
			t.Errorf("zero counter %s reported", k)
		}
	}

	for k := range got {
		if len(k) > maxCounterName {
			t.Errorf("counter name longer than %d: %s", maxCounterName, k)
		}
	}

	// A run that never started still counts one step.
	if u := usageOf(&trace.Totals{}); u[CounterAgentSteps] != 1 {
		t.Errorf("no run: %v", u)
	}
}

// TestStopReasonWithoutSummary checks the stop reason of a run that
// returned no summary is classified as phero classifies it.
func TestStopReasonWithoutSummary(t *testing.T) {
	for err, want := range map[error]trace.StopReason{
		fmt.Errorf("x: %w", llm.ErrGuardrailBlocked): trace.StopGuardrail,
		context.Canceled:              trace.StopCancelled,
		agent.StopRun(errAskedHuman):  trace.StopRequested,
		agent.ErrMaxIterationsReached: trace.StopMaxIterations,
		errNoResult:                   trace.StopError,
	} {
		ctx, rep := report.With(t.Context())
		_, span := trace.Start(ctx, "step", nil)
		observe(ctx, span).run(nil, err, &trace.Totals{})

		var got string

		for _, a := range rep.Attrs() {
			if a.Key == "stop_reason" {
				got = a.Value.String()
			}
		}

		if got != string(want) {
			t.Errorf("%v: stop_reason = %q, want %q", err, got, want)
		}
	}
}

// TestRemoteCode checks a remote agent's machine-readable error code reaches
// the step's error text.
func TestRemoteCode(t *testing.T) {
	err := fmt.Errorf("send: %w", &natsagent.ServiceError{Code: 429, Description: "slow down", ErrCode: "rate_limited"})
	if got := remoteCode(err); got != " (rate_limited)" {
		t.Errorf("remoteCode = %q", got)
	}

	if got := remoteCode(errors.New("plain")); got != "" {
		t.Errorf("remoteCode of a plain error = %q", got)
	}
}

func TestReviewOutcome(t *testing.T) {
	for _, tc := range []struct {
		kind, resume, want string
	}{
		{kindQuestion, `"yes"`, reviewAnswered},
		{kindReview, `{"approve": true}`, reviewApproved},
		{kindReview, `{"feedback": "shorter"}`, reviewFeedback},
	} {
		payload, err := json.Marshal(Interrupt{Kind: tc.kind, Stiggy: record{Pending: &pending{Kind: tc.kind}}})
		if err != nil {
			t.Fatal(err)
		}

		job := &worker.Job{Context: worker.Context{
			Resume:    json.RawMessage(tc.resume),
			Interrupt: payload,
		}}

		h, err := openHITL(job)
		if err != nil {
			t.Fatal(err)
		}

		if h.replied == nil || h.outcome != tc.want {
			t.Errorf("%s %s: outcome = %q, want %q", tc.kind, tc.resume, h.outcome, tc.want)
		}
	}
}
