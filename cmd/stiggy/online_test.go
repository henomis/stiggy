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

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/henomis/packtrail/packtrailtest"
)

// fakeOpenAI answers chat completions with "answer to: <last user message>".
func fakeOpenAI(t *testing.T) *httptest.Server {
	t.Helper()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}

		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		last := ""

		for _, m := range req.Messages {
			if m.Role == "user" {
				last = m.Content
			}
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "c1", "object": "chat.completion", "model": "fake",
			"choices": []any{map[string]any{
				"index": 0, "finish_reason": "stop",
				"message": map[string]any{"role": "assistant", "content": "answer to: " + last},
			}},
			"usage": map[string]any{"prompt_tokens": 7, "completion_tokens": 3, "total_tokens": 10},
		})
	}))
	t.Cleanup(srv.Close)

	return srv
}

// syncBuffer is a bytes.Buffer safe for the concurrent writes of a logger.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.b.String()
}

// TestRunAndStart drives the binary end to end: `stiggy run` serves a YAML
// fleet whose model is an OpenAI-compatible endpoint, and `stiggy start
// -wait` runs a flow through it.
func TestRunAndStart(t *testing.T) {
	nats := packtrailtest.Start(t)
	llmSrv := fakeOpenAI(t)

	fleet := `
namespace: cli
models:
  local: {provider: ollama, model: fake, base_url: ` + llmSrv.URL + `}
agents:
  helper: {model: local, role: Helper}
flows:
  ask:
    nodes:
      - {id: answer, type: task, agent: helper, prompt: "{{.input.q}}"}
expose:
  agents: [helper]
`

	path := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(path, []byte(fleet), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	logs := &syncBuffer{}
	health := freeAddr(t)

	go func() {
		done <- run(ctx, []string{"run", "-server", nats.URL(), "-http", health, path}, &bytes.Buffer{}, logs)
	}()

	// start fails until the engine has provisioned the namespace.
	var out string

	deadline := time.Now().Add(30 * time.Second)

	for {
		var err error

		out, _, err = runCLI(t, "start", "-server", nats.URL(), "-ns", "cli", "ask", "-input", `{"q":"ping"}`, "-wait")
		if err == nil {
			break
		}

		if time.Now().After(deadline) {
			t.Fatalf("start never succeeded: %v\nrun logs:\n%s", err, logs)
		}

		time.Sleep(200 * time.Millisecond)
	}

	var st struct {
		ExecID   string `json:"exec_id"`
		Status   string
		Output   struct{ Text string }
		Counters map[string]float64
	}

	if err := json.Unmarshal([]byte(out), &st); err != nil {
		t.Fatalf("%v: %s", err, out)
	}

	if st.Status != "completed" || st.Output.Text != "answer to: ping" || st.Counters["tokens_in"] != 7 {
		t.Fatalf("state = %+v", st)
	}

	if code := httpStatus(t, "http://"+health+"/readyz"); code != http.StatusOK {
		t.Errorf("/readyz = %d once serving", code)
	}

	got, _, err := runCLI(t, "get", "-server", nats.URL(), "-ns", "cli", st.ExecID)
	if err != nil || !strings.Contains(got, `"status": "completed"`) {
		t.Fatalf("get: %v\n%s", err, got)
	}

	hist, _, err := runCLI(t, "history", "-server", nats.URL(), "-ns", "cli", st.ExecID)
	if err != nil || !strings.Contains(hist, "ExecutionCompleted") {
		t.Fatalf("history: %v\n%s", err, hist)
	}

	rerun, _, err := runCLI(t, "rerun", "-server", nats.URL(), "-ns", "cli", st.ExecID, "answer")
	if err != nil || strings.TrimSpace(rerun) == "" {
		t.Fatalf("rerun: %v %q", err, rerun)
	}

	agents, _, err := runCLI(t, "agents", "-server", nats.URL(), "-owner", "cli")
	if err != nil || !strings.Contains(agents, "helper") || !strings.Contains(agents, "stiggy") {
		t.Fatalf("agents: %v\n%s", err, agents)
	}

	cancel()

	if rerr := <-done; rerr != nil {
		t.Fatalf("run: %v\n%s", rerr, logs)
	}

	if !strings.Contains(logs.String(), "stiggy: serving") {
		t.Errorf("no readiness log:\n%s", logs)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	return ln.Addr().String()
}

func httpStatus(t *testing.T, url string) int {
	t.Helper()

	resp, err := http.Get(url) //nolint:noctx // test helper
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	return resp.StatusCode
}

func TestRunRejectsUnknownActivity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fleet.yaml")
	doc := "flows:\n  f:\n    nodes:\n      - {id: s, type: task, activity: publish}\n"

	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	nats := packtrailtest.Start(t)

	_, stderr, err := runCLI(t, "run", "-server", nats.URL(), path)
	if err == nil || !strings.Contains(stderr, `unknown activity "publish"`) {
		t.Fatalf("err = %v, stderr = %s", err, stderr)
	}
}
