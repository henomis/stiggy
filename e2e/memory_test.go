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

package e2e_test

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
	"testing"
)

// secret returns a value no model can guess.
func secret(prefix string) string {
	return fmt.Sprintf("%s-%04d", prefix, rand.IntN(10000)) //nolint:gosec // not a security secret
}

const vaultFleet = `
namespace: e2e-vault
agents:
  keeper: {model: main, role: Vault keeper, goal: Keep track of what you are told and answer briefly., memory: execution}
  stranger: {model: main, role: Vault keeper, goal: Keep track of what you are told and answer briefly.}
flows:
  vault:
    channels: {answer: {}}
    nodes:
      - id: store
        type: task
        agent: keeper
        prompt: "Remember this: the vault codeword is {{.input.codeword}}. Reply only with the word stored."
        next: recall
      - id: recall
        type: task
        agent: keeper
        prompt: "What is the vault codeword? Answer with the codeword only, or UNKNOWN if you were never told."
        writes: {answer: output.text}
  forgetful:
    nodes:
      - id: store
        type: task
        agent: stranger
        prompt: "Remember this: the vault codeword is {{.input.codeword}}. Reply only with the word stored."
        next: recall
      - id: recall
        type: task
        agent: stranger
        prompt: "What is the vault codeword? Answer with the codeword only, or UNKNOWN if you were never told."
`

// TestExecutionMemory checks a real model remembers an earlier step of the
// same execution through execution memory (the recall prompt does not carry
// the codeword), that a rerun of the recall step remembers it too, since the
// memory lives in the execution's history, and that the same flow without
// memory cannot know it.
func TestExecutionMemory(t *testing.T) {
	h := start(t, vaultFleet)
	codeword := secret("ORCHID")

	st := run(t, h.Client, "vault", map[string]any{"codeword": codeword})
	mustContain(t, "recall", result(t, st, "recall"), codeword)

	var out map[string]json.RawMessage
	if err := st.DecodeOutput(&out); err != nil {
		t.Fatal(err)
	}

	if _, leaked := out["_mem_keeper"]; leaked {
		t.Fatalf("output %s leaks the memory channel", st.Output)
	}

	id, err := h.Client.Rerun(testCtx(t), st.ExecID, "recall")
	if err != nil {
		t.Fatal(err)
	}

	rst, err := h.Client.Wait(testCtx(t), id)
	if err != nil {
		t.Fatal(err)
	}

	completed(t, rst)
	mustContain(t, "rerun recall", result(t, rst, "recall"), codeword)

	fst := run(t, h.Client, "forgetful", map[string]any{"codeword": codeword})
	if got := result(t, fst, "recall"); strings.Contains(got, codeword) {
		t.Fatalf("the agent without memory knew the codeword: %q", got)
	}
}

const clerkFleet = `
namespace: e2e-clerk
agents:
  clerk:
    model: main
    role: Front-desk clerk
    goal: Help returning guests. Use what they told you in earlier conversations.
    memory: {type: nats, bucket: e2e_clerk_memory}
flows:
  desk:
    nodes:
      - {id: answer, type: task, agent: clerk, prompt: "{{.input.message}}"}
`

// TestLongTermMemory checks a NATS KV memory carries what a guest said in
// one execution into the next one.
func TestLongTermMemory(t *testing.T) {
	h := start(t, clerkFleet)
	room := secret("room")

	run(t, h.Client, "desk", map[string]any{
		"message": "Hi, I am Ada Byron. Please note my room booking code: " + room + ".",
	})

	st := run(t, h.Client, "desk", map[string]any{
		"message": "Hi again, it is Ada Byron. What is my room booking code? Answer with the code only.",
	})

	mustContain(t, "second visit", result(t, st, "answer"), room)
}
