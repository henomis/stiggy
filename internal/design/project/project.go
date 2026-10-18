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

// Package project is the designer's document: a project of workflows built
// from blocks (AI Agent, Condition, Loop, Human approval, Agent team...) that
// [Compile] turns into a stiggy fleet in YAML. The project is the source of
// truth; the fleet is generated from it and never read back.
package project

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
)

// Version is the project format version.
const Version = 1

var (
	// ErrVersion is returned for a project of an unknown format version.
	ErrVersion = errors.New("project: unsupported version")
	// ErrDecode is returned for a project that is not valid JSON of the
	// expected shape.
	ErrDecode = errors.New("project: invalid project")
)

// Block types.
const (
	TypeStart       = "start"
	TypeAgent       = "agent"
	TypeRouter      = "router"
	TypeCondition   = "condition"
	TypeLoop        = "loop"
	TypeParallel    = "parallel"
	TypeMerge       = "merge"
	TypeApproval    = "approval"
	TypeSubworkflow = "subworkflow"
	TypeTeam        = "team"
	TypeOutput      = "output"
)

// Attachment types: what an AI block can be given.
const (
	AttachFiles     = "files"
	AttachShell     = "shell"
	AttachKV        = "kv"
	AttachMCP       = "mcp"
	AttachSkills    = "skills"
	AttachKnowledge = "knowledge"
)

// Ports.
const (
	PortOut       = "out"
	PortTrue      = "true"
	PortFalse     = "false"
	PortOtherwise = "otherwise"
	PortApproved  = "approved"
	PortRejected  = "rejected"
	PortTimeout   = "timeout"
)

// Project is a set of workflows sharing credentials.
type Project struct {
	Version     int          `json:"version"`
	Name        string       `json:"name"`
	Namespace   string       `json:"namespace,omitempty"`
	Credentials []Credential `json:"credentials,omitempty"`
	Workflows   []Workflow   `json:"workflows"`
}

// Credential names a model provider and where its key comes from. The key
// itself is never stored: the fleet reads it from the environment variable.
type Credential struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Provider string `json:"provider"`
	Env      string `json:"env,omitempty"`
	BaseURL  string `json:"base_url,omitempty"`
}

// Workflow is one canvas: blocks and the connections between them.
type Workflow struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

// Node is a block on the canvas.
type Node struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Name     string   `json:"name"`
	Position Position `json:"position"`
	// Params are the block's settings; their shape depends on Type.
	Params json.RawMessage `json:"params,omitempty"`
	// Attachments are the tools and knowledge bases of an AI block.
	Attachments []Attachment `json:"attachments,omitempty"`
}

// Position is where a block sits on the canvas; the compiler ignores it.
type Position struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}

// Attachment is a tool or knowledge base given to an AI block.
type Attachment struct {
	ID     string          `json:"id"`
	Type   string          `json:"type"`
	Name   string          `json:"name"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Edge connects a port of one block to another block.
type Edge struct {
	ID   string `json:"id"`
	From End    `json:"from"`
	To   End    `json:"to"`
}

// End is one end of an edge: a block, and on the source side its port.
type End struct {
	Node string `json:"node"`
	Port string `json:"port,omitempty"`
}

// Parse decodes a project strictly: unknown fields are errors.
func Parse(data []byte) (*Project, error) {
	var p Project
	if err := decodeStrict(data, &p); err != nil {
		return nil, err
	}

	if p.Version != Version {
		return nil, fmt.Errorf("%w: %d (want %d)", ErrVersion, p.Version, Version)
	}

	return &p, nil
}

func decodeStrict(data []byte, v any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()

	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("%w: %w", ErrDecode, err)
	}

	return nil
}

// AgentConfig is what every AI block configures: the model, the
// instructions and the task.
type AgentConfig struct {
	// Credential is the ID of a [Credential].
	Credential  string   `json:"credential,omitempty"`
	Model       string   `json:"model,omitempty"`
	Temperature *float64 `json:"temperature,omitempty"`
	// Instructions are the agent's standing instructions (its system prompt).
	Instructions string `json:"instructions,omitempty"`
	// Prompt is the task of this step; it may hold references.
	Prompt string `json:"prompt,omitempty"`
	// Output, when set, makes the answer structured: one field each.
	Output []Field `json:"output,omitempty"`
	// Remember keeps the agent's conversation within one run.
	Remember bool `json:"remember,omitempty"`
	// MaxSteps bounds the agent's tool-calling loop; 0 is the default.
	MaxSteps int `json:"max_steps,omitempty"`
}

// Field is a named, typed value: a workflow input or a structured output.
type Field struct {
	Name        string `json:"name"`
	Type        string `json:"type,omitempty"` // text, number, boolean, list, object
	Description string `json:"description,omitempty"`
}

// Field types.
const (
	FieldText    = "text"
	FieldNumber  = "number"
	FieldBoolean = "boolean"
	FieldList    = "list"
	FieldObject  = "object"
)

// StartParams are the workflow's inputs.
type StartParams struct {
	Inputs []Field `json:"inputs,omitempty"`
}

// RouterParams is an agent that picks where the workflow goes.
type RouterParams struct {
	AgentConfig

	Routes []Route `json:"routes,omitempty"`
	// AskHuman lets the agent ask a person instead of choosing.
	AskHuman bool `json:"ask_human,omitempty"`
}

// Route is one way out of a router: a port and when to take it.
type Route struct {
	Port string `json:"port"`
	When string `json:"when,omitempty"`
}

// ConditionParams compare values to pick the true or false port.
type ConditionParams struct {
	// Match is "all" (every rule) or "any" (at least one); empty means all.
	Match string `json:"match,omitempty"`
	Rules []Rule `json:"rules,omitempty"`
}

// Rule compares a reference with a value.
type Rule struct {
	Left  string `json:"left"`
	Op    string `json:"op"`
	Value string `json:"value,omitempty"`
}

// Condition operators.
const (
	OpEquals      = "equals"
	OpNotEquals   = "not_equals"
	OpContains    = "contains"
	OpNotContains = "not_contains"
	OpStartsWith  = "starts_with"
	OpEndsWith    = "ends_with"
	OpGreater     = "gt"
	OpGreaterEq   = "gte"
	OpLess        = "lt"
	OpLessEq      = "lte"
	OpIsTrue      = "is_true"
	OpIsFalse     = "is_false"
	OpIsEmpty     = "is_empty"
	OpNotEmpty    = "not_empty"
)

// LoopParams run an agent once per item of a list.
type LoopParams struct {
	AgentConfig

	// Over is a reference to the list.
	Over        string `json:"over"`
	MaxParallel int    `json:"max_parallel,omitempty"`
}

// ParallelParams name the branches; each is a port.
type ParallelParams struct {
	Branches []string `json:"branches,omitempty"`
}

// MergeParams say when parallel branches are done.
type MergeParams struct {
	// Wait is "all" (every branch must succeed), "first" (the first success)
	// or "min" (every branch finishes, at least Min of them succeed); empty
	// means all.
	Wait string `json:"wait,omitempty"`
	// Min is how many branches must succeed with Wait "min"; default 1.
	Min *int `json:"min,omitempty"`
}

// ApprovalParams pause the workflow for a person's decision.
type ApprovalParams struct {
	Message string `json:"message,omitempty"`
	// Timeout is a duration such as "24h"; empty means 24h.
	Timeout string `json:"timeout,omitempty"`
}

// SubworkflowParams run another workflow of the project.
type SubworkflowParams struct {
	Workflow string    `json:"workflow"`
	Inputs   []Mapping `json:"inputs,omitempty"`
}

// Mapping names a value: text that may hold references.
type Mapping struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// OutputParams are what the workflow returns.
type OutputParams struct {
	Fields []Mapping `json:"fields,omitempty"`
}

// TeamParams are a team of agents working on one task.
type TeamParams struct {
	Kind    string   `json:"kind"`
	Members []Member `json:"members,omitempty"`
	// Task is what the team works on; it may hold references.
	Task string `json:"task,omitempty"`

	// crew
	Tasks   []TeamTask `json:"tasks,omitempty"`
	Manager string     `json:"manager,omitempty"` // a member; makes the crew hierarchical
	// supervisor
	Lead string `json:"lead,omitempty"`
	// evaluator_optimizer
	Generator string `json:"generator,omitempty"`
	Evaluator string `json:"evaluator,omitempty"`
	// debate
	Judge string `json:"judge,omitempty"`
	// plan_execute
	Planner     string `json:"planner,omitempty"`
	Executor    string `json:"executor,omitempty"`
	Synthesizer string `json:"synthesizer,omitempty"`
	// Rounds bounds evaluator_optimizer and debate; MaxParallel plan_execute.
	Rounds      int `json:"rounds,omitempty"`
	MaxParallel int `json:"max_parallel,omitempty"`
}

// Team kinds.
const (
	TeamCrew        = "crew"
	TeamSupervisor  = "supervisor"
	TeamSwarm       = "swarm"
	TeamEvaluator   = "evaluator_optimizer"
	TeamDebate      = "debate"
	TeamPlanExecute = "plan_execute"
)

// Member is an agent of a team, by name.
type Member struct {
	AgentConfig

	Name string `json:"name"`
}

// TeamTask is one task of a crew, done by a member.
type TeamTask struct {
	Member         string `json:"member"`
	Description    string `json:"description"`
	ExpectedOutput string `json:"expected_output,omitempty"`
	// Async runs the task alongside the ones around it.
	Async bool `json:"async,omitempty"`
}

// FilesParams give file tools within a working directory.
type FilesParams struct {
	WorkingDir string `json:"working_dir,omitempty"`
	Read       bool   `json:"read,omitempty"`
	Write      bool   `json:"write,omitempty"`
	Edit       bool   `json:"edit,omitempty"`
	Search     bool   `json:"search,omitempty"`
}

// ShellParams give a shell tool.
type ShellParams struct {
	WorkingDir string   `json:"working_dir,omitempty"`
	Allowlist  []string `json:"allowlist,omitempty"`
	SafeMode   bool     `json:"safe_mode,omitempty"`
	Timeout    string   `json:"timeout,omitempty"`
}

// KVParams give a key-value store tool.
type KVParams struct {
	Bucket string `json:"bucket"`
}

// MCPParams connect an MCP server's tools.
type MCPParams struct {
	Command string   `json:"command,omitempty"`
	Args    []string `json:"args,omitempty"`
	URL     string   `json:"url,omitempty"`
	Allow   []string `json:"allow,omitempty"`
	Deny    []string `json:"deny,omitempty"`
}

// SkillsParams give the skills under a directory.
type SkillsParams struct {
	Root string `json:"root"`
}

// KnowledgeParams are a searchable set of documents.
type KnowledgeParams struct {
	Description  string `json:"description,omitempty"`
	Documents    string `json:"documents,omitempty"` // a path or a glob
	ChunkSize    int    `json:"chunk_size,omitempty"`
	ChunkOverlap int    `json:"chunk_overlap,omitempty"`
	TopK         int    `json:"top_k,omitempty"`
	// Embedding is the credential and model that embed the documents.
	EmbeddingCredential string `json:"embedding_credential,omitempty"`
	EmbeddingModel      string `json:"embedding_model,omitempty"`
	// Store is the vector store type (qdrant, weaviate, psql) and its options.
	Store        string         `json:"store,omitempty"`
	StoreOptions map[string]any `json:"store_options,omitempty"`
}
