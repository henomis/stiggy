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

// Package spec is stiggy's model of a fleet: the models, tools and agents it
// runs, and the packtrail flows that orchestrate them. The YAML front end
// (package config) and the Go API both build a [Fleet]; package compile
// validates it and turns it into a deployable plan.
//
// Flows are plain packtrail flows ([flow.Flow]). A node that runs an agent or
// an activity carries a [Step] in its meta under [MetaKey]; [ApplyStep] sets
// that meta together with the worker kind and the dynamic edges the step
// implies, so YAML and Go produce identical flows.
package spec

import (
	"gopkg.in/yaml.v3"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/phero/v2/embedding"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/vectorstore"
)

// Version is the only supported fleet schema version.
const Version = "1"

// DefaultNamespace is the packtrail namespace used when a fleet sets none.
const DefaultNamespace = "stiggy"

// Fleet is a complete multi-agent deployment.
type Fleet struct {
	// Version is the schema version; empty means [Version].
	Version string `yaml:"version,omitempty" json:"version,omitempty"`
	// Namespace is the packtrail namespace; empty means [DefaultNamespace].
	Namespace string `yaml:"namespace,omitempty" json:"namespace,omitempty"`
	// Models are the LLMs agents use, by name.
	Models map[string]Model `yaml:"models,omitempty" json:"models,omitempty"`
	// Tools are tool instances agents use, by name.
	Tools map[string]Tool `yaml:"tools,omitempty" json:"tools,omitempty"`
	// Agents are the agents steps run, by name.
	Agents map[string]Agent `yaml:"agents,omitempty" json:"agents,omitempty"`
	// Flows are the packtrail flows, by name. Each flow's Name equals its key.
	Flows map[string]*flow.Flow `yaml:"-" json:"flows,omitempty"`
	// Embedders, VectorStores and Knowledge define searchable document
	// collections (RAG), by name.
	Embedders    map[string]Embedder    `yaml:"embedders,omitempty" json:"embedders,omitempty"`
	VectorStores map[string]VectorStore `yaml:"vectorstores,omitempty" json:"vectorstores,omitempty"`
	Knowledge    map[string]Knowledge   `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// Remotes are agents served by others on the NATS Agent Protocol, found
	// by discovery. Steps run them with `remote:`; agents may delegate to
	// them.
	Remotes map[string]Remote `yaml:"remotes,omitempty" json:"remotes,omitempty"`
	// Expose serves agents and flows of this fleet on the NATS Agent
	// Protocol, so other programs (any v0.3 client) can call them.
	Expose *Expose `yaml:"expose,omitempty" json:"expose,omitempty"`
	// Crews and Patterns compile into flows named after them (crews;
	// supervisor, swarm, evaluator-optimizer, debate, plan-execute).
	Crews    map[string]Crew    `yaml:"crews,omitempty" json:"crews,omitempty"`
	Patterns map[string]Pattern `yaml:"patterns,omitempty" json:"patterns,omitempty"`
	// Schedules start flows on a cron, by name.
	Schedules map[string]Schedule `yaml:"schedules,omitempty" json:"schedules,omitempty"`
}

// Model configures an LLM client.
type Model struct {
	// Instance is a ready LLM client, set from Go. When it is set, the other
	// fields are ignored.
	Instance llm.LLM `yaml:"-" json:"-"`
	// Provider names a registered LLM provider (openai, anthropic, ollama, ...).
	Provider string `yaml:"provider" json:"provider,omitempty"`
	// Model is the provider's model id; empty means the provider default.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// APIKey is a secret: it is never written into a flow or a plan.
	APIKey string `yaml:"api_key,omitempty" json:"-"`
	// BaseURL overrides the provider endpoint.
	BaseURL string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
	// Temperature is the sampling temperature; nil means the provider default.
	Temperature *float64 `yaml:"temperature,omitempty" json:"temperature,omitempty"`
	// MaxTokens caps the output tokens of one call; 0 means the default.
	MaxTokens int `yaml:"max_tokens,omitempty" json:"max_tokens,omitempty"`
	// Retries is how many times a failed call is retried (phero's retry
	// middleware, with exponential back-off), on top of the step's retries.
	Retries int `yaml:"retries,omitempty" json:"retries,omitempty"`
	// RateLimit caps the calls to this model. It applies
	// per process: each worker process has its own budget.
	RateLimit *RateLimit `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
}

// RateLimit bounds the calls to a model.
type RateLimit struct {
	RequestsPerMinute float64 `yaml:"requests_per_minute" json:"requests_per_minute"`
	// MaxConcurrent bounds calls in flight; 0 means no bound beyond the rate.
	MaxConcurrent int `yaml:"max_concurrent,omitempty" json:"max_concurrent,omitempty"`
}

// Tool configures one tool instance. Options are specific to Type and are
// checked by the tool's factory. One entry may provide several LLM tools
// (an MCP server, a KV store with read and write tools).
type Tool struct {
	// Instances are ready tools, set from Go. When they are set, the other
	// fields are ignored.
	Instances []*llm.Tool `yaml:"-" json:"-"`
	// Type names a registered tool type (bash, file_read, ...).
	Type string `yaml:"type" json:"type,omitempty"`
	// Options are the remaining keys of the tool's YAML mapping.
	Options map[string]any `yaml:",inline" json:"options,omitempty"`
}

// Agent configures a phero agent.
type Agent struct {
	// Model names an entry of [Fleet.Models].
	Model string `yaml:"model" json:"model"`
	// System is the system prompt. Role, Goal and Backstory, when set, are
	// appended to it.
	System    string `yaml:"system,omitempty" json:"system,omitempty"`
	Role      string `yaml:"role,omitempty" json:"role,omitempty"`
	Goal      string `yaml:"goal,omitempty" json:"goal,omitempty"`
	Backstory string `yaml:"backstory,omitempty" json:"backstory,omitempty"`
	// Description says what the agent does, for humans and for agents that
	// may route or delegate to it.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Tools name entries of [Fleet.Tools].
	Tools []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	// MaxIterations bounds the agent loop of one step; 0 means phero's
	// default (agent.DefaultMaxIterations, 25).
	MaxIterations int `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
	// Stream publishes the agent's text deltas and tool calls as packtrail
	// progress events while a step runs (Client.Progress).
	Stream bool `yaml:"stream,omitempty" json:"stream,omitempty"`
	// Memory selects the conversation memory; the zero value means
	// [MemoryNone].
	Memory Memory `yaml:"memory,omitempty" json:"memory,omitzero"`
	// Knowledge names entries of [Fleet.Knowledge] the agent can search; each
	// becomes a search_<name> tool.
	Knowledge []string `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// Delegates name other agents this agent may call as tools within one
	// step (delegation). Unlike routes, these calls are not
	// durable: if the step fails, they run again with it.
	Delegates []string `yaml:"delegates,omitempty" json:"delegates,omitempty"`
}

// Memory types. Besides these, every memory backend of phero is a type:
// simple, jsonfile, nats, psql and rag (see package registry).
const (
	// MemoryNone disables conversation memory: every step starts fresh.
	MemoryNone = "none"
	// MemoryExecution keeps the agent's conversation in the execution's
	// state (a hidden channel), so forks and reruns see it exactly as of
	// their fork point. Unless the flow sets its own output, the compiler
	// sets one that leaves the hidden channel out.
	MemoryExecution = "execution"
)

// Memory selects an agent's conversation memory. In YAML it is either a
// type name (`memory: none`) or a mapping with a type and options.
type Memory struct {
	Type string `yaml:"type" json:"type"`
	// Session is the memory session of a long-term memory; empty means the
	// agent's name, so all executions share it.
	Session string `yaml:"session,omitempty" json:"session,omitempty"`
	// PerExecution scopes a long-term memory to one execution: the session
	// gets the execution id appended.
	PerExecution bool `yaml:"per_execution,omitempty" json:"per_execution,omitempty"`
	// Summarize keeps a long-term memory short by summarizing old messages.
	Summarize *Summarize `yaml:"summarize,omitempty" json:"summarize,omitempty"`
	// Knowledge names the [Fleet.Knowledge] entry a "rag" memory stores into.
	Knowledge string `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// Options are specific to Type (max_items, path, bucket, dsn, ...).
	Options map[string]any `yaml:",inline" json:"options,omitempty"`
}

// Summarize configures memory summarization.
type Summarize struct {
	// Model names the [Fleet.Models] entry that writes summaries.
	Model string `yaml:"model" json:"model"`
	// Threshold is the message count that triggers a summary.
	Threshold uint `yaml:"threshold" json:"threshold"`
	// Size is how many recent messages stay verbatim.
	Size uint `yaml:"size" json:"size"`
}

// TypeOrDefault returns the memory type, [MemoryNone] when unset.
func (m *Memory) TypeOrDefault() string {
	if m.Type == "" {
		return MemoryNone
	}

	return m.Type
}

// IsLongTerm reports whether the memory is kept by a phero backend (not
// none or execution).
func (m *Memory) IsLongTerm() bool {
	t := m.TypeOrDefault()

	return t != MemoryNone && t != MemoryExecution
}

// Embedder configures a text embedding client for knowledge.
type Embedder struct {
	// Instance is a ready embedder, set from Go.
	Instance embedding.Embedder `yaml:"-" json:"-"`
	// Provider names a registered embedding provider (openai, ollama).
	Provider string `yaml:"provider" json:"provider,omitempty"`
	Model    string `yaml:"model,omitempty" json:"model,omitempty"`
	APIKey   string `yaml:"api_key,omitempty" json:"-"`
	BaseURL  string `yaml:"base_url,omitempty" json:"base_url,omitempty"`
}

// VectorStore configures a vector store for knowledge. Options are specific
// to Type (qdrant, psql, weaviate).
type VectorStore struct {
	// Instance is a ready store, set from Go.
	Instance vectorstore.Store `yaml:"-" json:"-"`
	Type     string            `yaml:"type" json:"type,omitempty"`
	Options  map[string]any    `yaml:",inline" json:"options,omitempty"`
}

// Knowledge is a searchable document collection (a RAG
// index): sources split into chunks, embedded and stored in a vector store.
type Knowledge struct {
	// Embedder names a [Fleet.Embedders] entry.
	Embedder string `yaml:"embedder" json:"embedder"`
	// VectorStore names a [Fleet.VectorStores] entry.
	VectorStore string `yaml:"vectorstore" json:"vectorstore"`
	// Description tells agents what they can find in it.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// TopK is how many chunks a search returns; 0 means phero's default.
	TopK uint64 `yaml:"top_k,omitempty" json:"top_k,omitempty"`
	// Sources are ingested when the engine starts, unless the store already
	// holds documents.
	Sources []Source `yaml:"sources,omitempty" json:"sources,omitempty"`
}

// Source is a set of files to ingest.
type Source struct {
	// Path is a file path or a glob.
	Path string `yaml:"path" json:"path"`
	// Splitter is "recursive" or "markdown"; empty picks markdown for .md
	// files and recursive otherwise.
	Splitter     string `yaml:"splitter,omitempty" json:"splitter,omitempty"`
	ChunkSize    int    `yaml:"chunk_size,omitempty" json:"chunk_size,omitempty"`
	ChunkOverlap int    `yaml:"chunk_overlap,omitempty" json:"chunk_overlap,omitempty"`
}

// UnmarshalYAML accepts both the scalar and the mapping form.
func (m *Memory) UnmarshalYAML(value *yaml.Node) error {
	if value.Kind == yaml.ScalarNode {
		m.Type = value.Value
		m.Options = nil

		return nil
	}

	type plain Memory

	return value.Decode((*plain)(m))
}

// Remote is an agent served by someone else on the NATS Agent Protocol.
type Remote struct {
	// Owner and Name address it (the protocol's owner and instance name).
	Owner string `yaml:"owner" json:"owner"`
	Name  string `yaml:"name" json:"name"`
	// Description tells agents that delegate to it what it does.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
}

// Expose lists what a fleet serves on the NATS Agent Protocol.
type Expose struct {
	// Owner is the protocol owner of every exposed agent and flow; empty
	// means the fleet's namespace.
	Owner string `yaml:"owner,omitempty" json:"owner,omitempty"`
	// Agents are served as themselves: each prompt runs a fresh agent.
	Agents []string `yaml:"agents,omitempty" json:"agents,omitempty"`
	// Flows are served as agents: each prompt starts an execution (its input
	// is the prompt, as a JSON object or {"prompt": text}) and the answer is
	// the execution's output.
	Flows []string `yaml:"flows,omitempty" json:"flows,omitempty"`
}

// OwnerOrDefault returns the protocol owner of exposed agents and flows.
func (f *Fleet) OwnerOrDefault() string {
	if f.Expose != nil && f.Expose.Owner != "" {
		return f.Expose.Owner
	}

	return f.NamespaceOrDefault()
}

// Schedule starts a flow on a cron schedule.
type Schedule struct {
	// Flow names an entry of [Fleet.Flows].
	Flow string `yaml:"flow" json:"flow"`
	// Cron is a 6-field cron expression (with seconds) or an @-descriptor.
	Cron string `yaml:"cron" json:"cron"`
	// Input is the flow input of every scheduled execution.
	Input any `yaml:"input,omitempty" json:"input,omitempty"`
}

// NamespaceOrDefault returns the fleet namespace, [DefaultNamespace] when unset.
func (f *Fleet) NamespaceOrDefault() string {
	if f.Namespace == "" {
		return DefaultNamespace
	}

	return f.Namespace
}
