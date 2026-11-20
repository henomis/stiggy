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

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/henomis/phero/v2/tool/todo"

	"github.com/henomis/stiggy/agentrun"
)

// TestMirrorsAgentrun keeps the dashboard's copy of what agent steps write in
// step with agentrun.
func TestMirrorsAgentrun(t *testing.T) {
	for mine, theirs := range map[string]string{
		counterAgentSteps:      agentrun.CounterAgentSteps,
		counterTokensIn:        agentrun.CounterTokensIn,
		counterTokensOut:       agentrun.CounterTokensOut,
		counterTokensCacheRead: agentrun.CounterTokensCacheRead,
		counterTokensReasoning: agentrun.CounterTokensReasoning,
		counterCostUSD:         agentrun.CounterCostUSD,
		counterLLMCalls:        agentrun.CounterLLMCalls,
		counterToolCalls:       agentrun.CounterToolCalls,
		counterToolErrors:      agentrun.CounterToolErrors,
		counterByAgent:         agentrun.CounterByAgent,
		counterByModel:         agentrun.CounterByModel,
	} {
		if mine != theirs {
			t.Errorf("counter %q, agentrun says %q", mine, theirs)
		}
	}

	in := agentrun.Progress{
		Type: "todo", Agent: "a", Node: "n", Text: "t", Tool: "x", Args: "{}", IsError: true,
		Trigger: "threshold", TokensBefore: 10, TokensAfter: 5,
		Todos: []todo.Item{{Content: "do it", Status: "in_progress"}},
	}

	sameJSON(t, "progress", in, &Progress{})

	// Every field of a record set, so a field the dashboard misses shows.
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	usage := agentrun.RecordUsage{
		InputTokens: 1, OutputTokens: 2, CacheReadTokens: 3, CacheWriteTokens: 4, ReasoningTokens: 5, CostUSD: 0.5,
	}
	msg := agentrun.RecordMessage{
		Role: "assistant", Name: "n", Text: "t", Reasoning: "r", ToolCalls: []string{"x"}, ToolCallID: "c",
		ToolError: true, Truncated: true,
	}
	run := agentrun.RecordRun{
		Agent: "a", Via: "delegate", Tool: "ask_a", Remote: "o/n", Model: "m", TraceID: "t", SpanID: "s", System: "sys",
		SystemTruncated: true, ResponseFormat: "f", Tools: []string{"x"}, Output: "o", OutputTruncated: true,
		Start: at, End: at, StopReason: "final", Iterations: 1, MaxIterations: 2, TTFTMs: 3,
		Memory: &agentrun.RecordMemory{
			Retrieved: 1, Saved: 1, RetrievedItems: []agentrun.RecordMessage{msg}, SavedItems: []agentrun.RecordMessage{msg},
		},
		Usage: usage,
		Calls: []agentrun.RecordCall{{
			Start: at, End: at, TTFTMs: 1, Iteration: 1, Compaction: true, Model: "m", FinishReason: "stop",
			Usage: usage, MessageOffset: 1, Messages: []agentrun.RecordMessage{msg}, Response: "r",
			ResponseTruncated: true, Error: "e",
			ToolCalls: []agentrun.RecordToolCall{{
				Tool: "x", CallID: "c", Args: "{}", Result: "r", Error: "e", Start: at, End: at, Truncated: true,
			}},
		}},
		Error: "e",
	}
	top := run
	top.Children = []agentrun.RecordRun{run}
	rec := agentrun.Record{
		Version: agentrun.RecordVersion, Level: "full", RecordRun: top,
		OutputSchema: map[string]any{"type": "object"}, Truncated: true,
	}

	sameJSON(t, "record", rec, &Record{})
}

// sameJSON checks in, encoded and decoded into out, encodes the same.
func sameJSON(t *testing.T, what string, in, out any) {
	t.Helper()

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	if err = json.Unmarshal(b, out); err != nil {
		t.Fatal(err)
	}

	back, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}

	var a, c map[string]any

	if err = json.Unmarshal(b, &a); err != nil {
		t.Fatal(err)
	}

	if err = json.Unmarshal(back, &c); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(a, c) {
		t.Fatalf("%s differs:\nagentrun  %s\ndashboard %s", what, b, back)
	}
}
