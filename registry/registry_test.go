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

package registry_test

import (
	"context"
	"errors"
	"testing"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/tool/bash"

	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

func TestBuiltinTools(t *testing.T) {
	r := registry.New()
	dir := t.TempDir()

	for typ, opts := range map[string]map[string]any{
		"bash": {
			"allowlist": []any{"ls"}, "timeout": "5s", "working_dir": dir,
			"env": map[string]any{"A": "1"}, "env_allowlist": []any{"PATH"}, "background_timeout": "1m",
		},
		"file_read":       {"working_dir": dir, "read_only_roots": []any{dir}, "max_output_chars": 100},
		"file_write":      {"working_dir": dir, "no_overwrite": true},
		"file_edit":       nil,
		"file_multi_edit": {"working_dir": dir},
		"glob":            {"max_results": 10, "no_ignore": true},
		"grep":            {"max_file_size": 1024},
		"skill":           {"roots": []any{dir, t.TempDir()}},
		"web_fetch":       {"allowed_hosts": []any{"*.go.dev"}, "timeout": "5s", "max_bytes": 1 << 20},
	} {
		tool := spec.Tool{Type: typ, Options: opts}

		if err := r.ValidateTool(tool); err != nil {
			t.Errorf("%s: validate: %v", typ, err)
		}

		tools, err := r.BuildTool(context.Background(), "t", tool, registry.Env{})
		if err != nil || len(tools) == 0 {
			t.Errorf("%s: build: %v (%d tools)", typ, err, len(tools))
		}
	}
}

func TestToolOptionsAreStrict(t *testing.T) {
	r := registry.New()

	for _, tool := range []spec.Tool{
		{Type: "bash", Options: map[string]any{"allowlst": []any{"ls"}}},
		{Type: "bash", Options: map[string]any{"timeout": "soon"}},
		{Type: "file_read", Options: map[string]any{"no_overwrite": true}},
		{Type: "bash", Options: map[string]any{"sandbox": map[string]any{"type": "bubblewrap", "net": true}}},
		{Type: "web_fetch", Options: map[string]any{"allowed": []any{"x"}}},
	} {
		if err := r.ValidateTool(tool); !errors.Is(err, registry.ErrOptions) {
			t.Errorf("%+v: err = %v", tool, err)
		}
	}

	for _, tool := range []spec.Tool{
		{Type: "kv", Options: map[string]any{"bucket": "b"}},
		{Type: "skill"},
		{Type: "bash", Options: map[string]any{"sandbox": map[string]any{"type": "docker", "workspace": "/w"}}},
		{Type: "bash", Options: map[string]any{"sandbox": map[string]any{"type": "bubblewrap"}}},
	} {
		if _, err := r.BuildTool(context.Background(), "t", tool, registry.Env{}); !errors.Is(err, registry.ErrOptions) {
			t.Errorf("%+v: err = %v", tool, err)
		}
	}
}

// TestBashSandbox checks a sandboxed bash tool builds where bubblewrap is
// installed, and fails with phero's error where it is not.
func TestBashSandbox(t *testing.T) {
	tool := spec.Tool{Type: "bash", Options: map[string]any{
		"working_dir": t.TempDir(),
		"sandbox":     map[string]any{"type": "bubblewrap", "read_only": []any{t.TempDir()}},
	}}

	var cleanups int

	env := registry.Env{Defer: func(func() error) { cleanups++ }}

	_, err := registry.New().BuildTool(context.Background(), "sh", tool, env)

	switch {
	case errors.Is(err, bash.ErrBubblewrapNotFound):
		t.Skip("bubblewrap is not installed")
	case err != nil:
		t.Fatal(err)
	case cleanups != 1:
		t.Fatalf("cleanups = %d, want the tool's Close", cleanups)
	}
}

func TestModels(t *testing.T) {
	r := registry.New()
	temp := 0.2
	lax := false

	for _, m := range []spec.Model{
		{Provider: "openai", Model: "gpt-4o-mini", APIKey: "k", Temperature: &temp, StrictTools: &lax},
		{Provider: "anthropic", MaxTokens: 1000, StrictTools: &lax, PromptCacheTTL: "1h"},
		{Provider: "google", Model: "gemini-3.5-flash", APIKey: "k", MaxTokens: 1000, Temperature: &temp},
		{Provider: "ollama", Model: "llama3", StrictTools: &lax},
	} {
		if _, err := r.BuildModel("m", m); err != nil {
			t.Errorf("%s: %v", m.Provider, err)
		}
	}

	// Each provider rejects the settings it cannot honour.
	for _, m := range []spec.Model{
		{Provider: "openai", PromptCacheTTL: "5m"},
		{Provider: "ollama", Model: "x", PromptCacheTTL: "5m"},
		{Provider: "google", StrictTools: &lax},
		{Provider: "google", PromptCacheTTL: "5m"},
	} {
		if _, err := r.BuildModel("m", m); !errors.Is(err, registry.ErrOptions) {
			t.Errorf("%s %+v: err = %v", m.Provider, m, err)
		}
	}

	for _, p := range []string{"openai", "ollama"} {
		if _, err := r.BuildModel("m", spec.Model{Provider: p, Model: "x", MaxTokens: 10}); err != nil {
			t.Errorf("%s max_tokens: %v", p, err)
		}
	}

	if _, err := r.BuildModel("m", spec.Model{Provider: "ollama"}); !errors.Is(err, registry.ErrOptions) {
		t.Errorf("ollama without a model: err = %v", err)
	}

	if _, err := r.BuildModel("m", spec.Model{Provider: "nope"}); !errors.Is(err, registry.ErrUnknown) {
		t.Errorf("unknown provider: err = %v", err)
	}

	inst := llm.Func(func(context.Context, []llm.Message, ...llm.CallOption) (*llm.Result, error) { return nil, nil })
	if l, err := r.BuildModel("m", spec.Model{Instance: inst}); err != nil || l == nil {
		t.Errorf("instance: %v", err)
	}
}

func TestRegister(t *testing.T) {
	r := registry.Empty()
	h := func(context.Context, *worker.Job) (*worker.Result, error) { return &worker.Result{}, nil }

	if err := r.RegisterActivity("a", h); err != nil {
		t.Fatal(err)
	}

	if err := r.RegisterActivity("a", h); !errors.Is(err, registry.ErrDuplicate) {
		t.Errorf("duplicate: %v", err)
	}

	if !r.HasActivity("a") || r.HasProvider("openai") || r.HasToolType("bash") {
		t.Error("an empty registry must only know what was registered")
	}

	custom := registry.NewToolType(func(_ context.Context, _ struct{ N int }, _ registry.Env) ([]*llm.Tool, error) {
		return []*llm.Tool{}, nil
	})

	if err := r.RegisterToolType("custom", custom); err != nil {
		t.Fatal(err)
	}

	if err := r.ValidateTool(spec.Tool{Type: "custom", Options: map[string]any{"N": "x"}}); err == nil {
		t.Error("a wrongly typed option must fail validation")
	}
}
