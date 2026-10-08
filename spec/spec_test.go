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

package spec_test

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

func TestApplyStep(t *testing.T) {
	n := flow.Node{ID: "review", Type: flow.NodeTask, Meta: map[string]any{"owner": "ops"}}
	s := spec.Step{Agent: "critic", Routes: []string{"plan", spec.RouteHuman}, Writes: map[string]string{"v": "output.v"}}

	if err := spec.ApplyStep(&n, s); err != nil {
		t.Fatal(err)
	}

	if n.Kind != "agent-critic" {
		t.Errorf("kind = %q", n.Kind)
	}

	if !slices.Equal(n.Dynamic, []string{"plan"}) {
		t.Errorf("dynamic = %v, want [plan] (human is not a node)", n.Dynamic)
	}

	if n.Meta["owner"] != "ops" {
		t.Error("user meta was dropped")
	}

	got, ok, err := spec.StepOf(&n)
	if err != nil || !ok {
		t.Fatalf("StepOf: ok=%v err=%v", ok, err)
	}

	if got.Agent != "critic" || !slices.Equal(got.Routes, s.Routes) || got.Writes["v"] != "output.v" {
		t.Errorf("round trip = %+v", got)
	}
}

func TestApplyStepRejects(t *testing.T) {
	cases := []struct {
		name string
		node flow.Node
		step spec.Step
		want error
	}{
		{"both targets", flow.Node{Type: flow.NodeTask}, spec.Step{Agent: "a", Activity: "b"}, spec.ErrStepTarget},
		{"no target", flow.Node{Type: flow.NodeTask}, spec.Step{Prompt: "hi"}, spec.ErrStepTarget},
		{"choice node", flow.Node{Type: flow.NodeChoice}, spec.Step{Agent: "a"}, spec.ErrStepNodeType},
		{"map of subflows", flow.Node{Type: flow.NodeMap, Flow: "x"}, spec.Step{Agent: "a"}, spec.ErrStepNodeType},
		{"explicit kind", flow.Node{Type: flow.NodeTask, Kind: "k"}, spec.Step{Agent: "a"}, spec.ErrStepConflict},
		{
			"dynamic and routes", flow.Node{Type: flow.NodeTask, Dynamic: []string{"x"}},
			spec.Step{Agent: "a", Routes: []string{"y"}}, spec.ErrStepConflict,
		},
		{
			"reserved meta", flow.Node{Type: flow.NodeTask, Meta: map[string]any{spec.MetaKey: 1}},
			spec.Step{Agent: "a"}, spec.ErrStepConflict,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := spec.ApplyStep(&tc.node, tc.step); !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestApplyZeroStepIsNoop(t *testing.T) {
	n := flow.Node{ID: "x", Type: flow.NodeChoice}
	if err := spec.ApplyStep(&n, spec.Step{}); err != nil || n.Meta != nil {
		t.Fatalf("err=%v meta=%v", err, n.Meta)
	}
}

func TestParseWritePath(t *testing.T) {
	for in, want := range map[string][]string{
		"output":         nil,
		"output.a":       {"a"},
		"output.a.b_c-d": {"a", "b_c-d"},
	} {
		got, err := spec.ParseWritePath(in)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%q: got %v, %v", in, got, err)
		}
	}

	for _, bad := range []string{"", "out", "output.", "output..a", "input.a", "output.a b"} {
		if _, err := spec.ParseWritePath(bad); !errors.Is(err, spec.ErrWritePath) {
			t.Errorf("%q: err = %v", bad, err)
		}
	}
}

func TestParsePrompt(t *testing.T) {
	tmpl, err := spec.ParsePrompt("p", `Topic {{.input.topic}} notes {{json .channels.notes}}`)
	if err != nil {
		t.Fatal(err)
	}

	var b strings.Builder

	data := map[string]any{
		"input":    map[string]any{"topic": "nats"},
		"channels": map[string]any{"notes": []string{"a"}},
	}
	if err = tmpl.Execute(&b, data); err != nil {
		t.Fatal(err)
	}

	if got := b.String(); got != `Topic nats notes ["a"]` {
		t.Fatalf("got %q", got)
	}

	if _, err = spec.ParsePrompt("p", "{{.oops"); err == nil {
		t.Fatal("expected a parse error")
	}
}

func TestMemoryYAML(t *testing.T) {
	var a struct {
		Short spec.Memory `yaml:"short"`
		Long  spec.Memory `yaml:"long"`
		Unset spec.Memory `yaml:"unset"`
	}

	err := yaml.Unmarshal([]byte("short: none\nlong: {type: nats, bucket: mem}\n"), &a)
	if err != nil {
		t.Fatal(err)
	}

	if a.Short.Type != spec.MemoryNone || a.Long.Type != "nats" || a.Long.Options["bucket"] != "mem" {
		t.Fatalf("got %+v", a)
	}

	if a.Unset.TypeOrDefault() != spec.MemoryNone {
		t.Fatalf("default = %q", a.Unset.TypeOrDefault())
	}
}

func TestSessionTemplate(t *testing.T) {
	data := map[string]any{"input": map[string]any{"user": "ann", "email": "a@b.c"}, "item": map[string]any{"id": 7}}

	for _, tc := range []struct {
		text, want string
		static     bool
	}{
		{"shared", "shared", true},
		{"team/eu.west", "team/eu.west", true},
		{"user-{{.input.user}}", "user-ann", false},
		{"persona-{{.item.id}}", "persona-7", false},
	} {
		s, err := spec.ParseSession(tc.text)
		if err != nil {
			t.Fatalf("%q: %v", tc.text, err)
		}

		if s.Static() != tc.static {
			t.Errorf("%q: static = %v", tc.text, s.Static())
		}

		if got, rerr := s.Render(data); rerr != nil || got != tc.want {
			t.Errorf("%q: render = %q, %v; want %q", tc.text, got, rerr, tc.want)
		}
	}

	for _, text := range []string{"", "a b", ".x", "x.", "a..b", "a.*", "a>", "{{.input"} {
		if _, err := spec.ParseSession(text); err == nil {
			t.Errorf("%q: parsed, want an error", text)
		}
	}

	// A templated session fails closed: a missing key or an unsafe value is
	// an error, never an empty or shared session.
	for _, text := range []string{"user-{{.input.missing}}", "{{.input.email}}", "u-{{.nothing.here}}"} {
		s, err := spec.ParseSession(text)
		if err != nil {
			t.Fatalf("%q: %v", text, err)
		}

		if got, rerr := s.Render(data); rerr == nil {
			t.Errorf("%q: render = %q, want an error", text, got)
		}
	}
}
