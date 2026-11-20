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
	"errors"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/henomis/packtrail/packtrailtest"
)

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	err := run(context.Background(), args, &out, &errOut)

	return out.String() + errOut.String(), err
}

func TestArgs(t *testing.T) {
	if _, err := runCLI(t, "extra"); !errors.Is(err, errUsage) {
		t.Fatalf("positional: %v", err)
	}

	if _, err := runCLI(t, "-ns", "bad.name"); err == nil || !strings.Contains(err.Error(), "-ns") {
		t.Fatalf("bad -ns: %v", err)
	}

	if _, err := runCLI(t, "-ui", filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("missing -ui: %v", err)
	}

	if _, err := runCLI(t, "-log-level", "loud"); err == nil || !strings.Contains(err.Error(), "log") {
		t.Fatalf("bad log level: %v", err)
	}

	if out, err := runCLI(t, "-version"); err != nil || !strings.HasPrefix(out, "stiggy-dashboard ") {
		t.Fatalf("version: %q %v", out, err)
	}
}

// TestServesEmbeddedPage checks the binary serves the built page it embeds,
// and its API, against a real server.
func TestServesEmbeddedPage(t *testing.T) {
	srv := packtrailtest.Start(t)

	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() { done <- run(ctx, []string{"-addr", "127.0.0.1:0", "-server", srv.URL()}, pw, io.Discard) }()

	line := make([]byte, 128)
	n, _ := pr.Read(line)
	url := strings.TrimSpace(strings.TrimPrefix(string(line[:n]), "stiggy-dashboard: "))

	go func() { _, _ = io.Copy(io.Discard, pr) }()

	client := &http.Client{Timeout: 5 * time.Second}

	for path, want := range map[string]string{
		"/":                  "<div id=\"root\">",
		"/ns/x/executions/y": "<div id=\"root\">", // the page's own routes fall back to it
		"/api/overview":      "\"namespaces\":[]",
	} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+path, nil)

		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}

		body, _ := io.ReadAll(res.Body)
		_ = res.Body.Close()

		if res.StatusCode != http.StatusOK || !strings.Contains(string(body), want) {
			t.Errorf("%s: %d %.200s", path, res.StatusCode, body)
		}
	}

	cancel()

	if err := <-done; err != nil {
		t.Fatal(err)
	}
}
