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

package project_test

import (
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/internal/design/project"
	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

var update = flag.Bool("update", false, "rewrite the golden files")

func load(t *testing.T, path string) *project.Project {
	t.Helper()

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	p, err := project.Parse(data)
	if err != nil {
		t.Fatal(err)
	}

	return p
}

// stiggyCheck loads and compiles a generated fleet like `stiggy validate -no-env`.
func stiggyCheck(t *testing.T, yaml []byte) error {
	t.Helper()

	f, _, err := config.Parse(yaml, config.WithoutEnv())
	if err != nil {
		return err
	}

	_, err = compile.Compile(f, registry.New())

	return err
}

func TestGolden(t *testing.T) {
	files, _ := filepath.Glob("testdata/*.stiggy.json")
	if len(files) == 0 {
		t.Fatal("no testdata")
	}

	for _, f := range files {
		t.Run(filepath.Base(f), func(t *testing.T) {
			res, err := project.Compile(load(t, f))
			if err != nil {
				t.Fatal(err)
			}

			if len(res.Problems) > 0 {
				t.Fatalf("design problems: %+v", res.Problems)
			}

			golden := strings.TrimSuffix(f, ".stiggy.json") + ".golden.yaml"
			if *update {
				if err = os.WriteFile(golden, res.YAML, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatal(err)
			}

			if string(want) != string(res.YAML) {
				t.Errorf("YAML differs from %s (run with -update):\n%s", golden, res.YAML)
			}

			if err = stiggyCheck(t, res.YAML); err != nil {
				t.Errorf("stiggy rejects the generated fleet: %v\n%s", err, res.YAML)
			}
		})
	}
}

// mutate compiles a copy of the story project after changing it.
func mutate(t *testing.T, change func(p *project.Project)) *project.Result {
	t.Helper()

	p := load(t, "testdata/story.stiggy.json")
	change(p)

	res, err := project.Compile(p)
	if err != nil {
		t.Fatal(err)
	}

	return res
}

func params(t *testing.T, v any) json.RawMessage {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func hasProblem(res *project.Result, node, field, msg string) bool {
	for _, p := range res.Problems {
		if p.Node == node && (field == "" || p.Field == field) && strings.Contains(p.Msg, msg) {
			return true
		}
	}

	return false
}

func TestDesignProblems(t *testing.T) {
	wf := func(p *project.Project) *project.Workflow { return &p.Workflows[0] }

	tests := []struct {
		name               string
		change             func(p *project.Project)
		node, field, msgIn string
	}{
		{
			name:   "no start",
			change: func(p *project.Project) { wf(p).Nodes = wf(p).Nodes[1:] },
			msgIn:  "add a Start block",
		},
		{
			name: "deleted block referenced",
			change: func(p *project.Project) {
				wf(p).Nodes[3].Params = params(t, project.AgentConfig{Credential: "c1", Prompt: "{{ref gone text}}"})
			},
			node: "a2", field: "prompt", msgIn: "no longer exists",
		},
		{
			name:   "missing credential",
			change: func(p *project.Project) { p.Credentials = nil },
			node:   "a1", field: "credential", msgIn: "no longer exists",
		},
		{
			name: "loop without a list",
			change: func(p *project.Project) {
				wf(p).Nodes[2].Params = params(t, project.LoopParams{AgentConfig: project.AgentConfig{Credential: "c1"}})
			},
			node: "l1", field: "over", msgIn: "choose the list",
		},
		{
			name: "one port to two blocks",
			change: func(p *project.Project) {
				wf(p).Edges = append(wf(p).Edges, project.Edge{From: project.End{Node: "a1"}, To: project.End{Node: "a3"}})
			},
			node: "a1", msgIn: "use Parallel",
		},
		{
			name: "text and a value mixed in an output",
			change: func(p *project.Project) {
				wf(p).Nodes[7].Params = params(t, project.OutputParams{Fields: []project.Mapping{
					{Name: "headline", Value: "About {{ref start topic}}"},
				}})
			},
			node: "o", field: "fields.0", msgIn: "either one value or plain text",
		},
		{
			name: "rule against a number that is not one",
			change: func(p *project.Project) {
				wf(p).Nodes[5].Params = params(t, project.ConditionParams{Rules: []project.Rule{
					{Left: "{{ref a3 ok}}", Op: project.OpGreater, Value: "many"},
				}})
			},
			node: "c", field: "rules.0", msgIn: "not a number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := mutate(t, tt.change)
			if !hasProblem(res, tt.node, tt.field, tt.msgIn) {
				t.Fatalf("want a problem on %q/%q with %q, got %+v", tt.node, tt.field, tt.msgIn, res.Problems)
			}
		})
	}
}

func TestBranchNeedsMerge(t *testing.T) {
	p := load(t, "testdata/support.stiggy.json")

	edges := p.Workflows[0].Edges[:0]
	for _, e := range p.Workflows[0].Edges {
		if e.From.Node != "b1" {
			edges = append(edges, e)
		}
	}

	p.Workflows[0].Edges = edges

	res, err := project.Compile(p)
	if err != nil {
		t.Fatal(err)
	}

	if !hasProblem(res, "b1", "", "connect it to a Merge") {
		t.Fatalf("problems: %+v", res.Problems)
	}
}

// TestMergeMin compiles a Merge that waits for every branch and needs a
// number of them to succeed.
func TestMergeMin(t *testing.T) {
	merge := func(mp project.MergeParams) *project.Result {
		p := load(t, "testdata/support.stiggy.json")
		p.Workflows[0].Nodes[7].Params = params(t, mp)

		res, err := project.Compile(p)
		if err != nil {
			t.Fatal(err)
		}

		return res
	}

	two, none, bad := 2, 0, -1

	for want, mp := range map[string]project.MergeParams{
		"policy: min:1": {Wait: "min"},
		"policy: min:2": {Wait: "min", Min: &two},
		"policy: min:0": {Wait: "min", Min: &none},
	} {
		res := merge(mp)
		if len(res.Problems) > 0 || !strings.Contains(string(res.YAML), want) {
			t.Fatalf("want %q, problems %+v:\n%s", want, res.Problems, res.YAML)
		}

		if err := stiggyCheck(t, res.YAML); err != nil {
			t.Fatalf("%s: %v", want, err)
		}
	}

	if res := merge(project.MergeParams{Wait: "min", Min: &bad}); !hasProblem(res, "m", "min", "0 or more") {
		t.Fatalf("problems: %+v", res.Problems)
	}
}

// TestLocate maps stiggy's own errors on the generated fleet back to blocks.
func TestLocate(t *testing.T) {
	res := mutate(t, func(p *project.Project) {
		// A model id stiggy accepts but an agent tool that does not exist is
		// out of reach from the designer; break a condition expression instead.
		p.Workflows[0].Nodes[5].Params = params(t, project.ConditionParams{Rules: []project.Rule{
			{Left: "{{ref a3 ok}}", Op: project.OpEquals, Value: "{{ref a3 ok}}"},
		}})
		p.Credentials[0].Provider = "no-such-provider"
	})

	if len(res.Problems) > 0 {
		t.Fatalf("design problems: %+v", res.Problems)
	}

	err := stiggyCheck(t, res.YAML)

	var se *spec.Error
	if !errors.As(err, &se) {
		t.Fatalf("want a located stiggy error, got %v", err)
	}

	loc := res.Locate(se.Path)
	if loc.Workflow != "w1" || loc.Node == "" || loc.Field != "credential" {
		t.Fatalf("%s located at %+v", se.Path, loc)
	}

	if got := res.Locate("flows.story.nodes.fact_check.prompt"); got.Node != "a3" || got.Field != "prompt" {
		t.Fatalf("node path located at %+v", got)
	}

	if got := res.Locate("flows.story.nodes.sign_off_decision"); got.Node != "h" {
		t.Fatalf("generated decision node located at %+v", got)
	}
}

func TestMergesEqualModelsAndAgents(t *testing.T) {
	res := mutate(t, func(p *project.Project) {
		w := &p.Workflows[0]
		// A second Plan block, identical to the first: one agent, one model.
		w.Nodes = append(w.Nodes, project.Node{ID: "a9", Type: project.TypeAgent, Name: "Plan", Params: w.Nodes[1].Params})
	})

	y := string(res.YAML)

	// Story has two models (one with a temperature); the copy adds neither a
	// model nor an agent, only a flow node.
	if strings.Contains(y, "gpt_4o_mini_3") || strings.Contains(y, "\n  plan_2:\n") {
		t.Errorf("not merged:\n%s", y)
	}

	if !strings.Contains(y, "id: plan_2") {
		t.Errorf("the second block is missing:\n%s", y)
	}
}

func TestParseRejects(t *testing.T) {
	if _, err := project.Parse([]byte(`{"version": 2, "workflows": []}`)); !errors.Is(err, project.ErrVersion) {
		t.Fatalf("version: %v", err)
	}

	if _, err := project.Parse([]byte(`{"version": 1, "workflows": [], "extra": 1}`)); !errors.Is(err, project.ErrDecode) {
		t.Fatalf("unknown field: %v", err)
	}
}
