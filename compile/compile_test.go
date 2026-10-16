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
	MemoryTypes:       []string{"simple", "nats", "rag"},
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
			name: "memory sessions",
			yaml: `
models: {m: {provider: openai}}
agents:
  a: {model: m, memory: {type: simple, session: "user-{{.input.id"}}
  b: {model: m, memory: {type: simple, session: "a b"}}
  c: {model: m, memory: {type: simple, session: "user-{{.input.id}}"}}
  d: {model: m, memory: {type: simple, session: "user-{{.item}}"}}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: d}
expose: {agents: [c]}
`,
			want: []string{
				`4: agents.a.memory.session: session: template: session:1: unclosed action`,
				`5: agents.b.memory.session: spec: a memory session must be`,
				`6: agents.c.memory.session: an exposed agent has no job context`,
			},
		},
		{
			name: "rag memory has no sessions",
			yaml: `
models: {m: {provider: openai}}
embedders: {e: {provider: openai}}
vectorstores: {v: {type: qdrant}}
knowledge: {k: {embedder: e, vectorstore: v}}
agents:
  a: {model: m, memory: {type: rag, knowledge: k, session: "user-{{.input.user}}"}}
  b: {model: m, memory: {type: rag, knowledge: k, per_execution: true}}
  c: {model: m, memory: {type: rag, knowledge: k}}
flows:
  f: {nodes: [{id: s, type: task, agent: c}]}
`,
			want: []string{
				`7: agents.a.memory: a memory stored in knowledge is one collection shared by every job`,
				`8: agents.b.memory: a memory stored in knowledge is one collection shared by every job`,
			},
		},
		{
			name: "system prompt templates",
			yaml: `
models: {m: {provider: openai}}
agents:
  a: {model: m, role: "{{.item.name", goal: "Help {{.input.user}}."}
  front: {model: m, system: "Serve {{.input.user}}.", delegates: [helper]}
  helper: {model: m, backstory: "{{.input.topic}}", delegates: [deep]}
  deep: {model: m, goal: "{{.input.x}}"}
  plain: {model: m, system: "Be brief.", delegates: [helper]}
flows:
  f:
    nodes:
      - {id: s, type: task, agent: a, expected_output: "{{.input.format", next: t}
      - {id: t, type: task, agent: deep, expected_output: "A list of {{.input.n}} items."}
expose: {agents: [front, plain]}
`,
			want: []string{
				`4: agents.a.role: prompt: template: role:1: unclosed action`,
				`5: agents.front.system: an exposed agent has no job context`,
				`6: agents.helper.backstory: a delegate of exposed agent "front" has no job context`,
				`7: agents.deep.goal: a delegate of exposed agent "front" has no job context`,
				`12: flows.f.nodes.s.expected_output: prompt: template: expected_output:1: unclosed action`,
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
			name: "rate limits",
			yaml: `
models:
  empty: {provider: openai, rate_limit: {}}
  negative: {provider: openai, rate_limit: {requests_per_minute: -1, max_concurrent: 2}}
`,
			want: []string{
				`3: models.empty.rate_limit: set requests_per_minute, max_concurrent or both`,
				`4: models.negative.rate_limit: requests_per_minute and max_concurrent must not be negative`,
			},
		},
		{
			name: "model retries",
			yaml: `
models:
  negative: {provider: openai, retries: 2, retry_backoff: -1s}
  lonely: {provider: openai, retry_backoff: 5s}
`,
			want: []string{
				`3: models.negative.retry_backoff: must not be negative`,
				`4: models.lonely.retry_backoff: needs retries`,
			},
		},
		{
			name: "crew and pattern retries",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}, b: {model: m}}
crews:
  c:
    retry: {max_attempts: 99}
    tasks: [{id: t, agent: a, description: d}]
  d:
    tasks:
      - {id: t, agent: a, description: d, retry: {max_attempts: 2, backoff: zigzag}}
patterns:
  p: {type: evaluator_optimizer, generator: a, evaluator: b, retry: {max_attempts: -1}}
`,
			want: []string{
				`6: crews.c.retry.max_attempts: must be between 0 and 64`,
				`10: crews.d.tasks.t.retry.backoff: unknown backoff "zigzag"`,
				`12: patterns.p.retry.max_attempts: must be between 0 and 64`,
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
		{
			name: "writes that can never resolve",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
remotes: {r: {owner: ops, name: reviewer}}
flows:
  f:
    channels: {c: {}, d: {}}
    nodes:
      - {id: plain, type: task, agent: a, writes: {c: output.summary}, next: far}
      - {id: far, type: task, remote: r, writes: {c: output.text.deep}, next: closed}
      - id: closed
        type: task
        agent: a
        output_schema: {type: object, properties: {title: {type: string}}, additionalProperties: false}
        writes: {c: output.title, d: output.summary}
`,
			want: []string{
				`9: flows.f.nodes.plain.writes: channel "c": output.summary can never resolve: without an output_schema`,
				`10: flows.f.nodes.far.writes: channel "c": output.text.deep can never resolve`,
				`15: flows.f.nodes.closed.writes: channel "d": output.summary can never resolve: the output_schema has no property "summary"`,
			},
		},
		{
			name: "schedule input",
			yaml: `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f: {nodes: [{id: s, type: task, agent: a}]}
schedules:
  word: {flow: f, cron: "@daily", input: hello}
  list: {flow: f, cron: "@daily", input: [1, 2]}
`,
			want: []string{
				`7: schedules.word.input: input must be an object`,
				`8: schedules.list.input: input must be an object`,
			},
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

// TestWritesThatMayResolve covers the write paths compile must accept: the
// whole output, the plain answer, an activity's fields, and fields an output
// schema declares or leaves open.
func TestWritesThatMayResolve(t *testing.T) {
	const fleet = `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f:
    channels: {c: {}, d: {}}
    nodes:
      - {id: whole, type: task, agent: a, writes: {c: output, d: output.text}, next: act}
      - {id: act, type: task, activity: publish, writes: {c: output.url}, next: open}
      - id: open
        type: task
        agent: a
        output_schema: {type: object, properties: {title: {type: string}}}
        writes: {c: output.title, d: output.extra}
schedules:
  ok: {flow: f, cron: "@daily", input: {topic: nats}}
  bare: {flow: f, cron: "@daily"}
`

	f, src, err := config.Parse([]byte(fleet))
	if err == nil {
		_, err = compile.Compile(f, catalog)
	}

	if err != nil {
		t.Fatalf("unexpected errors:\n%s", strings.Join(src.Describe(err), "\n"))
	}
}

// TestFailedCrewIsNotAnUnknownFlow: a crew or pattern that fails to expand
// reports its own problem; references to it are not reported on top.
func TestFailedCrewIsNotAnUnknownFlow(t *testing.T) {
	const fleet = `
models: {m: {provider: openai}}
agents: {a: {model: m}}
flows:
  f: {nodes: [{id: s, type: subflow, flow: c}]}
crews:
  c: {tasks: [{id: t, agent: a}]}
patterns:
  p: {type: teleport}
schedules:
  daily: {flow: c, cron: "@daily"}
expose: {flows: [c, p]}
`

	f, src, err := config.Parse([]byte(fleet))
	if err != nil {
		t.Fatal(err)
	}

	_, err = compile.Compile(f, catalog)
	got := strings.Join(src.Describe(err), "\n")

	if !strings.Contains(got, "crews.c") || !strings.Contains(got, "patterns.p.type") {
		t.Errorf("missing the crew's and the pattern's own errors in:\n%s", got)
	}

	if strings.Contains(got, "unknown flow") {
		t.Errorf("a failed crew or pattern is reported as an unknown flow:\n%s", got)
	}
}

// TestErrorOrderIsStable: the same fleet reports its problems in the same
// order on every run, whatever order Go walks its maps in.
func TestErrorOrderIsStable(t *testing.T) {
	const fleet = `
models:
  m1: {provider: openai, api_key: "${MISSING_1}"}
  m2: {provider: openai, api_key: "${MISSING_2}"}
  m3: {provider: openai, api_key: "${MISSING_3}"}
  m4: {provider: openai, api_key: "${MISSING_4}"}
flows:
  f1: {name: x, nodes: [{id: s, type: task, agent: a, activity: b}]}
  f2: {name: x, nodes: [{id: s, type: task, agent: a, activity: b}]}
  f3: {name: x, nodes: [{id: s, type: task, agent: a, activity: b}]}
  f4: {name: x, nodes: [{id: s, type: task, agent: a, activity: b}]}
`

	describe := func(data string, opts ...config.Option) string {
		t.Helper()

		f, src, err := config.Parse([]byte(data), opts...)
		if err == nil {
			_, err = compile.Compile(f, catalog)
		}

		if err == nil {
			t.Fatal("expected errors")
		}

		return strings.Join(src.Describe(err), "\n")
	}

	const channels = `
flows:
  f:
    channels: {_mem_a: {}, _mem_b: {}, _mem_c: {}, _mem_d: {}, _mem_e: {}}
    nodes: [{id: s, type: task, kind: k}]
`

	for name, run := range map[string]func() string{
		"load":    func() string { return describe(fleet, config.WithLookupEnv(env)) },
		"compile": func() string { return describe(channels) },
	} {
		first := run()
		if strings.Count(first, "\n") < 3 {
			t.Fatalf("%s: want several errors, got:\n%s", name, first)
		}

		for range 30 {
			if got := run(); got != first {
				t.Fatalf("%s: order changed between runs:\n%s\n--- then ---\n%s", name, first, got)
			}
		}
	}
}
