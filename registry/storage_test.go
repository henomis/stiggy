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

	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/rag"

	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

func memEnv(t *testing.T, env registry.Env, session string) registry.MemoryEnv {
	t.Helper()

	return registry.MemoryEnv{
		Env:     env,
		Session: session,
		Model: func(string) (llm.LLM, error) {
			return llm.Func(func(context.Context, []llm.Message, ...llm.CallOption) (*llm.Result, error) {
				return nil, errors.New("unused")
			}), nil
		},
		Knowledge: func(string) (*rag.RAG, error) { return nil, errors.New("no knowledge") },
	}
}

func TestBuiltinMemories(t *testing.T) {
	r := registry.New()
	ctx := context.Background()
	srv := packtrailtest.Start(t)
	env := registry.Env{Conn: srv.Connect(t)}
	sum := &spec.Summarize{Model: "m", Threshold: 20, Size: 5}

	for _, m := range []spec.Memory{
		{Type: "simple", Options: map[string]any{"max_items": 10}, Summarize: sum},
		{Type: "jsonfile", Options: map[string]any{"dir": t.TempDir()}},
		{Type: "nats", Options: map[string]any{"bucket": "stiggy-test-mem"}, Summarize: sum},
	} {
		if err := r.ValidateMemory(m); err != nil {
			t.Errorf("%s: validate: %v", m.Type, err)
		}

		mem, err := r.BuildMemory(ctx, m, memEnv(t, env, "agent-1"))
		if err != nil {
			t.Fatalf("%s: build: %v", m.Type, err)
		}

		if err = mem.Save(ctx, []llm.Message{llm.UserMessage(llm.Text("hi"))}); err != nil {
			t.Fatalf("%s: save: %v", m.Type, err)
		}

		got, err := mem.Retrieve(ctx, "")
		if err != nil || len(got) != 1 {
			t.Fatalf("%s: retrieve = %v, %v", m.Type, got, err)
		}
	}
}

func TestMemoryOptionErrors(t *testing.T) {
	r := registry.New()
	env := memEnv(t, registry.Env{}, "s")

	for _, m := range []spec.Memory{
		{Type: "simple", Options: map[string]any{"max_itms": 1}},
		{Type: "jsonfile"},
		{Type: "nats"},
		{Type: "psql"},
		{Type: "rag"},
	} {
		_, err := r.BuildMemory(context.Background(), m, env)
		if !errors.Is(err, registry.ErrOptions) {
			t.Errorf("%s %v: err = %v", m.Type, m.Options, err)
		}
	}
}

func TestVectorStoreAndEmbedderOptions(t *testing.T) {
	r := registry.New()

	for _, v := range []spec.VectorStore{
		{Type: "qdrant", Options: map[string]any{"host": "localhost", "collection": "c", "distance": "dot"}},
		{Type: "psql", Options: map[string]any{"dsn": "postgres://x", "collection": "c"}},
		{Type: "weaviate", Options: map[string]any{"host": "localhost:8080", "class": "Doc"}},
	} {
		if err := r.ValidateVectorStore(v); err != nil {
			t.Errorf("%s: %v", v.Type, err)
		}
	}

	if err := r.ValidateVectorStore(spec.VectorStore{Type: "qdrant", Options: map[string]any{"colection": "c"}}); err == nil {
		t.Error("an unknown qdrant option must fail validation")
	}

	if _, err := r.BuildVectorStore(context.Background(), "v",
		spec.VectorStore{Type: "qdrant", Options: map[string]any{"host": "h", "collection": "c", "distance": "far"}},
		registry.Env{}); !errors.Is(err, registry.ErrOptions) {
		t.Errorf("bad distance: %v", err)
	}

	for _, e := range []spec.Embedder{{Provider: "openai", APIKey: "k"}, {Provider: "ollama", Model: "nomic-embed-text"}} {
		if _, err := r.BuildEmbedder("e", e); err != nil {
			t.Errorf("%s: %v", e.Provider, err)
		}
	}

	if _, err := r.BuildEmbedder("e", spec.Embedder{Provider: "ollama"}); !errors.Is(err, registry.ErrOptions) {
		t.Errorf("ollama without model: %v", err)
	}
}
