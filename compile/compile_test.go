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

package compile_test

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

var update = flag.Bool("update", false, "rewrite golden files")

var catalog = compile.StaticCatalog{
	Providers:         []string{"openai", "anthropic", "ollama"},
	ToolTypes:         []string{"bash"},
	Activities:        []string{"publish"},
	MemoryTypes:       []string{"simple", "nats"},
	EmbedderProviders: []string{"openai"},
	VectorStoreTypes:  []string{"qdrant"},
}

func env(name string) (string, bool) {
	if name == "OPENAI_API_KEY" {
		return "sk-test", true
	}

	return "", false
}

// TestGolden compiles every testdata/*.yaml and compares the plan with the
// matching .golden.json. Run with -update to rewrite the golden files.
func TestGolden(t *testing.T) {
	files, globErr := filepath.Glob("testdata/*.yaml")
	if globErr != nil || len(files) == 0 {
		t.Fatalf("no fixtures: %v", globErr)
	}

	for _, in := range files {
		t.Run(filepath.Base(in), func(t *testing.T) {
			fleet, src, err := config.LoadFile(in, config.WithLookupEnv(env))
			if err != nil {
				t.Fatalf("load: %v", strings.Join(src.Describe(err), "\n"))
			}

			plan, err := compile.Compile(fleet, catalog)
			if err != nil {
				t.Fatalf("compile:\n%s", strings.Join(src.Describe(err), "\n"))
			}

			got, err := json.MarshalIndent(plan, "", "  ")
			if err != nil {
				t.Fatal(err)
			}

			got = append(got, '\n')
			golden := strings.TrimSuffix(in, ".yaml") + ".golden.json"

			if *update {
				if err = os.WriteFile(golden, got, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			want, err := os.ReadFile(golden)
			if err != nil {
				t.Fatalf("%v (run with -update)", err)
			}

			if !bytes.Equal(got, want) {
				t.Errorf("plan differs from %s (run with -update and review the diff):\n%s", golden, got)
			}
		})
	}
}

// TestPlanHasNoSecrets guards the rule that meta and plans never carry
// secrets: the API key must not leak into the compiled plan.
func TestPlanHasNoSecrets(t *testing.T) {
	fleet, _, err := config.LoadFile("testdata/sequential.yaml", config.WithLookupEnv(env))
	if err != nil {
		t.Fatal(err)
	}

	plan, err := compile.Compile(fleet, catalog)
	if err != nil {
		t.Fatal(err)
	}

	b, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}

	if bytes.Contains(b, []byte("sk-test")) {
		t.Fatal("the API key leaked into the plan")
	}
}

// TestYAMLAndGoAgree builds the sequential fixture's flow in Go and checks it
// hashes like the YAML one: both front ends must produce the same flows.
func TestYAMLAndGoAgree(t *testing.T) {
	fleet, _, err := config.LoadFile("testdata/sequential.yaml", config.WithLookupEnv(env))
	if err != nil {
		t.Fatal(err)
	}

	nodes := []flow.Node{
		{ID: "research", Type: flow.NodeTask, Next: "write",
			Retry: &flow.Retry{MaxAttempts: 3, Backoff: flow.BackoffExponential, Delay: flow.Duration(2e9)}},
		{ID: "write", Type: flow.NodeTask, Next: "publish", Timeout: flow.Duration(5 * 60e9)},
		{ID: "publish", Type: flow.NodeTask},
	}
	steps := []spec.Step{
		{Agent: "researcher", Prompt: "Research {{.input.topic}}"},
		{Agent: "writer"},
		{Activity: "publish"},
	}

	for i := range nodes {
		if err = spec.ApplyStep(&nodes[i], steps[i]); err != nil {
			t.Fatal(err)
		}
	}

	fleet.Flows["article"] = &flow.Flow{Name: "article", Nodes: nodes}

	goPlan, err := compile.Compile(fleet, catalog)
	if err != nil {
		t.Fatal(err)
	}

	yamlFleet, _, _ := config.LoadFile("testdata/sequential.yaml", config.WithLookupEnv(env))

	yamlPlan, err := compile.Compile(yamlFleet, catalog)
	if err != nil {
		t.Fatal(err)
	}

	gh, _ := goPlan.Flows[0].Hash()
	yh, _ := yamlPlan.Flows[0].Hash()

	if gh != yh {
		t.Fatalf("hash differs: go %s, yaml %s", gh, yh)
	}
}

func TestErrors(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want []string // "line: substring"
	}{
		{
			name: "references",
			yaml: `
models:
  m: {provider: nope}
agents:
  a: {model: missing, tools: [ghost]}
  bad.name: {model: m}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: zed, next: t}
      - {id: t, type: task, activity: unknown}
schedules:
  daily: {flow: nope, cron: "bad"}
`,
			want: []string{
				`3: models.m.provider: unknown provider "nope"`,
				`5: agents.a.model: unknown model "missing"`,
				`5: agents.a.tools: unknown tool "ghost"`,
				`6: agents.bad.name: agent name`,
				`10: flows.f.nodes.s.agent: unknown agent "zed"`,
				`11: flows.f.nodes.t.activity: unknown activity "unknown"`,
				`13: schedules.daily.flow: unknown flow "nope"`,
				`13: schedules.daily.cron:`,
			},
		},
		{
			name: "routes",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f:
    nodes:
      - {id: fan, type: fanout, branches: [b1, b2], next: j}
      - {id: b1, type: task, agent: a, routes: [j]}
      - {id: b2, type: task, agent: a}
      - {id: j, type: join, next: m}
      - {id: m, type: map, over: input.xs, agent: a, routes: [j]}
      - {id: r, type: task, agent: a, routes: [j, j, nowhere]}
`,
			want: []string{
				`8: flows.f.nodes.b1.routes: routes are not allowed on a fan-out branch`,
				`11: flows.f.nodes.m.routes: routes are not allowed on a map node`,
				`12: flows.f.nodes.r.routes: route "j" listed twice`,
				`12: flows.f.nodes.r.routes: unknown node "nowhere"`,
			},
		},
		{
			name: "steps",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f:
    channels: {notes: {}}
    nodes:
      - {id: s, type: task, agent: a, prompt: "{{.input", writes: {notes: out, missing: output}, next: k}
      - {id: k, type: task, kind: agent-sneaky}
`,
			want: []string{
				`8: flows.f.nodes.s.prompt: prompt:`,
				`8: flows.f.nodes.s.writes: channel "missing" is not declared`,
				`8: flows.f.nodes.s.writes: channel "notes": spec: write path`,
				`9: flows.f.nodes.k.kind: kind "agent-sneaky" uses a prefix reserved`,
			},
		},
		{
			name: "packtrail validation is mapped to the node",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: a, next: w}
      - {id: w, type: await, signal: go}
`,
			want: []string{`8: flows.f.nodes.w.timeout: timeout: `},
		},
		{
			name: "task-style fields",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: a, context: [ghost, s, s], next: t}
      - {id: t, type: task, activity: publish, prompt: hi, human_input: true}
`,
			want: []string{
				`7: flows.f.nodes.s.context: unknown node "ghost"`,
				`7: flows.f.nodes.s.context: node "s" listed twice`,
				`8: flows.f.nodes.t.prompt: prompt applies to agent steps only`,
				`8: flows.f.nodes.t.human_input: human_input applies to agent steps only`,
			},
		},
		{
			name: "memory, knowledge and delegation",
			yaml: `
models: {m: {provider: openai}}
embedders: {e: {provider: nope}}
vectorstores: {v: {type: qdrant}}
knowledge:
  k: {embedder: e, vectorstore: ghost, sources: [{path: "", splitter: html}]}
agents:
  a: {model: m, delegates: [b], knowledge: [missing], memory: {type: simple, summarize: {model: zz, threshold: 5, size: 9}}}
  b: {model: m, delegates: [a], memory: {type: execution, session: s}}
  c: {model: m, memory: redis}
flows:
  f:
    channels: {_mem_x: {}}
    nodes:
      - {id: s, type: task, agent: a}
`,
			want: []string{
				`3: embedders.e.provider: unknown embedding provider "nope"`,
				`6: knowledge.k.vectorstore: unknown vector store "ghost"`,
				`6: knowledge.k.sources.0.path: path is required`,
				`6: knowledge.k.sources.0.splitter: unknown splitter "html"`,
				`8: agents.a.knowledge: unknown knowledge "missing"`,
				`8: agents.a.memory.summarize.model: unknown model "zz"`,
				`8: agents.a.memory.summarize: threshold and size must be positive`,
				`8: agents.a.delegates: delegation cycle: a -> b -> a`,
				`9: agents.b.memory: memory "execution" takes no options`,
				`10: agents.c.memory.type: unknown memory type "redis"`,
				`13: flows.f.channels._mem_x: channel names starting with "_mem_" are reserved`,
			},
		},
		{
			name: "remotes and expose",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m, delegates: [r]}, r: {model: m}}
remotes:
  r: {owner: ops, name: reviewer}
  bad: {owner: "o.x", name: n}
flows:
  f:
    nodes:
      - {id: s, type: task, remote: ghost, next: t}
      - {id: t, type: task, remote: bad, routes: [s], human_input: true}
expose: {agents: [a, nope], flows: [f, missing]}
`,
			want: []string{
				`5: remotes.r: "r" is both an agent and a remote`,
				`6: remotes.bad.owner:`,
				`10: flows.f.nodes.s.remote: unknown remote "ghost"`,
				`11: flows.f.nodes.t.routes: routes need stiggy's route tools`,
				`11: flows.f.nodes.t.human_input: human_input applies to stiggy agents only`,
				`12: expose.agents: unknown agent "nope"`,
				`12: expose.flows: unknown flow "missing"`,
			},
		},
		{
			name: "crews and patterns",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  taken: {nodes: [{id: s, type: task, agent: a}]}
crews:
  c:
    tasks:
      - {id: t1, agent: a, description: one}
      - {id: t2, agent: ghost, description: two}
  bad: {process: chaotic, tasks: [{id: x, agent: a, description: d}]}
  taken: {tasks: [{id: x, agent: a, description: d}]}
patterns:
  p: {type: evaluator_optimizer, generator: a}
  q: {type: swarm, agents: [a, nobody]}
  r: {type: teleport}
`,
			want: []string{
				`10: crews.c.tasks.t2.agent: unknown agent "ghost"`,
				`11: crews.bad.process: unknown process "chaotic"`,
				`12: crews.taken: a flow with this name already exists`,
				`14: patterns.p.evaluator: evaluator is required`,
				`15: patterns.q: unknown agent "nobody"`,
				`16: patterns.r.type: unknown pattern type "teleport"`,
			},
		},
		{
			name: "subflow reference",
			yaml: `
flows:
  f:
    nodes:
      - {id: s, type: subflow, flow: ghost}
`,
			want: []string{`5: flows.f.nodes.s.flow: unknown flow "ghost"`},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fleet, src, err := config.Parse([]byte(tc.yaml), config.WithFilename("fleet.yaml"))
			if err == nil {
				_, err = compile.Compile(fleet, catalog)
			}

			if err == nil {
				t.Fatal("expected errors")
			}

			if !errors.Is(err, spec.ErrInvalid) {
				t.Errorf("errors.Is(err, spec.ErrInvalid) = false for %v", err)
			}

			got := strings.Join(src.Describe(err), "\n")

			for _, w := range tc.want {
				if !strings.Contains(got, "fleet.yaml:"+w) {
					t.Errorf("missing %q in:\n%s", w, got)
				}
			}
		})
	}
}
