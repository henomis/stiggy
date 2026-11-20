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
