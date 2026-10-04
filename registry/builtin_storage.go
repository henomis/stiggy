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
	"context"
	"fmt"

	qdrantapi "github.com/qdrant/go-client/qdrant"
	weaviateclient "github.com/weaviate/weaviate-go-client/v4/weaviate"
	weaviateauth "github.com/weaviate/weaviate-go-client/v4/weaviate/auth"

	"github.com/henomis/phero/v2/embedding"
	embeddingopenai "github.com/henomis/phero/v2/embedding/openai"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/memory"
	jsonfilememory "github.com/henomis/phero/v2/memory/jsonfile"
	natsmemory "github.com/henomis/phero/v2/memory/nats"
	psqlmemory "github.com/henomis/phero/v2/memory/psql"
	simplememory "github.com/henomis/phero/v2/memory/simple"
	"github.com/henomis/phero/v2/vectorstore"
	vspsql "github.com/henomis/phero/v2/vectorstore/psql"
	vsqdrant "github.com/henomis/phero/v2/vectorstore/qdrant"
	vsweaviate "github.com/henomis/phero/v2/vectorstore/weaviate"

	"github.com/henomis/stiggy/spec"
)

// Defaults of the built-in storages.
const (
	defaultSimpleMaxItems = 100
	defaultQdrantPort     = 6334
	defaultWeaviateScheme = "http"
)

// Distance names shared by the vector stores.
const (
	distanceCosine    = "cosine"
	distanceEuclid    = "euclid"
	distanceDot       = "dot"
	distanceManhattan = "manhattan"
)

type simpleMemoryOptions struct {
	MaxItems uint `json:"max_items"`
}

type jsonfileMemoryOptions struct {
	Dir string `json:"dir"`
}

type natsMemoryOptions struct {
	Bucket string `json:"bucket"`
}

type psqlMemoryOptions struct {
	DSN          string `json:"dsn"`
	Table        string `json:"table"`
	EnsureSchema *bool  `json:"ensure_schema"`
}

type ragMemoryOptions struct{}

// summarized is the common shape of phero's WithSummarization options.
func summarized[O any](env MemoryEnv, m spec.Memory, with func(l llm.LLM, threshold, size uint) O) ([]O, error) {
	l, s, err := env.Summarization(m)
	if err != nil || s == nil {
		return nil, err
	}

	return []O{with(l, s.Threshold, s.Size)}, nil
}

func builtinMemoryTypes() map[string]MemoryType {
	return map[string]MemoryType{
		"simple":   NewMemoryType(newSimpleMemory),
		"jsonfile": NewMemoryType(newJSONFileMemory),
		"nats":     NewMemoryType(newNATSMemory),
		namePsql:   NewMemoryType(newPsqlMemory),
		"rag":      NewMemoryType(newRAGMemory),
	}
}

func newSimpleMemory(_ context.Context, o simpleMemoryOptions, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
	opts, err := summarized(env, m, simplememory.WithSummarization)
	if err != nil {
		return nil, err
	}

	n := o.MaxItems
	if n == 0 {
		n = defaultSimpleMaxItems
	}

	return simplememory.New(n, opts...), nil
}

func newJSONFileMemory(_ context.Context, o jsonfileMemoryOptions, m spec.Memory, env MemoryEnv,
) (memory.Memory, error) {
	if o.Dir == "" {
		return nil, fmt.Errorf("%w: jsonfile memory needs dir", ErrOptions)
	}

	opts, err := summarized(env, m, jsonfilememory.WithSummarization)
	if err != nil {
		return nil, err
	}

	return jsonfilememory.New(sessionFile(o.Dir, env.Session), opts...)
}

func newNATSMemory(_ context.Context, o natsMemoryOptions, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
	if o.Bucket == "" {
		return nil, fmt.Errorf("%w: nats memory needs bucket", ErrOptions)
	}

	if env.Conn == nil {
		return nil, fmt.Errorf("%w: nats memory needs a NATS connection", ErrOptions)
	}

	opts, err := summarized(env, m, natsmemory.WithSummarization)
	if err != nil {
		return nil, err
	}

	return natsmemory.Open(env.Conn, o.Bucket, env.Session, opts...)
}

func newPsqlMemory(_ context.Context, o psqlMemoryOptions, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
	db, err := env.sqlDB(o.DSN)
	if err != nil {
		return nil, err
	}

	opts, err := summarized(env, m, psqlmemory.WithSummarization)
	if err != nil {
		return nil, err
	}

	if o.Table != "" {
		opts = append(opts, psqlmemory.WithTable(o.Table))
	}

	if o.EnsureSchema != nil {
		opts = append(opts, psqlmemory.WithEnsureSchema(*o.EnsureSchema))
	}

	return psqlmemory.New(db, env.Session, opts...)
}

func newRAGMemory(_ context.Context, _ ragMemoryOptions, m spec.Memory, env MemoryEnv) (memory.Memory, error) {
	if m.Knowledge == "" {
		return nil, fmt.Errorf("%w: rag memory needs knowledge", ErrOptions)
	}

	r, err := env.Knowledge(m.Knowledge)
	if err != nil {
		return nil, err
	}

	return r.AsMemory(), nil
}

func builtinEmbedders() map[string]EmbedderFactory {
	return map[string]EmbedderFactory{
		nameOpenAI: func(e spec.Embedder) (embedding.Embedder, error) {
			opts := []embeddingopenai.Option{embeddingopenai.WithBaseURL(e.BaseURL)}
			if e.Model != "" {
				opts = append(opts, embeddingopenai.WithModel(e.Model))
			}

			return embeddingopenai.New(e.APIKey, opts...), nil
		},
		nameOllama: func(e spec.Embedder) (embedding.Embedder, error) {
			if e.Model == "" {
				return nil, fmt.Errorf("%w: the ollama embedding provider needs a model", ErrOptions)
			}

			opts := []embeddingopenai.Option{embeddingopenai.WithOllamaBaseURL(), embeddingopenai.WithModel(e.Model)}
			if e.BaseURL != "" {
				opts = append(opts, embeddingopenai.WithBaseURL(e.BaseURL))
			}

			key := e.APIKey
			if key == "" {
				key = ollamaAPIKey
			}

			return embeddingopenai.New(key, opts...), nil
		},
	}
}

type qdrantOptions struct {
	Host       string `json:"host"`
	Port       int    `json:"port"`
	APIKey     string `json:"api_key"`
	UseTLS     bool   `json:"use_tls"`
	Collection string `json:"collection"`
	VectorSize uint64 `json:"vector_size"`
	Distance   string `json:"distance"`
}

type psqlStoreOptions struct {
	DSN             string `json:"dsn"`
	Collection      string `json:"collection"`
	Table           string `json:"table"`
	VectorSize      uint64 `json:"vector_size"`
	Distance        string `json:"distance"`
	EnsureExtension *bool  `json:"ensure_extension"`
}

type weaviateOptions struct {
	Host       string `json:"host"`
	Scheme     string `json:"scheme"`
	APIKey     string `json:"api_key"`
	Class      string `json:"class"`
	VectorSize uint64 `json:"vector_size"`
	Distance   string `json:"distance"`
}

func builtinVectorStores() map[string]VectorStoreType {
	return map[string]VectorStoreType{
		"qdrant":   NewVectorStoreType(newQdrant),
		namePsql:   NewVectorStoreType(newPsqlStore),
		"weaviate": NewVectorStoreType(newWeaviate),
	}
}

func newQdrant(_ context.Context, o qdrantOptions, _ Env) (vectorstore.Store, error) {
	if o.Host == "" || o.Collection == "" {
		return nil, fmt.Errorf("%w: qdrant needs host and collection", ErrOptions)
	}

	port := o.Port
	if port == 0 {
		port = defaultQdrantPort
	}

	client, err := qdrantapi.NewClient(&qdrantapi.Config{Host: o.Host, Port: port, APIKey: o.APIKey, UseTLS: o.UseTLS})
	if err != nil {
		return nil, fmt.Errorf("qdrant: %w", err)
	}

	var opts []vsqdrant.Option
	if o.VectorSize > 0 {
		opts = append(opts, vsqdrant.WithVectorSize(o.VectorSize))
	}

	if o.Distance != "" {
		d, ok := map[string]vsqdrant.Distance{
			distanceCosine: vsqdrant.DistanceCosine, distanceEuclid: vsqdrant.DistanceEuclid,
			distanceDot: vsqdrant.DistanceDot, distanceManhattan: vsqdrant.DistanceManhattan,
		}[o.Distance]
		if !ok {
			return nil, fmt.Errorf("%w: qdrant distance %q", ErrOptions, o.Distance)
		}

		opts = append(opts, vsqdrant.WithDistance(d))
	}

	return vsqdrant.New(client, o.Collection, opts...)
}

func newPsqlStore(_ context.Context, o psqlStoreOptions, env Env) (vectorstore.Store, error) {
	if o.Collection == "" {
		return nil, fmt.Errorf("%w: psql vector store needs collection", ErrOptions)
	}

	db, err := env.sqlDB(o.DSN)
	if err != nil {
		return nil, err
	}

	var opts []vspsql.Option
	if o.VectorSize > 0 {
		opts = append(opts, vspsql.WithVectorSize(o.VectorSize))
	}

	if o.Table != "" {
		opts = append(opts, vspsql.WithTable(o.Table))
	}

	if o.EnsureExtension != nil {
		opts = append(opts, vspsql.WithEnsureExtension(*o.EnsureExtension))
	}

	if o.Distance != "" {
		d, ok := map[string]vspsql.Distance{
			distanceCosine: vspsql.DistanceCosine, distanceEuclid: vspsql.DistanceEuclid, distanceDot: vspsql.DistanceDot,
		}[o.Distance]
		if !ok {
			return nil, fmt.Errorf("%w: psql distance %q", ErrOptions, o.Distance)
		}

		opts = append(opts, vspsql.WithDistance(d))
	}

	return vspsql.New(db, o.Collection, opts...)
}

func newWeaviate(_ context.Context, o weaviateOptions, _ Env) (vectorstore.Store, error) {
	if o.Host == "" || o.Class == "" {
		return nil, fmt.Errorf("%w: weaviate needs host and class", ErrOptions)
	}

	cfg := weaviateclient.Config{Host: o.Host, Scheme: o.Scheme}
	if cfg.Scheme == "" {
		cfg.Scheme = defaultWeaviateScheme
	}

	if o.APIKey != "" {
		cfg.AuthConfig = weaviateauth.ApiKey{Value: o.APIKey}
	}

	client, err := weaviateclient.NewClient(cfg)
	if err != nil {
		return nil, fmt.Errorf("weaviate: %w", err)
	}

	var opts []vsweaviate.Option
	if o.VectorSize > 0 {
		opts = append(opts, vsweaviate.WithVectorSize(o.VectorSize))
	}

	if o.Distance != "" {
		d, ok := map[string]vsweaviate.Distance{
			distanceCosine: vsweaviate.DistanceCosine, distanceDot: vsweaviate.DistanceDot,
			"l2-squared": vsweaviate.DistanceL2,
		}[o.Distance]
		if !ok {
			return nil, fmt.Errorf("%w: weaviate distance %q", ErrOptions, o.Distance)
		}

		opts = append(opts, vsweaviate.WithDistance(d))
	}

	return vsweaviate.New(client, o.Class, opts...)
}
