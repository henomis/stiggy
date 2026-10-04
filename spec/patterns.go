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

package spec

// Crew processes.
const (
	ProcessSequential   = "sequential"
	ProcessHierarchical = "hierarchical"
)

// Pattern types.
const (
	PatternSupervisor         = "supervisor"
	PatternSwarm              = "swarm"
	PatternEvaluatorOptimizer = "evaluator_optimizer"
	PatternDebate             = "debate"
	PatternPlanExecute        = "plan_execute"
)

// Crew is a crew: agents working through tasks under a
// process. It compiles into a flow named after the crew.
type Crew struct {
	// Process is sequential (tasks in order; consecutive async tasks run in
	// parallel) or hierarchical (a manager agent assigns the tasks).
	Process string `yaml:"process,omitempty" json:"process,omitempty"`
	// Manager names the agent that assigns tasks in a hierarchical crew.
	Manager string `yaml:"manager,omitempty" json:"manager,omitempty"`
	Tasks   []Task `yaml:"tasks" json:"tasks"`
	// MaxSteps bounds the crew's flow (packtrail's recursion limit).
	MaxSteps int `yaml:"max_steps,omitempty" json:"max_steps,omitempty"`
	// Budget caps usage counters (tokens_out, agent_steps, cost_usd, ...).
	Budget map[string]float64 `yaml:"budget,omitempty" json:"budget,omitempty"`
}

// Task is one task of a crew.
type Task struct {
	ID    string `yaml:"id" json:"id"`
	Agent string `yaml:"agent" json:"agent"`
	// Description is the task's prompt (a template over the context, so
	// "{{.input.topic}}" interpolates crew inputs).
	Description    string `yaml:"description" json:"description"`
	ExpectedOutput string `yaml:"expected_output,omitempty" json:"expected_output,omitempty"`
	// Context lists earlier tasks whose outputs the task sees; empty means
	// the previous task.
	Context      []string `yaml:"context,omitempty" json:"context,omitempty"`
	OutputSchema any      `yaml:"output_schema,omitempty" json:"output_schema,omitempty"`
	HumanInput   bool     `yaml:"human_input,omitempty" json:"human_input,omitempty"`
	// Async runs the task in parallel with the consecutive async tasks
	// around it (sequential crews).
	Async bool `yaml:"async,omitempty" json:"async,omitempty"`
	// Guardrail has an agent check the task's output; a rejected output is
	// redone with the checker's feedback.
	Guardrail *Guardrail `yaml:"guardrail,omitempty" json:"guardrail,omitempty"`
}

// Guardrail checks a task's output with an agent.
type Guardrail struct {
	Agent string `yaml:"agent" json:"agent"`
	// MaxRetries bounds how often a rejected output is redone (default 2);
	// after that the output is accepted as it is.
	MaxRetries int `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
}

// Pattern is a multi-agent pattern that compiles into a flow named after
// it. Which fields apply depends on Type.
type Pattern struct {
	Type string `yaml:"type" json:"type"`

	// supervisor: Supervisor routes to Workers, which report back.
	Supervisor string   `yaml:"supervisor,omitempty" json:"supervisor,omitempty"`
	Workers    []string `yaml:"workers,omitempty" json:"workers,omitempty"`

	// swarm: each of Agents may hand off to any other; the first starts.
	Agents []string `yaml:"agents,omitempty" json:"agents,omitempty"`

	// evaluator_optimizer: Generator writes, Evaluator judges, up to
	// MaxRounds generations.
	Generator string `yaml:"generator,omitempty" json:"generator,omitempty"`
	Evaluator string `yaml:"evaluator,omitempty" json:"evaluator,omitempty"`
	MaxRounds int    `yaml:"max_rounds,omitempty" json:"max_rounds,omitempty"`

	// debate: Debaters argue for Rounds rounds, then Judge decides.
	Debaters []string `yaml:"debaters,omitempty" json:"debaters,omitempty"`
	Judge    string   `yaml:"judge,omitempty" json:"judge,omitempty"`
	Rounds   int      `yaml:"rounds,omitempty" json:"rounds,omitempty"`

	// plan_execute: Planner lists steps, Executor runs each (up to
	// MaxParallel at once), Synthesizer (optional) writes the result.
	Planner     string `yaml:"planner,omitempty" json:"planner,omitempty"`
	Executor    string `yaml:"executor,omitempty" json:"executor,omitempty"`
	Synthesizer string `yaml:"synthesizer,omitempty" json:"synthesizer,omitempty"`
	MaxParallel int    `yaml:"max_parallel,omitempty" json:"max_parallel,omitempty"`

	// Prompt is the first agent's prompt; empty means the input.
	Prompt   string             `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	MaxSteps int                `yaml:"max_steps,omitempty" json:"max_steps,omitempty"`
	Budget   map[string]float64 `yaml:"budget,omitempty" json:"budget,omitempty"`
}
