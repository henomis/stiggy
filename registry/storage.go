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

package registry

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"

	"github.com/henomis/phero/v2/embedding"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/memory"
	"github.com/henomis/phero/v2/rag"
	"github.com/henomis/phero/v2/vectorstore"

	"github.com/henomis/stiggy/spec"
)

// MemoryEnv is what memory factories may use besides their options.
type MemoryEnv struct {
	Env

	// Session is the memory session: the spec's session (default: the agent
	// name), with the execution id appended when per_execution is set.
	Session string
	// Model returns a fleet model, for summarization.
	Model func(name string) (llm.LLM, error)
	// Knowledge returns a fleet knowledge index, for "rag" memory.
	Knowledge func(name string) (*rag.RAG, error)
}

// Summarization returns the summarization model and settings of m, nil when
// it has none.
func (e MemoryEnv) Summarization(m spec.Memory) (llm.LLM, *spec.Summarize, error) {
	if m.Summarize == nil {
		return nil, nil, nil
	}

	l, err := e.Model(m.Summarize.Model)
	if err != nil {
		return nil, nil, err
	}

	return l, m.Summarize, nil
}

// MemoryType builds a phero memory backend. Create one with [NewMemoryType].
type MemoryType struct {
	validate func(opts map[string]any) error
	build    func(ctx context.Context, m spec.Memory, env MemoryEnv) (memory.Memory, error)
}

// NewMemoryType returns a memory type whose type-specific options decode
// strictly into O.
func NewMemoryType[O any](build func(ctx context.Context, opts O, m spec.Memory, env MemoryEnv) (memory.Memory, error),
) MemoryType {
	return MemoryType{
		validate: func(raw map[string]any) error {
			_, err := decodeStrict[O](raw)

			return err
		},
		build: func(ctx context.Context, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
			o, err := decodeStrict[O](m.Options)
			if err != nil {
				return nil, err
			}

			return build(ctx, o, m, env)
		},
	}
}

// EmbedderFactory builds an embedding client from its spec.
type EmbedderFactory func(e spec.Embedder) (embedding.Embedder, error)

// VectorStoreType builds a vector store. Create one with [NewVectorStoreType].
type VectorStoreType struct {
	validate func(opts map[string]any) error
	build    func(ctx context.Context, opts map[string]any, env Env) (vectorstore.Store, error)
}

// NewVectorStoreType returns a vector store type whose options decode
// strictly into O.
func NewVectorStoreType[O any](build func(ctx context.Context, opts O, env Env) (vectorstore.Store, error),
) VectorStoreType {
	return VectorStoreType{
		validate: func(raw map[string]any) error {
			_, err := decodeStrict[O](raw)

			return err
		},
		build: func(ctx context.Context, raw map[string]any, env Env) (vectorstore.Store, error) {
			o, err := decodeStrict[O](raw)
			if err != nil {
				return nil, err
			}

			return build(ctx, o, env)
		},
	}
}

func decodeStrict[O any](raw map[string]any) (O, error) {
	var o O

	b, err := json.Marshal(raw)
	if err != nil {
		return o, fmt.Errorf("%w: %w", ErrOptions, err)
	}

	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()

	if err = dec.Decode(&o); err != nil {
		return o, fmt.Errorf("%w: %w", ErrOptions, err)
	}

	return o, nil
}

// RegisterMemoryType adds a memory backend.
func (r *Registry) RegisterMemoryType(name string, t MemoryType) error {
	return register(&r.mu, r.memoryTypes, "memory type", name, t)
}

// RegisterEmbedderProvider adds an embedding provider.
func (r *Registry) RegisterEmbedderProvider(name string, f EmbedderFactory) error {
	return register(&r.mu, r.embedders, "embedding provider", name, f)
}

// RegisterVectorStoreType adds a vector store type.
func (r *Registry) RegisterVectorStoreType(name string, t VectorStoreType) error {
	return register(&r.mu, r.vectorStores, "vector store type", name, t)
}

// HasMemoryType implements compile.Catalog.
func (r *Registry) HasMemoryType(name string) bool { return has(&r.mu, r.memoryTypes, name) }

// HasEmbedderProvider implements compile.Catalog.
func (r *Registry) HasEmbedderProvider(name string) bool { return has(&r.mu, r.embedders, name) }

// HasVectorStoreType implements compile.Catalog.
func (r *Registry) HasVectorStoreType(name string) bool { return has(&r.mu, r.vectorStores, name) }

// ValidateMemory implements compile.MemoryValidator.
func (r *Registry) ValidateMemory(m spec.Memory) error {
	t, ok := get(&r.mu, r.memoryTypes, m.TypeOrDefault())
	if !ok {
		return fmt.Errorf("%w: memory type %q", ErrUnknown, m.TypeOrDefault())
	}

	return t.validate(m.Options)
}

// ValidateVectorStore implements compile.VectorStoreValidator.
func (r *Registry) ValidateVectorStore(v spec.VectorStore) error {
	t, ok := get(&r.mu, r.vectorStores, v.Type)
	if !ok {
		return fmt.Errorf("%w: vector store type %q", ErrUnknown, v.Type)
	}

	return t.validate(v.Options)
}

// BuildMemory builds a long-term memory.
func (r *Registry) BuildMemory(ctx context.Context, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
	t, ok := get(&r.mu, r.memoryTypes, m.TypeOrDefault())
	if !ok {
		return nil, fmt.Errorf("%w: memory type %q", ErrUnknown, m.TypeOrDefault())
	}

	return t.build(ctx, m, env)
}

// BuildEmbedder returns the embedder's Instance or builds it.
func (r *Registry) BuildEmbedder(name string, e spec.Embedder) (embedding.Embedder, error) {
	if e.Instance != nil {
		return e.Instance, nil
	}

	f, ok := get(&r.mu, r.embedders, e.Provider)
	if !ok {
		return nil, fmt.Errorf("embedder %q: %w: provider %q", name, ErrUnknown, e.Provider)
	}

	out, err := f(e)
	if err != nil {
		return nil, fmt.Errorf("embedder %q: %w", name, err)
	}

	return out, nil
}

// BuildVectorStore returns the store's Instance or builds it.
func (r *Registry) BuildVectorStore(ctx context.Context, name string, v spec.VectorStore, env Env,
) (vectorstore.Store, error) {
	if v.Instance != nil {
		return v.Instance, nil
	}

	t, ok := get(&r.mu, r.vectorStores, v.Type)
	if !ok {
		return nil, fmt.Errorf("vector store %q: %w: type %q", name, ErrUnknown, v.Type)
	}

	out, err := t.build(ctx, v.Options, env)
	if err != nil {
		return nil, fmt.Errorf("vector store %q: %w", name, err)
	}

	return out, nil
}

// sessionFile is the file of a session in a jsonfile memory directory.
func sessionFile(dir, session string) string {
	return filepath.Join(dir, session+".json")
}
