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

package design_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/henomis/stiggy/internal/design"
	"github.com/henomis/stiggy/registry"
)

const story = "project/testdata/story.stiggy.json"

func call(t *testing.T, h http.Handler, method, path, body string) (int, string) {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(body))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	b, _ := io.ReadAll(rec.Body)

	return rec.Code, string(b)
}

func decode[T any](t *testing.T, body string) T {
	t.Helper()

	var v T
	if err := json.Unmarshal([]byte(body), &v); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}

	return v
}

func read(t *testing.T, path string) string {
	t.Helper()

	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	return string(b)
}

func TestCatalog(t *testing.T) {
	h := design.New(registry.New(), design.WithActivities("fetch")).Handler()

	code, body := call(t, h, http.MethodGet, "/api/catalog", "")
	if code != http.StatusOK {
		t.Fatalf("code %d: %s", code, body)
	}

	c := decode[design.Catalog](t, body)
	if !slices.Contains(c.Providers, "openai") || !slices.Contains(c.Activities, "fetch") ||
		len(c.ToolTypes) == 0 || len(c.MemoryTypes) == 0 || len(c.EmbedderProviders) == 0 ||
		len(c.VectorStoreTypes) == 0 {
		t.Fatalf("catalog %+v", c)
	}
}

func TestCompile(t *testing.T) {
	h := design.New(registry.New()).Handler()

	_, body := call(t, h, http.MethodPost, "/api/compile", read(t, story))

	c := decode[design.Compiled](t, body)
	if !c.OK || !strings.Contains(c.YAML, "flows:") {
		t.Fatalf("compile: %+v", c)
	}

	// A provider stiggy does not know: stiggy's problem, mapped to the block.
	broken := strings.Replace(read(t, story), `"provider": "openai"`, `"provider": "nope"`, 1)

	_, body = call(t, h, http.MethodPost, "/api/compile", broken)

	c = decode[design.Compiled](t, body)
	if c.OK || len(c.Problems) == 0 {
		t.Fatalf("want problems: %+v", c)
	}

	if p := c.Problems[0]; p.Workflow != "w1" || p.Node == "" || p.Field != "credential" || p.Path == "" {
		t.Fatalf("problem not located: %+v", p)
	}

	if code, _ := call(t, h, http.MethodPost, "/api/compile", `{"version": 9}`); code != http.StatusBadRequest {
		t.Fatalf("bad project: %d", code)
	}
}

func TestProjectFile(t *testing.T) {
	dir := t.TempDir()
	path, out := filepath.Join(dir, "news.stiggy.json"), filepath.Join(dir, "news.yaml")

	h := design.New(registry.New(), design.WithProject(path, out)).Handler()

	code, body := call(t, h, http.MethodGet, "/api/project", "")
	if f := decode[design.File](t, body); code != http.StatusOK || f.Project != nil || f.Output != out {
		t.Fatalf("new project: %d %s", code, body)
	}

	code, body = call(t, h, http.MethodPut, "/api/project", read(t, story))
	if s := decode[design.Saved](t, body); code != http.StatusOK || !s.OK {
		t.Fatalf("save: %d %s", code, body)
	}

	if !strings.Contains(read(t, out), "namespace: newsroom") {
		t.Fatalf("fleet not written:\n%s", read(t, out))
	}

	code, body = call(t, h, http.MethodGet, "/api/project", "")
	if f := decode[design.File](t, body); code != http.StatusOK || f.Project == nil || f.Project.Name != "Newsroom" {
		t.Fatalf("reload: %d %s", code, body)
	}

	none := design.New(registry.New()).Handler()
	if code, _ = call(t, none, http.MethodGet, "/api/project", ""); code != http.StatusNotFound {
		t.Fatalf("no project: %d", code)
	}
}

func TestPage(t *testing.T) {
	code, body := call(t, design.New(registry.New()).Handler(), http.MethodGet, "/", "")
	if code != http.StatusOK || !strings.Contains(body, "make ui") {
		t.Fatalf("placeholder: %d %s", code, body)
	}

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("designer"), 0o600); err != nil {
		t.Fatal(err)
	}

	code, body = call(t, design.New(registry.New(), design.WithUI(dir)).Handler(), http.MethodGet, "/", "")
	if code != http.StatusOK || body != "designer" {
		t.Fatalf("ui: %d %s", code, body)
	}
}
