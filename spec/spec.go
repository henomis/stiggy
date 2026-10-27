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
	// Tracing exports the spans of agent steps and exposed agents: agent
	// runs, model calls, tool calls, delegates. Absent means no export.
	Tracing *Tracing `yaml:"tracing,omitempty" json:"tracing,omitempty"`
}

// Model configures an LLM client.
type Model struct {
	// Instance is a ready LLM client, set from Go. When it is set, the other
	// fields are ignored.
	Instance llm.LLM `yaml:"-" json:"-"`
	// Provider names a registered LLM provider (openai, anthropic, google, ollama, ...).
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
	// RetryBackoff is the wait before the first retry; it doubles on each
	// retry, up to 30s. 0 keeps phero's default (500ms). Only with Retries.
	RetryBackoff flow.Duration `yaml:"retry_backoff,omitempty" json:"retry_backoff,omitempty"`
	// RateLimit caps the calls to this model. It applies
	// per process: each worker process has its own budget.
	RateLimit *RateLimit `yaml:"rate_limit,omitempty" json:"rate_limit,omitempty"`
	// StrictTools set to false sends tool schemas without strict mode, for
	// compatible servers that mishandle it; nil keeps strict mode (openai,
	// ollama and anthropic only).
	StrictTools *bool `yaml:"strict_tools,omitempty" json:"strict_tools,omitempty"`
	// PromptCacheTTL turns on prompt caching with entries kept for 5m or 1h
	// (anthropic only).
	PromptCacheTTL string `yaml:"prompt_cache_ttl,omitempty" json:"prompt_cache_ttl,omitempty"`
}

// RateLimit bounds the calls to a model: their rate, their concurrency, or
// both. At least one must be set.
type RateLimit struct {
	// RequestsPerMinute bounds the call rate; 0 means no rate bound.
	RequestsPerMinute float64 `yaml:"requests_per_minute,omitempty" json:"requests_per_minute,omitempty"`
	// MaxConcurrent bounds calls in flight; 0 means no concurrency bound.
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
	// appended to it. All four are templates over the job context, like a
	// step prompt (see [ParsePrompt]), so "{{.item.name}}" gives each item of
	// a map its own persona. Exposed agents and their delegates run outside
	// any job, so theirs must be static.
	System    string `yaml:"system,omitempty" json:"system,omitempty"`
	Role      string `yaml:"role,omitempty" json:"role,omitempty"`
	Goal      string `yaml:"goal,omitempty" json:"goal,omitempty"`
	Backstory string `yaml:"backstory,omitempty" json:"backstory,omitempty"`
	// Description says what the agent does, for humans and for agents that
	// may route or delegate to it.
	Description string `yaml:"description,omitempty" json:"description,omitempty"`
	// Tools name entries of [Fleet.Tools].
	Tools []string `yaml:"tools,omitempty" json:"tools,omitempty"`
	// Permissions decide which calls of the agent's Tools may run; nil
	// allows them all.
	Permissions *Permissions `yaml:"permissions,omitempty" json:"permissions,omitempty"`
	// MaxIterations bounds the agent loop of one step; 0 means phero's
	// default (agent.DefaultMaxIterations, 25).
	MaxIterations int `yaml:"max_iterations,omitempty" json:"max_iterations,omitempty"`
	// Todo gives the agent a todo_write tool to keep a task list while it
	// works on a long step. The list lasts one run: a step, a delegate's
	// step, or a prompt to an exposed agent.
	Todo bool `yaml:"todo,omitempty" json:"todo,omitempty"`
	// MaxParallelTools bounds the tool calls of one model answer that run at
	// once; 0 runs them all at once.
	MaxParallelTools int `yaml:"max_parallel_tools,omitempty" json:"max_parallel_tools,omitempty"`
	// Compaction keeps the conversation within the model's context window
	// by summarizing its older messages; nil disables it.
	Compaction *Compaction `yaml:"compaction,omitempty" json:"compaction,omitempty"`
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

// Permissions decide which tool calls an agent may make, with rules written
// as a tool name, optionally followed by a pattern on what the call touches:
// "bash(go test *)", "read(/repo/**)", "mcp_github_*". A call is denied when
// a deny rule matches, else allowed when an allow rule matches, else given
// Default. A denied call is reported to the model, which may go on another
// way. Rules are matched as phero's permission package does: a bash command
// line is split into its commands, each of which must be allowed, and a line
// it cannot judge (a command substitution, a redirection to a file) is
// denied unless an allow rule names it whole.
type Permissions struct {
	Allow []string `yaml:"allow,omitempty" json:"allow,omitempty"`
	Deny  []string `yaml:"deny,omitempty" json:"deny,omitempty"`
	// Default is the decision for a call no rule matches: "allow" (the
	// default) or "deny".
	Default string `yaml:"default,omitempty" json:"default,omitempty"`
	// WorkingDir is what relative file paths, in calls and in rules, are
	// resolved against; set it to the file tools' working_dir. Empty means
	// the worker's working directory.
	WorkingDir string `yaml:"working_dir,omitempty" json:"working_dir,omitempty"`
}

// Permission defaults.
const (
	PermissionAllow = "allow"
	PermissionDeny  = "deny"
)

// Memory types. Besides these, phero's memory backends simple, nats, psql
// and rag are types (see package registry). phero's jsonfile is left out: it
// is meant for tests, and two instances of one file overwrite each other.
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
	// agent's name, so all executions share it. It is a template over the
	// job context ("user-{{.input.user_id}}"), rendered per step; a missing
	// key fails the step rather than sharing a session (see [ParseSession]).
	Session string `yaml:"session,omitempty" json:"session,omitempty"`
	// PerExecution scopes a long-term memory to one execution: the session
	// gets the execution id appended.
	PerExecution bool `yaml:"per_execution,omitempty" json:"per_execution,omitempty"`
	// Knowledge names the [Fleet.Knowledge] entry a "rag" memory stores into.
	Knowledge string `yaml:"knowledge,omitempty" json:"knowledge,omitempty"`
	// Options are specific to Type (max_items, path, bucket, dsn, ...).
	Options map[string]any `yaml:",inline" json:"options,omitempty"`
}

// Compaction configures how an agent compacts its conversation: the older
// messages are replaced by a summary, the recent ones are kept whole. A
// conversation is compacted when it reaches AtTokens, and whenever the model
// rejects a prompt too long for its context window (then the call is made
// again, once). With a long-term memory that keeps snapshots (simple, nats,
// psql), the summary is stored too, and the next run starts from it.
type Compaction struct {
	// Model names the [Fleet.Models] entry that writes summaries; empty
	// means the agent's own model.
	Model string `yaml:"model,omitempty" json:"model,omitempty"`
	// AtTokens is the conversation size, in tokens, that triggers a
	// compaction; 0 compacts only on a context-length error.
	AtTokens int `yaml:"at_tokens,omitempty" json:"at_tokens,omitempty"`
	// KeepRecent bounds the recent messages kept whole; 0 means phero's
	// default (agent.DefaultKeepRecent, 20).
	KeepRecent int `yaml:"keep_recent,omitempty" json:"keep_recent,omitempty"`
	// KeepTokens bounds the size of the messages kept whole; 0 means a
	// quarter of AtTokens, or phero's default (agent.DefaultKeepTokens,
	// 20000) without it.
	KeepTokens int `yaml:"keep_tokens,omitempty" json:"keep_tokens,omitempty"`
	// MaxSummaryTokens bounds the summary; 0 leaves it to the model.
	MaxSummaryTokens int `yaml:"max_summary_tokens,omitempty" json:"max_summary_tokens,omitempty"`
	// Prompt replaces phero's instructions to the summarizing model.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
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

// Tracing exporters.
const (
	// TracingOTLP exports OpenTelemetry spans over OTLP/HTTP, following the
	// GenAI semantic conventions.
	TracingOTLP = "otlp"
	// TracingJSONFile writes one JSON line per span to a file.
	TracingJSONFile = "jsonfile"
	// TracingText prints an indented span tree on stderr.
	TracingText = "text"
)

// Tracing configures where a fleet's spans go.
type Tracing struct {
	// Exporter is otlp, jsonfile or text.
	Exporter string `yaml:"exporter" json:"exporter"`
	// Endpoint is the OTLP/HTTP collector, as host:port or as a base URL,
	// like OTEL_EXPORTER_OTLP_ENDPOINT: spans go to its path + /v1/traces
	// (a URL already ending in /v1/traces is kept). Empty uses the
	// OTEL_EXPORTER_OTLP_* environment variables (default localhost:4318).
	// otlp only.
	Endpoint string `yaml:"endpoint,omitempty" json:"endpoint,omitempty"`
	// Insecure sends OTLP over plain HTTP. otlp only.
	Insecure bool `yaml:"insecure,omitempty" json:"insecure,omitempty"`
	// Headers are sent with every OTLP export (authentication). otlp only.
	Headers map[string]string `yaml:"headers,omitempty" json:"headers,omitempty"`
	// ServiceName is the OTel service.name; empty means OTEL_SERVICE_NAME,
	// else the fleet's namespace. otlp only.
	ServiceName string `yaml:"service_name,omitempty" json:"service_name,omitempty"`
	// File is the jsonfile exporter's output. jsonfile only.
	File string `yaml:"file,omitempty" json:"file,omitempty"`
	// Content decides whether prompts, answers, tool arguments and results
	// are exported.
	Content *TracingContent `yaml:"content,omitempty" json:"content,omitempty"`
}

// TracingContent is the content policy of a tracing exporter.
type TracingContent struct {
	// Capture exports message content. Unset, otlp captures none (as the
	// semantic conventions advise) and jsonfile and text capture all.
	Capture *bool `yaml:"capture,omitempty" json:"capture,omitempty"`
	// MaxBytes bounds each captured value; 0 means 64 KiB for otlp and no
	// bound for jsonfile and text.
	MaxBytes int `yaml:"max_bytes,omitempty" json:"max_bytes,omitempty"`
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
