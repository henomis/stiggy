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

package dashboard_test

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/internal/dashboard"
	"github.com/henomis/stiggy/stiggytest"
)

const fleetDoc = `
namespace: newsroom
models: {m: {provider: x}}
agents:
  researcher: {model: m}
  writer: {model: m}
flows:
  article:
    budget: {tokens_out: 100000}
    nodes:
      - {id: research, type: task, agent: researcher, next: write}
      - {id: write, type: task, agent: writer, human_input: true}
`

func start(t *testing.T) *stiggytest.Harness {
	t.Helper()

	fleet, src, err := config.Parse([]byte(fleetDoc))
	if err != nil {
		t.Fatalf("%s", strings.Join(src.Describe(err), "\n"))
	}

	delete(fleet.Models, "m")

	return stiggytest.Start(t, stiggy.WithFleet(fleet),
		stiggy.WithModel("m", stiggytest.Reply(func(p string) string { return "notes on " + p })))
}

func ctxOf(t *testing.T) context.Context {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), stiggytest.Timeout)
	t.Cleanup(cancel)

	return ctx
}

func call(t *testing.T, srv *httptest.Server, method, path, body string, out any) int {
	t.Helper()

	req, err := http.NewRequestWithContext(ctxOf(t), method, srv.URL+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}

	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	if out != nil && res.StatusCode == http.StatusOK {
		if err = json.NewDecoder(res.Body).Decode(out); err != nil {
			t.Fatalf("%s %s: %v", method, path, err)
		}
	}

	return res.StatusCode
}

func waitFor(t *testing.T, h *stiggytest.Harness, id string, cond func(*packtrail.State) bool) {
	t.Helper()

	if _, err := h.Client.WaitUntil(ctxOf(t), id, cond); err != nil {
		t.Fatal(err)
	}
}

// TestDashboard runs a fleet with a reviewed step and follows one execution
// through the API: waiting for review, answered, completed.
func TestDashboard(t *testing.T) {
	h := start(t)
	srv := httptest.NewServer(dashboard.New(h.Conn).Handler())
	t.Cleanup(srv.Close)

	id, err := h.Client.Start(ctxOf(t), "article", map[string]any{"topic": "ants"})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, h, id, func(st *packtrail.State) bool { return st.Status == packtrail.StatusWaiting })

	var ov dashboard.Overview
	if code := call(t, srv, http.MethodGet, "/api/overview", "", &ov); code != http.StatusOK {
		t.Fatalf("overview: %d", code)
	}

	if len(ov.Namespaces) != 1 || ov.Namespaces[0].Name != "newsroom" || ov.Namespaces[0].Total != 1 {
		t.Fatalf("namespaces = %+v", ov.Namespaces)
	}

	if len(ov.Waiting) != 1 || ov.Waiting[0].ExecID != id || ov.Waiting[0].Waits[0].Kind != "review" ||
		!strings.HasPrefix(ov.Waiting[0].Waits[0].Text, "notes on") {
		t.Fatalf("waiting = %+v", ov.Waiting)
	}

	var ex dashboard.Execution
	if code := call(t, srv, http.MethodGet, "/api/ns/newsroom/executions/"+id, "", &ex); code != http.StatusOK {
		t.Fatalf("execution: %d", code)
	}

	if ex.Graph == nil || len(ex.Graph.Nodes) != 2 || ex.Graph.Nodes[0].Runs != "researcher" ||
		!ex.Graph.Nodes[1].Human || len(ex.Graph.Edges) != 1 {
		t.Fatalf("graph = %+v", ex.Graph)
	}

	if ex.Nodes["research"].State != dashboard.StateDone || ex.Nodes["write"].State != dashboard.StateWaiting {
		t.Fatalf("nodes = %+v", ex.Nodes)
	}

	if ex.Usage == nil || ex.Usage.TokensIn == 0 || len(ex.Usage.ByAgent) != 2 {
		t.Fatalf("usage = %+v", ex.Usage)
	}

	if len(ex.Input) != 1 || ex.Input[0].Key != "topic" || ex.Input[0].Text != "ants" {
		t.Fatalf("input = %+v", ex.Input)
	}

	if len(ex.Budget) != 1 || ex.Budget[0].Counter != "tokens_out" || ex.Budget[0].Used == 0 {
		t.Fatalf("budget = %+v", ex.Budget)
	}

	body := `{"node": "write", "approve": true, "by": "editor"}`
	if code := call(t, srv, http.MethodPost, "/api/ns/newsroom/executions/"+id+"/resume", body, nil); code != http.StatusOK {
		t.Fatalf("resume: %d", code)
	}

	waitFor(t, h, id, func(st *packtrail.State) bool { return st.Status == packtrail.StatusCompleted })

	var tl dashboard.Timeline
	if code := call(t, srv, http.MethodGet, "/api/ns/newsroom/executions/"+id+"/timeline", "", &tl); code != http.StatusOK {
		t.Fatalf("timeline: %d", code)
	}

	assertHumanSpan(t, tl)

	var list struct{ Executions []dashboard.Summary }
	if code := call(t, srv, http.MethodGet, "/api/ns/newsroom/executions?status=completed", "", &list); code != http.StatusOK ||
		len(list.Executions) != 1 || list.Executions[0].Usage == nil {
		t.Fatalf("list: %d %+v", code, list)
	}
}

func assertHumanSpan(t *testing.T, tl dashboard.Timeline) {
	t.Helper()

	var human *dashboard.Span

	for i := range tl.Spans {
		if tl.Spans[i].Outcome == dashboard.SpanHuman {
			human = &tl.Spans[i]
		}
	}

	if human == nil || human.Node != "write" || human.By != "editor" || human.End.IsZero() || tl.End.IsZero() {
		t.Fatalf("timeline = %+v", tl)
	}
}

// TestDashboardRefuses checks the requests the dashboard turns down.
func TestDashboardRefuses(t *testing.T) {
	h := start(t)

	ro := httptest.NewServer(dashboard.New(h.Conn, dashboard.WithReadOnly()).Handler())
	t.Cleanup(ro.Close)

	rw := httptest.NewServer(dashboard.New(h.Conn, dashboard.WithNamespaces("other")).Handler())
	t.Cleanup(rw.Close)

	body := `{"node": "write", "approve": true}`

	if code := call(t, ro, http.MethodPost, "/api/ns/newsroom/executions/x/resume", body, nil); code != http.StatusForbidden {
		t.Fatalf("read-only resume: %d", code)
	}

	if code := call(t, rw, http.MethodGet, "/api/ns/newsroom/executions", "", nil); code != http.StatusNotFound {
		t.Fatalf("namespace not allowed: %d", code)
	}

	open := httptest.NewServer(dashboard.New(h.Conn).Handler())
	t.Cleanup(open.Close)

	req, _ := http.NewRequestWithContext(ctxOf(t), http.MethodPost,
		open.URL+"/api/ns/newsroom/executions/x/cancel", strings.NewReader("reason=x"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	_ = res.Body.Close()

	if res.StatusCode != http.StatusUnsupportedMediaType {
		t.Fatalf("form post: %d", res.StatusCode)
	}

	if code := call(t, open, http.MethodPost, "/api/ns/newsroom/executions/x/resume", `{"node": "write"}`, nil); code != http.StatusBadRequest {
		t.Fatalf("empty resume: %d", code)
	}
}

// TestLive checks an execution's end reaches the live stream.
func TestLive(t *testing.T) {
	h := start(t)
	srv := httptest.NewServer(dashboard.New(h.Conn).Handler())
	t.Cleanup(srv.Close)

	req, _ := http.NewRequestWithContext(ctxOf(t), http.MethodGet, srv.URL+"/api/live", nil)

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()

	// Give the watch a moment to subscribe before the execution ends.
	time.Sleep(200 * time.Millisecond)

	id, err := h.Client.Start(ctxOf(t), "article", map[string]any{"topic": "bees"})
	if err != nil {
		t.Fatal(err)
	}

	waitFor(t, h, id, func(st *packtrail.State) bool { return st.Status == packtrail.StatusWaiting })

	if err = h.Client.Cancel(ctxOf(t), id, "test"); err != nil {
		t.Fatal(err)
	}

	sc := bufio.NewScanner(res.Body)
	for sc.Scan() {
		data, ok := strings.CutPrefix(sc.Text(), "data: ")
		if !ok {
			continue
		}

		var e dashboard.Ended
		if err = json.Unmarshal([]byte(data), &e); err != nil {
			t.Fatal(err)
		}

		if e.ExecID == id && e.Status == "cancelled" && e.Namespace == "newsroom" {
			return
		}
	}

	t.Fatalf("stream ended without the execution: %v", sc.Err())
}
