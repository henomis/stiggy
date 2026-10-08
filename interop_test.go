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
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/stiggytest"
)

// handlerFunc adapts a function to phero's protocol handler.
type handlerFunc func(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error)

func (f handlerFunc) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	return f(ctx, parts...)
}

func textResult(s string) *agent.Result { return &agent.Result{Parts: []llm.ContentPart{llm.Text(s)}} }

// serveRemote runs a protocol agent outside stiggy (as another team would)
// until the test ends.
func serveRemote(t *testing.T, nc *nats.Conn, owner, name string, h natsagent.Handler) {
	t.Helper()

	srv, err := natsagent.New(nc, h, owner, name)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		_ = srv.Start(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	// Wait until discovery finds it, as callers will.
	r, err := natsagent.NewResolver(natsagent.NewClient(nc))
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()

	keys := []natsagent.AgentKey{{Owner: owner, Name: name}}
	if err = r.WaitReady(context.Background(), keys, stiggytest.Timeout, 50*time.Millisecond); err != nil {
		t.Fatal(err)
	}
}

// TestRemoteStep prompts an agent served by someone else: the prompt carries
// the step's identity in headers, and a structured answer is parsed.
func TestRemoteStep(t *testing.T) {
	var sawHeaders atomic.Bool

	reviewer := handlerFunc(func(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
		req, ok := natsagent.RequestFrom(ctx)
		if ok && req.Header.Get(agentrun.HeaderExec) != "" && req.Header.Get(agentrun.HeaderNode) == "review" {
			sawHeaders.Store(true)
		}

		if !strings.Contains(llm.TextContent(parts...), "JSON Schema") {
			return textResult("no schema in the prompt"), nil
		}

		return textResult(`{"approved": true}`), nil
	})

	h := startYAML(t, `
remotes:
  reviewer: {owner: ops, name: reviewer, description: Reviews drafts.}
flows:
  check:
    nodes:
      - id: review
        type: task
        remote: reviewer
        prompt: "Review {{.input.doc}}"
        output_schema: {type: object, required: [approved]}
        retry: {max_attempts: 5, delay: 100ms}
`, nil)

	serveRemote(t, h.Conn, "ops", "reviewer", reviewer)

	st := h.Run(t, "check", map[string]any{"doc": "d1"})
	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %s", st.Status, st.Error)
	}

	var out struct{ Approved bool }
	if err := st.DecodeOutput(&out); err != nil || !out.Approved {
		t.Fatalf("output = %s, %v", st.Output, err)
	}

	if !sawHeaders.Load() {
		t.Error("the remote agent did not get the Stiggy-* headers")
	}
}

// TestRemotePermanentError checks a protocol 4xx fails the step at once,
// without using its retries.
func TestRemotePermanentError(t *testing.T) {
	var calls atomic.Int64

	h := startYAML(t, `
remotes:
  strict: {owner: ops, name: strict}
flows:
  f:
    nodes:
      - {id: s, type: task, remote: strict, retry: {max_attempts: 4, delay: 10ms}}
`, nil)

	serveRemote(t, h.Conn, "ops", "strict", handlerFunc(func(context.Context, ...llm.ContentPart) (*agent.Result, error) {
		calls.Add(1)

		return nil, &natsagent.CodedError{Code: http.StatusBadRequest, ErrCode: "bad_input", Message: "nope"}
	}))

	st := h.Run(t, "f", map[string]any{})
	if st.Status != packtrail.StatusFailed || calls.Load() != 1 {
		t.Fatalf("status %s, calls %d: want one call and a failure", st.Status, calls.Load())
	}
}

// TestRemoteEmptyAnswer checks an empty answer from a remote agent is
// retried like any transient failure.
func TestRemoteEmptyAnswer(t *testing.T) {
	var calls atomic.Int64

	h := startYAML(t, `
remotes:
  shy: {owner: lab, name: shy}
flows:
  f:
    nodes:
      - {id: s, type: task, remote: shy, retry: {max_attempts: 3, delay: 10ms}}
`, nil)

	serveRemote(t, h.Conn, "lab", "shy", handlerFunc(func(context.Context, ...llm.ContentPart) (*agent.Result, error) {
		if calls.Add(1) == 1 {
			return textResult(""), nil
		}

		return textResult("found it"), nil
	}))

	st := h.Run(t, "f", map[string]any{})

	var out struct{ Text string }
	if err := st.DecodeOutput(&out); err != nil || out.Text != "found it" || calls.Load() != 2 {
		t.Fatalf("output %+v, %v, calls %d (status %s %s)", out, err, calls.Load(), st.Status, st.Error)
	}
}

// TestExposedFlowAndAgent serves a flow and an agent on the protocol and
// calls them as any v0.3 client would.
func TestExposedFlowAndAgent(t *testing.T) {
	h := startYAML(t, `
namespace: crew
models: {m: {provider: x}}
agents:
  writer: {model: m}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, prompt: "draft about {{.input.prompt}}"}
expose:
  owner: newsroom
  agents: [writer]
  flows: [write]
`, map[string]llm.LLM{"m": stiggytest.Echo("text: ")})

	ctx := ctxTimeout(t)
	client := natsagent.NewClient(h.Conn)

	handles, err := client.Discover(ctx, natsagent.FilterByOwner("newsroom"))
	if err != nil {
		t.Fatal(err)
	}

	byName := map[string]*natsagent.AgentHandle{}
	for _, hd := range handles {
		byName[hd.Name] = hd

		if hd.Agent != stiggy.AgentID {
			t.Errorf("%s: agent id %q", hd.Name, hd.Agent)
		}
	}

	ask := func(name, prompt string) string {
		t.Helper()

		hd, ok := byName[name]
		if !ok {
			t.Fatalf("%s not discovered among %v", name, handles)
		}

		s, perr := hd.Prompt(ctx, prompt)
		if perr != nil {
			t.Fatal(perr)
		}
		defer s.Close()

		text, perr := s.Text(ctx)
		if perr != nil {
			t.Fatal(perr)
		}

		return text
	}

	if got := ask("write", "nats"); got != "text: draft about nats" {
		t.Errorf("flow answer = %q", got)
	}

	if got := ask("writer", "hello"); got != "text: hello" {
		t.Errorf("agent answer = %q", got)
	}

	// The flow ran as an ordinary execution of the fleet.
	sums, err := h.Client.List(ctx, packtrail.ListFilter{Flow: "write"})
	if err != nil || len(sums) != 1 {
		t.Fatalf("executions of write = %v, %v", sums, err)
	}
}

// TestRemoteDelegate lets an agent delegate to a remote agent through a tool.
func TestRemoteDelegate(t *testing.T) {
	planner := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		if len(r.ToolResults) == 0 {
			return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_oracle", Args: map[string]any{"prompt": "answer?"}}}}
		}

		return stiggytest.Response{Text: "oracle says " + r.ToolResults[0]}
	})

	h := startYAML(t, `
models: {p: {provider: x}}
remotes:
  oracle: {owner: ops, name: oracle}
agents:
  planner: {model: p, delegates: [oracle]}
flows:
  f:
    nodes:
      - {id: plan, type: task, agent: planner}
`, map[string]llm.LLM{"p": planner})

	serveRemote(t, h.Conn, "ops", "oracle", handlerFunc(func(context.Context, ...llm.ContentPart) (*agent.Result, error) {
		return textResult("42"), nil
	}))

	st := h.Run(t, "f", map[string]any{})

	var out struct{ Text string }
	if err := st.DecodeOutput(&out); err != nil || !strings.Contains(out.Text, "42") {
		t.Fatalf("output = %q, %v (status %s %s)", out.Text, err, st.Status, st.Error)
	}
}

// TestExposedFlowExecutionIDJoins retries a prompt with the same
// caller-chosen execution id: it joins the execution instead of starting
// another.
func TestExposedFlowExecutionIDJoins(t *testing.T) {
	h := startYAML(t, `
namespace: crew
models: {m: {provider: x}}
agents:
  writer: {model: m}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, prompt: "draft about {{.input.prompt}}"}
expose:
  owner: newsroom
  flows: [write]
`, map[string]llm.LLM{"m": stiggytest.Echo("text: ")})

	ctx := ctxTimeout(t)

	handles, err := natsagent.NewClient(h.Conn).Discover(ctx, natsagent.FilterByOwner("newsroom"))
	if err != nil || len(handles) != 1 {
		t.Fatalf("discover = %v, %v", handles, err)
	}

	for range 2 {
		s, serr := handles[0].Send(ctx, &natsagent.Request{
			Prompt: "nats", Header: nats.Header{stiggy.HeaderExecutionID: []string{"write-1"}},
		})
		if serr != nil {
			t.Fatal(serr)
		}

		got, terr := s.Text(ctx)
		s.Close()

		if terr != nil || got != "text: draft about nats" {
			t.Fatalf("same id = %q, %v", got, terr)
		}
	}

	st, err := h.Client.Get(ctx, "write-1")
	if err != nil || st.Visits["draft"] != 1 {
		t.Fatalf("execution = %+v, %v; want one run of draft", st, err)
	}
}

// TestExposedFlowExecutionIDConflict sends one exposed flow's execution id
// to another exposed flow: it is refused, and the first execution's output
// never reaches the caller.
func TestExposedFlowExecutionIDConflict(t *testing.T) {
	h := startYAML(t, `
namespace: crew
models: {m: {provider: x}}
agents:
  writer: {model: m}
flows:
  write:
    nodes:
      - {id: draft, type: task, agent: writer, prompt: "secret draft about {{.input.prompt}}"}
  review:
    nodes:
      - {id: check, type: task, agent: writer, prompt: "review of {{.input.prompt}}"}
expose:
  owner: newsroom
  flows: [write, review]
`, map[string]llm.LLM{"m": stiggytest.Echo("text: ")})

	ctx := ctxTimeout(t)

	if _, err := h.Client.Start(ctx, "write", map[string]any{"prompt": "nats"},
		packtrail.WithExecutionID("write-1")); err != nil {
		t.Fatal(err)
	}

	handles, err := natsagent.NewClient(h.Conn).Discover(ctx, natsagent.FilterByOwner("newsroom"),
		natsagent.FilterByName("review"))
	if err != nil || len(handles) != 1 {
		t.Fatalf("discover = %v, %v", handles, err)
	}

	s, err := handles[0].Send(ctx, &natsagent.Request{
		Prompt: "it", Header: nats.Header{stiggy.HeaderExecutionID: []string{"write-1"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	got, err := s.Text(ctx)

	var se *natsagent.ServiceError
	if !errors.As(err, &se) || se.Code != http.StatusConflict || se.ErrCode != "execution_conflict" ||
		strings.Contains(got, "secret") {
		t.Fatalf("another flow's id = %q, %v; want a 409 execution_conflict", got, err)
	}

	st, err := h.Client.Get(ctx, "write-1")
	if err != nil || st.Flow != "write" {
		t.Fatalf("execution = %+v, %v", st, err)
	}
}

// TestShutdownFinishesExposedPrompts stops the app while an exposed flow's
// prompt is in flight: workers keep serving until the servers have drained,
// so the execution completes instead of stalling and being cancelled.
func TestShutdownFinishesExposedPrompts(t *testing.T) {
	fleet, _, err := config.Parse([]byte(`
namespace: shut
models: {slow: {provider: x}, fast: {provider: x}}
agents:
  first: {model: slow}
  second: {model: fast}
flows:
  write:
    nodes:
      - {id: a, type: task, agent: first, next: b}
      - {id: b, type: task, agent: second}
expose:
  owner: newsroom
  flows: [write]
`))
	if err != nil {
		t.Fatal(err)
	}

	delete(fleet.Models, "slow")
	delete(fleet.Models, "fast")

	entered, release := make(chan struct{}), make(chan struct{})

	var once sync.Once

	slow := stiggytest.Reply(func(string) string {
		once.Do(func() { close(entered) })
		<-release

		return "one"
	})

	nc := packtrailtest.Start(t).Connect(t)

	app, err := stiggy.New(nc, stiggy.WithFleet(fleet),
		stiggy.WithModel("slow", slow), stiggy.WithModel("fast", stiggytest.Echo("two: ")))
	if err != nil {
		t.Fatal(err)
	}

	runCtx, stop := context.WithCancel(context.Background())
	defer stop()

	done := make(chan error, 1)

	go func() { done <- app.Run(runCtx) }()

	ctx := ctxTimeout(t)

	select {
	case <-app.Ready():
	case <-ctx.Done():
		t.Fatal("not ready")
	}

	handles, err := natsagent.NewClient(nc).Discover(ctx, natsagent.FilterByOwner("newsroom"))
	if err != nil || len(handles) != 1 {
		t.Fatalf("discover = %v, %v", handles, err)
	}

	type answer struct {
		text string
		err  error
	}

	answers := make(chan answer, 1)

	go func() {
		s, perr := handles[0].Prompt(ctx, "go")
		if perr != nil {
			answers <- answer{err: perr}

			return
		}
		defer s.Close()

		text, perr := s.Text(ctx)
		answers <- answer{text, perr}
	}()

	<-entered
	stop()
	time.Sleep(200 * time.Millisecond) // let shutdown begin before step a ends
	close(release)

	select {
	case a := <-answers:
		if a.err != nil || !strings.HasPrefix(a.text, "two: ") {
			t.Fatalf("answer = %q, %v", a.text, a.err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the exposed flow stalled during shutdown")
	}

	if err = <-done; err != nil {
		t.Fatalf("run: %v", err)
	}
}
