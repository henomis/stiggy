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
)

const fixture = "../../internal/design/project/testdata/story.stiggy.json"

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()

	var out, errOut bytes.Buffer

	err := run(context.Background(), args, &out, &errOut)

	return out.String() + errOut.String(), err
}

func TestArgs(t *testing.T) {
	if _, err := runCLI(t, "a.json", "b.json"); !errors.Is(err, errUsage) {
		t.Fatalf("two files: %v", err)
	}

	if _, err := runCLI(t, "-ui", filepath.Join(t.TempDir(), "missing")); err == nil ||
		!strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("missing -ui: %v", err)
	}

	if out, err := runCLI(t, "-version"); err != nil || !strings.HasPrefix(out, "stiggy-design ") {
		t.Fatalf("version: %q %v", out, err)
	}
}

func TestFleetPath(t *testing.T) {
	for in, want := range map[string]string{
		"news.stiggy.json": "news.yaml",
		"dir/news.json":    "dir/news.yaml",
		"news":             "news.yaml",
	} {
		if got := fleetPath(in); got != want {
			t.Errorf("fleetPath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestServesEmbeddedPage checks the binary serves the built page it embeds.
func TestServesEmbeddedPage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	done := make(chan error, 1)

	go func() { done <- run(ctx, []string{"-addr", "127.0.0.1:0", fixture}, pw, io.Discard) }()

	line := make([]byte, 128)
	n, _ := pr.Read(line)
	url := strings.TrimSpace(strings.TrimPrefix(string(line[:n]), "stiggy-design: "))

	go func() { _, _ = io.Copy(io.Discard, pr) }()

	client := &http.Client{Timeout: 5 * time.Second}

	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, url+"/", nil)

	res, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}

	body, _ := io.ReadAll(res.Body)
	_ = res.Body.Close()

	if res.StatusCode != http.StatusOK || !strings.Contains(string(body), "<div id=\"root\">") {
		t.Fatalf("page: %d %.200s", res.StatusCode, body)
	}

	cancel()

	if err = <-done; err != nil {
		t.Fatal(err)
	}
}
