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

package config

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

func lookup(vars map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := vars[k]

		return v, ok
	}
}

// TestFileFlowMirrorsFlow fails when packtrail adds a flow field that the
// YAML loader does not know about yet.
func TestFileFlowMirrorsFlow(t *testing.T) {
	tags := func(typ reflect.Type) []string {
		var out []string

		for i := range typ.NumField() {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}

			name, _, _ := strings.Cut(f.Tag.Get("yaml"), ",")
			out = append(out, name)
		}

		slices.Sort(out)

		return out
	}

	want := tags(reflect.TypeFor[flow.Flow]())
	if got := tags(reflect.TypeFor[fileFlow]()); !slices.Equal(got, want) {
		t.Fatalf("fileFlow fields %v, flow.Flow fields %v", got, want)
	}
}

func TestEnvExpansion(t *testing.T) {
	doc := `
models:
  m:
    provider: openai
    api_key: ${KEY}
    base_url: ${URL:-http://localhost:11434/v1}
    model: "${EMPTY:-fallback}"
tools:
  t: {type: bash, note: "costs $$5", nested: {list: ["${KEY}"]}}
agents:
  a: {model: m, system: "user ${WHO}"}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: a, prompt: "keep ${KEY} literally"}
`
	vars := map[string]string{"KEY": "sk-1", "WHO": "simone", "EMPTY": ""}

	f, _, err := Parse([]byte(doc), WithLookupEnv(lookup(vars)))
	if err != nil {
		t.Fatal(err)
	}

	m := f.Models["m"]
	if m.APIKey != "sk-1" || m.BaseURL != "http://localhost:11434/v1" || m.Model != "fallback" {
		t.Errorf("model = %+v", m)
	}

	opts := f.Tools["t"].Options
	if opts["note"] != "costs $5" {
		t.Errorf("note = %v", opts["note"])
	}

	if l := opts["nested"].(map[string]any)["list"].([]any); l[0] != "sk-1" {
		t.Errorf("nested = %v", l)
	}

	if f.Agents["a"].System != "user simone" {
		t.Errorf("system = %q", f.Agents["a"].System)
	}

	step, _, _ := spec.StepOf(&f.Flows["f"].Nodes[0])
	if step.Prompt != "keep ${KEY} literally" {
		t.Errorf("flows must not be expanded: prompt = %q", step.Prompt)
	}
}

// Crews and patterns compile into flows, which must not hold environment
// values (a secret would end up in the versioned definition): a reference
// is an error that points at it, not a silent literal.
func TestEnvRejectedInCrewsAndPatterns(t *testing.T) {
	doc := `
agents: {a: {model: m}}
crews:
  c:
    tasks:
      - {id: t, agent: a, description: "use ${KEY}, costs $$5"}
patterns:
  p: {type: supervisor, supervisor: "${BOSS}", workers: [a]}
`

	_, src, err := Parse([]byte(doc), WithLookupEnv(lookup(map[string]string{"KEY": "s3cr3t"})), WithFilename("f.yaml"))
	if err == nil {
		t.Fatal("expected errors")
	}

	msg := err.Error()
	for _, want := range []string{"crews.c.tasks.0.description", "${KEY}", "patterns.p.supervisor", "${BOSS}"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q lacks %q", msg, want)
		}
	}

	if strings.Contains(msg, "s3cr3t") {
		t.Errorf("error leaks the value: %q", msg)
	}

	if src == nil {
		t.Fatal("no source")
	}
}

func TestEnvMissing(t *testing.T) {
	doc := "models:\n  m:\n    provider: openai\n    api_key: ${NOPE}\n"

	_, src, err := Parse([]byte(doc), WithLookupEnv(lookup(nil)), WithFilename("f.yaml"))
	if !errors.Is(err, spec.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}

	got := strings.Join(src.Describe(err), "\n")
	if !strings.Contains(got, "f.yaml:4: models.m.api_key: environment variable NOPE is not set") {
		t.Fatalf("got %q", got)
	}
}

func TestStrictDecoding(t *testing.T) {
	cases := map[string]string{
		"unknown top-level key": "modles: {}\n",
		"unknown agent field":   "agents:\n  a: {model: m, temprature: 1}\n",
		"unknown node field":    "flows:\n  f:\n    nodes:\n      - {id: s, type: task, agnet: a}\n",
		"wrong type":            "agents:\n  a: {model: m, max_iterations: lots}\n",
	}

	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := Parse([]byte(doc)); err == nil || !strings.Contains(err.Error(), "line") {
				t.Fatalf("err = %v, want a line-numbered decode error", err)
			}
		})
	}
}

func TestDocumentShape(t *testing.T) {
	if _, _, err := Parse([]byte("")); !errors.Is(err, ErrEmpty) {
		t.Errorf("empty: %v", err)
	}

	if _, _, err := Parse([]byte("namespace: a\n---\nnamespace: b\n")); !errors.Is(err, ErrMultipleDocuments) {
		t.Errorf("multi: %v", err)
	}
}

func TestStepErrorsHaveLines(t *testing.T) {
	doc := `flows:
  f:
    name: other
    nodes:
      - {id: c, type: choice, agent: a}
      - {id: s, type: task, agent: a, activity: b}
`

	_, src, err := Parse([]byte(doc), WithFilename("f.yaml"))
	if err == nil {
		t.Fatal("expected errors")
	}

	got := strings.Join(src.Describe(err), "\n")

	for _, want := range []string{
		`f.yaml:3: flows.f.name: name "other" does not match`,
		`f.yaml:5: flows.f.nodes.c: node "c": spec: agent, activity and remote steps must be task or map nodes`,
		`f.yaml:6: flows.f.nodes.s: node "s": spec: a step runs exactly one of agent, activity or remote`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
}

func TestLineFallsBackToAncestor(t *testing.T) {
	src, err := newSource("", []byte("agents:\n  a:\n    model: m\n"))
	if err != nil {
		t.Fatal(err)
	}

	if l := src.Line("agents.a.tools"); l != 2 {
		t.Errorf("line = %d, want 2 (agents.a)", l)
	}

	if l := src.Line("nowhere"); l != 0 {
		t.Errorf("line = %d, want 0", l)
	}
}
