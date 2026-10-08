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

// Package e2e_test runs whole fleets against a real model: Ollama with
// llama3.2-eco by default, an embedded JetStream server, and (for knowledge)
// a real Qdrant. The tests are skipped unless STIGGY_E2E=1; run them with
// make e2e, which starts Qdrant first.
//
// A real model's words vary, so the tests check structure (status, visits,
// routing, interrupts, channels, counters) and facts the model can only
// know through stiggy: codes planted in files, knowledge, memory, other
// teams' agents or a human's answers.
//
// Environment:
//   - STIGGY_E2E=1 enables the tests;
//   - STIGGY_E2E_MODEL (default llama3.2-eco);
//   - STIGGY_E2E_OLLAMA_URL (default http://localhost:11434/v1);
//   - STIGGY_E2E_EMBED_MODEL (default nomic-embed-text) and
//     STIGGY_E2E_EMBED_SIZE (default 768);
//   - STIGGY_E2E_QDRANT (default localhost:6334, gRPC).
package e2e_test

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/stiggytest"
)

// runTimeout bounds one test's executions: real models are slow, and
// Ollama cloud answers 429 under load, which the model retries absorb.
const runTimeout = 10 * time.Minute

// models is the model section every fleet shares. Ollama cloud limits
// concurrent requests, so each process sends one at a time and retries.
const models = `
models:
  main:
    provider: ollama
    model: "${STIGGY_E2E_MODEL:-llama3.2-eco}"
    base_url: "${STIGGY_E2E_OLLAMA_URL:-http://localhost:11434/v1}"
    temperature: 0.1
    retries: 5
    rate_limit: {max_concurrent: 1}
`

// live skips the test unless the e2e suite is enabled.
func live(t *testing.T) {
	t.Helper()

	if os.Getenv("STIGGY_E2E") != "1" {
		t.Skip("set STIGGY_E2E=1 (or run make e2e) to run against a real model")
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}

	return def
}

// options parses a fleet (the shared models plus doc) into app options.
func options(t *testing.T, doc string, opts ...stiggy.Option) []stiggy.Option {
	t.Helper()

	f, src, err := config.Parse([]byte(models + doc))
	if err != nil {
		t.Fatalf("fleet:\n%s", strings.Join(src.Describe(err), "\n"))
	}

	return append([]stiggy.Option{stiggy.WithFleet(f), stiggy.WithLogger(quiet())}, opts...)
}

// start runs a fleet on its own embedded server until the test ends.
func start(t *testing.T, doc string, opts ...stiggy.Option) *stiggytest.Harness {
	t.Helper()
	live(t)

	return stiggytest.Start(t, options(t, doc, opts...)...)
}

// serve runs one more app on an existing server (another team, or another
// process of the same deployment) until the test ends.
func serve(t *testing.T, nc *nats.Conn, opts ...stiggy.Option) *stiggy.App {
	t.Helper()

	app, err := stiggy.New(nc, opts...)
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		if rerr := <-done; rerr != nil {
			t.Errorf("run: %v", rerr)
		}
	})

	select {
	case <-app.Ready():
	case err = <-done:
		done <- err

		t.Fatalf("stopped before ready: %v", err)
	case <-time.After(stiggytest.Timeout):
		t.Fatal("not ready in time")
	}

	return app
}

func quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// testCtx is bounded by runTimeout and cancelled when the test ends.
func testCtx(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), runTimeout)
	t.Cleanup(cancel)

	return ctx
}

// run starts flow, waits for it to end and requires it to complete.
func run(t *testing.T, c *packtrail.Client, flow string, input any) *packtrail.State {
	t.Helper()

	ctx := testCtx(t)

	id, err := c.Start(ctx, flow, input)
	if err != nil {
		t.Fatalf("start %s: %v", flow, err)
	}

	st, err := c.Wait(ctx, id)
	if err != nil {
		t.Fatalf("wait %s: %v", id, err)
	}

	completed(t, st)

	return st
}

// completed requires st to have completed, and logs what happened.
func completed(t *testing.T, st *packtrail.State) {
	t.Helper()

	report(t, st)

	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("%s %s: %s (%s) at %q", st.Flow, st.ExecID, st.Status, st.Reason+" "+st.Error, st.FailedNode)
	}
}

// report logs an execution's shape and output, so a live run can be read.
func report(t *testing.T, st *packtrail.State) {
	t.Helper()

	var b strings.Builder

	for node, raw := range st.Results {
		fmt.Fprintf(&b, "\n  %s: %s", node, clip(text(raw)))
	}

	t.Logf("%s %s: %s, visits %v, agent_steps %v, tokens in/out %v/%v\nresults:%s\noutput: %s",
		st.Flow, st.ExecID, st.Status, st.Visits, st.Counters["agent_steps"],
		st.Counters["tokens_in"], st.Counters["tokens_out"], b.String(), text(st.Output))
}

// clip shortens s to one line of at most 300 bytes, for logs.
func clip(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 300 {
		return s[:300] + "..."
	}

	return s
}

// text returns the answer of a plain agent output ({"text": ...}), or the
// raw JSON of any other output.
func text(raw []byte) string {
	var o struct{ Text string }
	if json.Unmarshal(raw, &o) == nil && o.Text != "" {
		return o.Text
	}

	return string(raw)
}

// result returns the text answer of node.
func result(t *testing.T, st *packtrail.State, node string) string {
	t.Helper()

	raw, ok := st.Results[node]
	if !ok {
		t.Fatalf("no result for %q (results: %v)", node, keys(st.Results))
	}

	return text(raw)
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}

	return out
}

// mustContain fails unless got contains every want, ignoring case and the
// formatting a model adds (spaces, dashes, markdown).
func mustContain(t *testing.T, what, got string, wants ...string) {
	t.Helper()

	norm := func(s string) string {
		return strings.Map(func(r rune) rune {
			if r == ' ' || r == '-' || r == '*' || r == '`' || r == '_' {
				return -1
			}

			return r
		}, strings.ToLower(s))
	}

	for _, w := range wants {
		if !strings.Contains(norm(got), norm(w)) {
			t.Fatalf("%s lacks %q:\n%s", what, w, got)
		}
	}
}

// interrupt is what a step paused for a human waits for.
type interrupt struct {
	Node     string
	Kind     string
	Question string
	Answer   json.RawMessage
}

// waitPaused waits until the execution pauses for a human, and returns
// what it waits for, or nil once the execution ends. Only states past
// sequence after count, so a step just resumed is not seen paused again.
func waitPaused(t *testing.T, c *packtrail.Client, id string, after uint64) (*packtrail.State, *interrupt) {
	t.Helper()

	st, err := c.WaitUntil(testCtx(t), id, func(s *packtrail.State) bool {
		return s.LastSeq > after && pending(s) != nil
	})
	if err != nil && (st == nil || !st.Status.Terminal()) {
		t.Fatalf("waiting for %s: %v", id, err)
	}

	return st, pending(st)
}

func pending(st *packtrail.State) *interrupt {
	for _, task := range st.Tasks {
		if task.Status != "interrupted" {
			continue
		}

		in := interrupt{Node: task.Node}
		if json.Unmarshal(task.Interrupt, &in) == nil {
			return &in
		}
	}

	return nil
}

// fleetName makes names unique per run where external state outlives the
// test (Qdrant collections).
func fleetName(prefix string) string {
	return fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
}
