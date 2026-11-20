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

	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}

	var out Progress
	if err = json.Unmarshal(b, &out); err != nil {
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
		t.Fatalf("progress differs:\nagentrun  %s\ndashboard %s", b, back)
	}
}
