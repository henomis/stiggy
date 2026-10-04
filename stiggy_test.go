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

package stiggy_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

const sequentialYAML = `
namespace: seq
models:
  fake: {provider: fake}
agents:
  researcher: {model: fake, role: Researcher}
  writer: {model: fake}
flows:
  article:
    channels: {draft: {}}
    nodes:
      - id: research
        type: task
        agent: researcher
        prompt: "Research {{.input.topic}}"
        next: write
      - id: write
        type: task
        agent: writer
        writes: {draft: output.text}
        next: publish
      - id: publish
        type: task
        activity: publish
`

// TestSequentialFlow runs agent → agent → activity from YAML end to end:
// prompts render from the context, the default prompt carries the previous
// output, writes land in channels, activities see them, and usage is
// counted.
func TestSequentialFlow(t *testing.T) {
	fleet, _, err := config.Parse([]byte(sequentialYAML))
	if err != nil {
		t.Fatal(err)
	}

	// The YAML names model "fake"; replace it with a scripted instance.
	delete(fleet.Models, "fake")

	var published string

	h := stiggytest.Start(t,
		stiggy.WithFleet(fleet),
		stiggy.WithModel("fake", stiggytest.Reply(func(p string) string {
			if strings.HasPrefix(p, "Research ") {
				return "facts about " + strings.TrimPrefix(p, "Research ")
			}

			return "ARTICLE<" + p + ">"
		})),
		stiggy.WithActivity("publish", func(_ context.Context, j *worker.Job) (*worker.Result, error) {
			if cerr := j.Channel("draft", &published); cerr != nil {
				return nil, worker.Permanent(cerr)
			}

			return &worker.Result{Output: map[string]any{"url": "https://example.com/1"}}, nil
		}),
	)

	st := h.Run(t, "article", map[string]any{"topic": "nats"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status = %s (%s)", st.Status, st.Error)
	}

	var research struct{ Text string }
	if err = st.Result("research", &research); err != nil || research.Text != "facts about nats" {
		t.Fatalf("research = %+v, %v", research, err)
	}

	if !strings.Contains(published, `Output of step "research":`) || !strings.Contains(published, "facts about nats") {
		t.Fatalf("the writer's default prompt lacks the previous output: %q", published)
	}

	// With channels declared, packtrail's execution output is the channels.
	var out struct{ Draft string }
	if err = st.DecodeOutput(&out); err != nil || out.Draft != published {
		t.Fatalf("output = %+v, %v", out, err)
	}

	var pub struct{ URL string }
	if err = st.Result("publish", &pub); err != nil || pub.URL == "" {
		t.Fatalf("publish = %+v, %v", pub, err)
	}

	if st.Counters["agent_steps"] != 2 || st.Counters["tokens_out"] == 0 {
		t.Fatalf("counters = %v", st.Counters)
	}
}

// TestGoAPI builds the same kind of flow in Go only.
func TestGoAPI(t *testing.T) {
	greet, err := stiggy.AgentTask("greet", "greeter", spec.Step{Prompt: "Hi {{.input.name}}"})
	if err != nil {
		t.Fatal(err)
	}

	h := stiggytest.Start(t,
		stiggy.WithNamespace("goapi"),
		stiggy.WithModel("m", stiggytest.Echo("echo: ")),
		stiggy.WithAgent("greeter", spec.Agent{Model: "m"}),
		stiggy.WithFlow(&flow.Flow{Name: "hello", Nodes: []flow.Node{greet}}),
	)

	st := h.Run(t, "hello", map[string]any{"name": "ada"})

	var got struct{ Text string }
	if err = st.DecodeOutput(&got); err != nil || got.Text != "echo: Hi ada" {
		t.Fatalf("output = %+v, %v (status %s %s)", got, err, st.Status, st.Error)
	}
}

// TestAgentFailureIsReported checks a model error fails the step and the
// execution, with the error visible in the state.
func TestAgentFailureIsReported(t *testing.T) {
	step, err := stiggy.AgentTask("s", "a", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	h := stiggytest.Start(t,
		stiggy.WithModel("m", stiggytest.Replies()), // no replies: every call fails
		stiggy.WithAgent("a", spec.Agent{Model: "m"}),
		stiggy.WithFlow(&flow.Flow{Name: "f", Nodes: []flow.Node{step}}),
	)

	st := h.Run(t, "f", nil)
	if st.Status != packtrail.StatusFailed || !strings.Contains(st.Error, "no scripted reply") {
		t.Fatalf("status = %s, error = %q", st.Status, st.Error)
	}
}

// TestSplitRoles runs the workers before the engine exists: they must wait
// for the namespace, then serve.
func TestSplitRoles(t *testing.T) {
	srv := packtrailtest.Start(t)

	step, err := stiggy.AgentTask("s", "a", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	common := []stiggy.Option{
		stiggy.WithNamespace("split"),
		stiggy.WithModel("m", stiggytest.Echo("")),
		stiggy.WithAgent("a", spec.Agent{Model: "m"}),
		stiggy.WithFlow(&flow.Flow{Name: "f", Nodes: []flow.Node{step}}),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	workers, err := stiggy.New(srv.Connect(t), append(common, stiggy.WithRole(stiggy.RoleWorkers))...)
	if err != nil {
		t.Fatal(err)
	}

	wdone := make(chan error, 1)

	go func() { wdone <- workers.Run(ctx) }()

	time.Sleep(300 * time.Millisecond) // workers retry while nothing is provisioned

	engine, err := stiggy.New(srv.Connect(t), append(common, stiggy.WithRole(stiggy.RoleEngine))...)
	if err != nil {
		t.Fatal(err)
	}

	edone := make(chan error, 1)

	go func() { edone <- engine.Run(ctx) }()

	for _, app := range []*stiggy.App{engine, workers} {
		select {
		case <-app.Ready():
		case <-time.After(stiggytest.Timeout):
			t.Fatal("not ready")
		}
	}

	wctx, wcancel := context.WithTimeout(ctx, stiggytest.Timeout)
	defer wcancel()

	id, err := engine.Client().Start(wctx, "f", map[string]any{"msg": "ping"})
	if err != nil {
		t.Fatal(err)
	}

	st, err := engine.Client().Wait(wctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v, err %v", st, err)
	}

	cancel()

	for _, done := range []chan error{wdone, edone} {
		if rerr := <-done; rerr != nil {
			t.Errorf("run: %v", rerr)
		}
	}
}

func TestNewRejectsInvalidFleet(t *testing.T) {
	srv := packtrailtest.Start(t)

	_, err := stiggy.New(srv.Connect(t), stiggy.WithAgent("a", spec.Agent{Model: "missing"}))
	if !errors.Is(err, spec.ErrInvalid) {
		t.Fatalf("err = %v", err)
	}

	_, err = stiggy.New(srv.Connect(t),
		stiggy.WithAgent("a", spec.Agent{Model: "m"}), stiggy.WithAgent("a", spec.Agent{Model: "m"}))
	if !errors.Is(err, stiggy.ErrOption) {
		t.Fatalf("duplicate: err = %v", err)
	}
}

func TestPlanIsJSON(t *testing.T) {
	srv := packtrailtest.Start(t)

	app, err := stiggy.New(srv.Connect(t), stiggy.WithModel("m", stiggytest.Echo("")))
	if err != nil {
		t.Fatal(err)
	}

	if _, err = json.Marshal(app.Plan()); err != nil {
		t.Fatal(err)
	}
}

// TestOnly splits a flow's workers across two processes: each runs only
// the agents it is given, and together they complete the flow.
func TestOnly(t *testing.T) {
	srv := packtrailtest.Start(t)

	first, err := stiggy.AgentTask("first", "a", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	first.Next = "second"

	second, err := stiggy.AgentTask("second", "b", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	common := []stiggy.Option{
		stiggy.WithNamespace("only"),
		stiggy.WithModel("m", stiggytest.Echo("")),
		stiggy.WithAgent("a", spec.Agent{Model: "m"}),
		stiggy.WithAgent("b", spec.Agent{Model: "m"}),
		stiggy.WithFlow(&flow.Flow{Name: "f", Nodes: []flow.Node{first, second}}),
	}

	if _, err = stiggy.New(srv.Connect(t), append(common, stiggy.WithOnly("ghost"))...); !errors.Is(err, stiggy.ErrOnlyUnknown) {
		t.Fatalf("unknown only: err = %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	apps := make([]*stiggy.App, 0, 2)
	done := make(chan error, 2)

	for _, opts := range [][]stiggy.Option{
		{stiggy.WithOnly("a")}, // engine + worker a
		{stiggy.WithRole(stiggy.RoleWorkers), stiggy.WithOnly("b")}, // worker b
	} {
		app, nerr := stiggy.New(srv.Connect(t), append(common, opts...)...)
		if nerr != nil {
			t.Fatal(nerr)
		}

		if len(app.Plan().Workers) != 1 {
			t.Fatalf("workers = %v, want one", app.Plan().Workers)
		}

		apps = append(apps, app)

		go func() { done <- app.Run(ctx) }()
	}

	for _, app := range apps {
		select {
		case <-app.Ready():
		case <-time.After(stiggytest.Timeout):
			t.Fatal("not ready")
		}
	}

	wctx, wcancel := context.WithTimeout(ctx, stiggytest.Timeout)
	defer wcancel()

	id, err := apps[0].Client().Start(wctx, "f", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}

	st, err := apps[0].Client().Wait(wctx, id)
	if err != nil || st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %v, err %v", st, err)
	}

	cancel()

	for range apps {
		if rerr := <-done; rerr != nil {
			t.Errorf("run: %v", rerr)
		}
	}
}

// TestModelRetriesAndRateLimit checks a model's retries absorb a failing
// call (the step itself has one attempt) behind a rate limit.
func TestModelRetriesAndRateLimit(t *testing.T) {
	var calls atomic.Int64

	flaky := llm.Func(func(ctx context.Context, msgs []llm.Message, opts ...llm.CallOption) (*llm.Result, error) {
		if calls.Add(1) == 1 {
			return nil, errors.New("transient provider error")
		}

		return stiggytest.Echo("ok: ").Execute(ctx, msgs, opts...)
	})

	step, err := stiggy.AgentTask("s", "a", spec.Step{Prompt: "go"})
	if err != nil {
		t.Fatal(err)
	}

	h := stiggytest.Start(t,
		stiggy.WithModelSpec("m", spec.Model{
			Instance: flaky, Retries: 2, RateLimit: &spec.RateLimit{RequestsPerMinute: 600},
		}),
		stiggy.WithAgent("a", spec.Agent{Model: "m"}),
		stiggy.WithFlow(&flow.Flow{Name: "f", Nodes: []flow.Node{step}}),
	)

	st := h.Run(t, "f", map[string]any{})
	if st.Status != packtrail.StatusCompleted || calls.Load() != 2 {
		t.Fatalf("status %s (%s), calls %d", st.Status, st.Error, calls.Load())
	}
}
