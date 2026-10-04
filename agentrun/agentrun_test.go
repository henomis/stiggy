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
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/spec"
)

func job(t *testing.T, step spec.Step, ctx worker.Context) *worker.Job {
	t.Helper()

	meta, err := json.Marshal(map[string]any{spec.MetaKey: step})
	if err != nil {
		t.Fatal(err)
	}

	return &worker.Job{Node: "n", Meta: meta, Context: ctx}
}

func TestPromptTemplate(t *testing.T) {
	j := job(t, spec.Step{Agent: "a", Prompt: "{{.input.topic}} / {{json .channels.notes}} / {{.item}}"}, worker.Context{
		Input:    json.RawMessage(`{"topic":"nats"}`),
		Channels: map[string]json.RawMessage{"notes": json.RawMessage(`["x"]`)},
		Item:     json.RawMessage(`"it"`),
	})

	got, err := Prompt(j, spec.Step{Prompt: "{{.input.topic}} / {{json .channels.notes}} / {{.item}}"})
	if err != nil || got != `nats / ["x"] / it` {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestDefaultPrompt(t *testing.T) {
	ctx := worker.Context{
		Input:    json.RawMessage(`{"topic":"nats"}`),
		LastNode: "research",
		Results:  map[string]json.RawMessage{"research": json.RawMessage(`{"text":"facts"}`)},
	}

	got, err := Prompt(job(t, spec.Step{Agent: "a"}, ctx), spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	want := "Input:\n{\n  \"topic\": \"nats\"\n}\n\nOutput of step \"research\":\nfacts"
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	if got, _ = Prompt(job(t, spec.Step{Agent: "a"}, worker.Context{}), spec.Step{}); got != "Begin." {
		t.Fatalf("empty context: %q", got)
	}
}

func TestSystemPrompt(t *testing.T) {
	if got := SystemPrompt("x", spec.Agent{}); got != "You are x, a helpful agent." {
		t.Errorf("default: %q", got)
	}

	got := SystemPrompt("x", spec.Agent{System: "Be brief.", Role: "Editor", Goal: "Clarity"})
	if got != "Be brief.\n\nRole: Editor\n\nGoal: Clarity" {
		t.Errorf("composed: %q", got)
	}
}

func TestClassify(t *testing.T) {
	pe := func(code int) error { return &llm.ProviderError{Provider: "p", StatusCode: code, Err: errors.New("x")} }

	for err, permanent := range map[error]bool{
		pe(http.StatusBadRequest):              true,
		pe(http.StatusUnauthorized):            true,
		pe(http.StatusTooManyRequests):         false,
		pe(http.StatusConflict):                false,
		pe(http.StatusInternalServerError):     false,
		agent.ErrMaxIterationsReached:          true,
		fmt.Errorf("wrapped: %w", pe(403)):     true,
		errors.New("connection reset by peer"): false,
	} {
		if got := worker.IsPermanent(classify(err)); got != permanent {
			t.Errorf("%v: permanent = %v, want %v", err, got, permanent)
		}
	}
}

func TestWrongAgentIsPermanent(t *testing.T) {
	h := Handler(Agent{Name: "a"})

	_, err := h(t.Context(), job(t, spec.Step{Agent: "b"}, worker.Context{}))
	if !errors.Is(err, ErrAgentMismatch) || !worker.IsPermanent(err) {
		t.Fatalf("err = %v", err)
	}
}

func TestPromptContextAndExpectedOutput(t *testing.T) {
	ctx := worker.Context{
		LastNode: "b",
		Results: map[string]json.RawMessage{
			"a": json.RawMessage(`{"text":"from a"}`),
			"b": json.RawMessage(`{"text":"from b"}`),
		},
	}
	s := spec.Step{Agent: "x", Context: []string{"a"}, ExpectedOutput: "One line."}

	got, err := Prompt(job(t, s, ctx), s)
	if err != nil {
		t.Fatal(err)
	}

	want := "Output of step \"a\":\nfrom a\n\nExpected output:\nOne line."
	if got != want {
		t.Fatalf("got %q\nwant %q", got, want)
	}

	s.Prompt = "Summarise."

	if got, _ = Prompt(job(t, s, ctx), s); got != "Summarise.\n\nOutput of step \"a\":\nfrom a\n\nExpected output:\nOne line." {
		t.Fatalf("template + context: %q", got)
	}
}

func TestParseOutput(t *testing.T) {
	schema := map[string]any{"type": "object"}

	for in, ok := range map[string]bool{
		`{"a":1}`:                 true,
		"```json\n{\"a\":1}\n```": true,
		"```\n{\"a\":1}```":       true,
		`[1,2]`:                   false,
		`not json`:                false,
		`null`:                    false,
	} {
		out, err := parseOutput(in, schema)
		if ok != (err == nil) {
			t.Errorf("%q: out=%v err=%v", in, out, err)
		}

		if err != nil && (!errors.Is(err, ErrNotJSONObject) || worker.IsPermanent(err)) {
			t.Errorf("%q: err = %v, want a retryable ErrNotJSONObject", in, err)
		}
	}

	out, _ := parseOutput("plain", nil)
	if out.(map[string]any)[spec.OutputText] != "plain" {
		t.Errorf("no schema: %v", out)
	}
}

func TestParseReply(t *testing.T) {
	for raw, want := range map[string]reply{
		`"blue"`:                 {Answer: "blue"},
		`{"answer":"blue"}`:      {Answer: "blue"},
		`{"feedback":"shorter"}`: {Feedback: "shorter"},
		`{"approve":true}`:       {Approve: true},
		`42`:                     {Answer: "42"},
	} {
		if got := parseReply(json.RawMessage(raw)); got != want {
			t.Errorf("%s: got %+v", raw, got)
		}
	}

	if !(reply{Approve: true}).approves() || (reply{Approve: true, Feedback: "x"}).approves() {
		t.Error("approval with feedback must count as feedback")
	}
}

func TestSteeringTools(t *testing.T) {
	ctl := &control{cancel: func(error) {}}

	tools, err := ctl.tools(spec.Step{Routes: []string{"plan", "publish-now", spec.RouteHuman}})
	if err != nil {
		t.Fatal(err)
	}

	names := make([]string, 0, len(tools))
	for _, tool := range tools {
		names = append(names, tool.Name())
	}

	want := []string{"route_to_plan", "route_to_" + agent.SanitizeToolName("publish-now"), "ask_human"}
	if fmt.Sprint(names) != fmt.Sprint(want) {
		t.Fatalf("tools = %v, want %v", names, want)
	}

	if ctl.instructions(spec.Step{}) != "" {
		t.Error("a step without routes needs no instructions")
	}
}
