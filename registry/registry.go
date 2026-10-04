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

// Package registry maps the names a fleet uses to constructors: LLM
// providers, tool types and activities. [New] returns a registry with the
// built-ins; programs register their own before building an app. A Registry
// implements compile.Catalog, so validation sees exactly what the runtime
// can build.
package registry

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"slices"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/spec"
)

// Errors returned by a Registry.
var (
	ErrDuplicate = errors.New("registry: name already registered")
	ErrUnknown   = errors.New("registry: unknown name")
	ErrOptions   = errors.New("registry: invalid options")
)

// ModelFactory builds an LLM client from its spec.
type ModelFactory func(m spec.Model) (llm.LLM, error)

// Env is what factories may use besides their options.
type Env struct {
	// Conn is the fleet's NATS connection, for storages backed by phero's
	// NATS helpers (kv tool, nats memory). Factories must not use it directly.
	Conn   *nats.Conn
	Logger *slog.Logger
	// SQL returns a shared database handle for a DSN; see [SQLPool].
	SQL func(dsn string) (*sql.DB, error)
	// Defer registers a cleanup that runs when the app stops (an MCP
	// session, a client). Nil when nothing collects cleanups.
	Defer func(cleanup func() error)
}

func (e Env) sqlDB(dsn string) (*sql.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("%w: dsn is required", ErrOptions)
	}

	if e.SQL == nil {
		return nil, fmt.Errorf("%w: no SQL pool configured", ErrOptions)
	}

	return e.SQL(dsn)
}

func (e Env) deferCleanup(cleanup func() error) {
	if e.Defer != nil {
		e.Defer(cleanup)
	}
}

// ToolType builds the LLM tools of one tool entry. Create one with
// [NewToolType].
type ToolType struct {
	validate func(opts map[string]any) error
	build    func(ctx context.Context, opts map[string]any, env Env) ([]*llm.Tool, error)
}

// NewToolType returns a tool type whose options decode strictly into O:
// unknown keys and wrong types are errors, both at validation and at build.
func NewToolType[O any](build func(ctx context.Context, opts O, env Env) ([]*llm.Tool, error)) ToolType {
	decode := func(raw map[string]any) (O, error) {
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

	return ToolType{
		validate: func(raw map[string]any) error {
			_, err := decode(raw)

			return err
		},
		build: func(ctx context.Context, raw map[string]any, env Env) ([]*llm.Tool, error) {
			o, err := decode(raw)
			if err != nil {
				return nil, err
			}

			return build(ctx, o, env)
		},
	}
}

// Registry holds the constructors a fleet can name. It is safe for
// concurrent use.
type Registry struct {
	mu           sync.RWMutex
	providers    map[string]ModelFactory
	toolTypes    map[string]ToolType
	activities   map[string]worker.Handler
	memoryTypes  map[string]MemoryType
	embedders    map[string]EmbedderFactory
	vectorStores map[string]VectorStoreType
}

// Empty returns a registry with nothing registered.
func Empty() *Registry {
	return &Registry{
		providers:    map[string]ModelFactory{},
		toolTypes:    map[string]ToolType{},
		activities:   map[string]worker.Handler{},
		memoryTypes:  map[string]MemoryType{},
		embedders:    map[string]EmbedderFactory{},
		vectorStores: map[string]VectorStoreType{},
	}
}

// New returns a registry with every built-in:
//   - LLM providers: openai, anthropic, ollama;
//   - tool types: bash, file_read, file_write, file_edit, glob, grep, skill,
//     kv, mcp;
//   - memory types: simple, jsonfile, nats, psql, rag;
//   - embedding providers: openai, ollama;
//   - vector stores: qdrant, psql, weaviate.
func New() *Registry {
	r := Empty()

	maps.Copy(r.providers, builtinProviders())
	maps.Copy(r.toolTypes, builtinToolTypes())
	maps.Copy(r.memoryTypes, builtinMemoryTypes())
	maps.Copy(r.embedders, builtinEmbedders())
	maps.Copy(r.vectorStores, builtinVectorStores())

	return r
}

func register[V any](mu *sync.RWMutex, m map[string]V, what, name string, v V) error {
	mu.Lock()
	defer mu.Unlock()

	if _, dup := m[name]; dup {
		return fmt.Errorf("%w: %s %q", ErrDuplicate, what, name)
	}

	m[name] = v

	return nil
}

// RegisterProvider adds an LLM provider.
func (r *Registry) RegisterProvider(name string, f ModelFactory) error {
	return register(&r.mu, r.providers, "provider", name, f)
}

// RegisterToolType adds a tool type.
func (r *Registry) RegisterToolType(name string, t ToolType) error {
	return register(&r.mu, r.toolTypes, "tool type", name, t)
}

// RegisterActivity adds an activity: a Go function run as a flow step.
func (r *Registry) RegisterActivity(name string, h worker.Handler) error {
	if h == nil {
		return fmt.Errorf("%w: activity %q has a nil handler", ErrOptions, name)
	}

	return register(&r.mu, r.activities, "activity", name, h)
}

// HasProvider implements compile.Catalog.
func (r *Registry) HasProvider(name string) bool { return has(&r.mu, r.providers, name) }

// HasToolType implements compile.Catalog.
func (r *Registry) HasToolType(name string) bool { return has(&r.mu, r.toolTypes, name) }

// HasActivity implements compile.Catalog.
func (r *Registry) HasActivity(name string) bool { return has(&r.mu, r.activities, name) }

func has[V any](mu *sync.RWMutex, m map[string]V, name string) bool {
	_, ok := get(mu, m, name)

	return ok
}

func get[V any](mu *sync.RWMutex, m map[string]V, name string) (V, bool) {
	mu.RLock()
	defer mu.RUnlock()

	v, ok := m[name]

	return v, ok
}

// Activities returns the registered activity names, sorted.
func (r *Registry) Activities() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return slices.Sorted(maps.Keys(r.activities))
}

// Activity returns a registered activity.
func (r *Registry) Activity(name string) (worker.Handler, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	h, ok := r.activities[name]

	return h, ok
}

// ValidateTool implements compile.ToolValidator: it decodes the options
// without building the tool.
func (r *Registry) ValidateTool(t spec.Tool) error {
	r.mu.RLock()
	tt, ok := r.toolTypes[t.Type]
	r.mu.RUnlock()

	if !ok {
		return fmt.Errorf("%w: tool type %q", ErrUnknown, t.Type)
	}

	return tt.validate(t.Options)
}

// BuildModel returns the model's client: its Instance, or one built by its
// provider.
func (r *Registry) BuildModel(name string, m spec.Model) (llm.LLM, error) {
	if m.Instance != nil {
		return m.Instance, nil
	}

	r.mu.RLock()
	f, ok := r.providers[m.Provider]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("model %q: %w: provider %q", name, ErrUnknown, m.Provider)
	}

	l, err := f(m)
	if err != nil {
		return nil, fmt.Errorf("model %q: %w", name, err)
	}

	return l, nil
}

// BuildTool returns the tool entry's LLM tools: its Instances, or the ones
// built by its type.
func (r *Registry) BuildTool(ctx context.Context, name string, t spec.Tool, env Env) ([]*llm.Tool, error) {
	if len(t.Instances) > 0 {
		return t.Instances, nil
	}

	r.mu.RLock()
	tt, ok := r.toolTypes[t.Type]
	r.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("tool %q: %w: tool type %q", name, ErrUnknown, t.Type)
	}

	tools, err := tt.build(ctx, t.Options, env)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", name, err)
	}

	return tools, nil
}
