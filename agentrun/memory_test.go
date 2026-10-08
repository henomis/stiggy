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
	"testing"

	"github.com/henomis/phero/v2/llm"
)

// recMem records what reaches the wrapped memory.
type recMem struct{ saved []llm.Message }

func (m *recMem) Save(_ context.Context, msgs []llm.Message) error {
	m.saved = append(m.saved, msgs...)

	return nil
}

func (m *recMem) Retrieve(context.Context, string) ([]llm.Message, error) { return m.saved, nil }

func (m *recMem) Clear(context.Context) error { return nil }

func TestReplayableDropsEmptyAnswers(t *testing.T) {
	empty := llm.Message{Role: llm.RoleAssistant}
	blank := llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{llm.Text(" \n")}}
	call := llm.Message{Role: llm.RoleAssistant, ToolCalls: []llm.ToolCall{{ID: "1"}}}
	answer := llm.Message{Role: llm.RoleAssistant, Parts: []llm.ContentPart{llm.Text("hi")}}
	user := llm.Message{Role: llm.RoleUser}

	inner := &recMem{}
	m := replayable{inner}

	if err := m.Save(t.Context(), []llm.Message{user, call, empty}); err != nil {
		t.Fatal(err)
	}

	if err := m.Save(t.Context(), []llm.Message{blank}); err != nil {
		t.Fatal(err)
	}

	if len(inner.saved) != 2 {
		t.Fatalf("saved %d messages, want the user turn and the tool call", len(inner.saved))
	}

	// A session stored before the filter still replays without them.
	inner.saved = []llm.Message{user, empty, answer, blank}

	got, err := m.Retrieve(t.Context(), "")
	if err != nil || len(got) != 2 || got[1].TextContent() != "hi" {
		t.Fatalf("retrieved %v, %v; want the user turn and the answer", got, err)
	}
}
