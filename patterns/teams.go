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

package patterns

import (
	"fmt"
	"slices"
	"strings"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// Defaults of the patterns.
const (
	defaultMaxRounds   = 3
	defaultRounds      = 2
	defaultMaxParallel = 4
	// stepsPerWorker bounds a supervisor loop: each worker visit costs a
	// supervisor turn and a worker turn, with room for revisits.
	stepsPerWorker = 4
)

// Node ids of the patterns.
const (
	nodeGenerate   = "generate"
	nodeEvaluate   = "evaluate"
	nodeCheck      = "check"
	nodeRound      = "round"
	nodeGather     = "gather"
	nodeNextRound  = "next_round"
	nodeJudge      = "judge"
	nodePlan       = "plan"
	nodeExecute    = "execute"
	nodeSynthesize = "synthesize"
)

// Minimum number of agents in a swarm or a debate.
const minPeers = 2

// JSON Schema keywords and fields of the schemas the patterns declare.
const (
	schemaType     = "type"
	schemaObject   = "object"
	schemaProps    = "properties"
	schemaRequired = "required"
	fieldFeedback  = "feedback"
	fieldSteps     = "steps"
	typeString     = "string"
)

// Channels the patterns declare.
const (
	chanTranscript = "transcript"
	chanResults    = "results"
)

// Supervisor is a supervisor pattern: the supervisor agent routes to one
// worker at a time and every worker reports back, until the supervisor
// answers without routing. Node ids are the agents' names.
func Supervisor(name string, p spec.Pattern) (*flow.Flow, error) {
	if err := required([2]string{"supervisor", p.Supervisor}); err != nil {
		return nil, err
	}

	if err := distinct("workers", p.Workers, 1); err != nil {
		return nil, err
	}

	if slices.Contains(p.Workers, p.Supervisor) {
		return nil, perr("workers", "the supervisor %q cannot also be a worker", p.Supervisor)
	}

	b := newBuilder(name, maxStepsOr(p.MaxSteps, stepsPerWorker*len(p.Workers)+stepsPerWorker), p.Budget)
	b.agent(p.Supervisor, spec.Step{Agent: p.Supervisor, Prompt: p.Prompt, Routes: p.Workers})

	for _, w := range p.Workers {
		b.agent(w, spec.Step{Agent: w}).Next = p.Supervisor
	}

	return b.done(p.Supervisor)
}

// Swarm is a swarm pattern: every agent may hand off to any other, the
// first one starts, and the flow ends with the first agent that answers
// without handing off.
func Swarm(name string, p spec.Pattern) (*flow.Flow, error) {
	if err := distinct("agents", p.Agents, minPeers); err != nil {
		return nil, err
	}

	b := newBuilder(name, maxStepsOr(p.MaxSteps, stepsPerWorker*len(p.Agents)), p.Budget)

	for i, a := range p.Agents {
		s := spec.Step{Agent: a, Routes: others(p.Agents, a)}
		if i == 0 {
			s.Prompt = p.Prompt
		}

		b.agent(a, s)
	}

	return b.done(p.Agents[0])
}

// EvaluatorOptimizer generates, has the evaluator judge the result with
// structured output ({pass, feedback}), and regenerates with the feedback
// until it passes or MaxRounds generations ran. The output is the last
// generation.
func EvaluatorOptimizer(name string, p spec.Pattern) (*flow.Flow, error) {
	if err := required([2]string{"generator", p.Generator}, [2]string{"evaluator", p.Evaluator}); err != nil {
		return nil, err
	}

	rounds := p.MaxRounds
	if rounds <= 0 {
		rounds = defaultMaxRounds
	}

	b := newBuilder(name, p.MaxSteps, p.Budget)

	b.agent(nodeGenerate, spec.Step{Agent: p.Generator, Prompt: p.Prompt, Context: []string{nodeEvaluate}}).
		Next = nodeEvaluate

	ev := b.agent(nodeEvaluate, spec.Step{
		Agent:   p.Evaluator,
		Context: []string{nodeGenerate},
		Prompt: "Evaluate the output below against the task. Set pass to true when it is good enough; " +
			"otherwise explain what to improve in feedback.\n\nTask:\n{{json .input}}",
	})
	ev.OutputSchema = verdictSchema("pass")
	ev.Next = nodeCheck

	b.add(flow.Node{ID: nodeCheck, Type: flow.NodeChoice, Rules: []flow.Rule{
		{When: fmt.Sprintf("%s.pass == true", resultOf(nodeEvaluate)), To: flow.End},
		{When: fmt.Sprintf("visits[%q] < %d", nodeGenerate, rounds), To: nodeGenerate},
		{Default: true, To: flow.End},
	}})

	b.f.Output = resultOf(nodeGenerate)

	return b.done(nodeGenerate)
}

// Debate has the debaters argue in parallel for Rounds rounds, each seeing
// the transcript so far, then the judge decides. The output is the
// judge's answer.
func Debate(name string, p spec.Pattern) (*flow.Flow, error) {
	if err := distinct("debaters", p.Debaters, minPeers); err != nil {
		return nil, err
	}

	if err := required([2]string{"judge", p.Judge}); err != nil {
		return nil, err
	}

	if slices.Contains(p.Debaters, p.Judge) {
		return nil, perr("judge", "the judge %q cannot also debate", p.Judge)
	}

	rounds := p.Rounds
	if rounds <= 0 {
		rounds = defaultRounds
	}

	b := newBuilder(name, p.MaxSteps, p.Budget)
	b.f.Channels = map[string]flow.Channel{chanTranscript: {Reducer: flow.ReducerAppend}}

	b.add(flow.Node{ID: nodeRound, Type: flow.NodeFanout, Branches: p.Debaters, Next: nodeGather})

	topic := p.Prompt
	if topic == "" {
		topic = "{{json .input}}"
	}

	for _, d := range p.Debaters {
		b.agent(d, spec.Step{
			Agent: d,
			Prompt: "You are debating this topic:\n" + topic + "\n\nTranscript so far:\n{{json .channels.transcript}}" +
				"\n\nGive your argument for this round. Start with \"" + d + ":\".",
			Writes: map[string]string{chanTranscript: "output." + spec.OutputText},
		})
	}

	b.add(flow.Node{ID: nodeGather, Type: flow.NodeJoin, Next: nodeNextRound})
	b.add(flow.Node{ID: nodeNextRound, Type: flow.NodeChoice, Rules: []flow.Rule{
		{When: fmt.Sprintf("visits[%q] < %d", nodeRound, rounds), To: nodeRound},
		{Default: true, To: nodeJudge},
	}})
	b.agent(nodeJudge, spec.Step{
		Agent: p.Judge,
		Prompt: "Judge this debate on:\n" + topic + "\n\nTranscript:\n{{json .channels.transcript}}" +
			"\n\nDecide which position is strongest and explain why.",
	})

	b.f.Output = resultOf(nodeJudge)

	return b.done(nodeRound)
}

// PlanExecute has the planner list steps (structured output {steps}), the
// executor run each (MaxParallel at a time), and the synthesizer, when
// set, combine their results. The output is the synthesis, or the list of
// step results.
func PlanExecute(name string, p spec.Pattern) (*flow.Flow, error) {
	if err := required([2]string{"planner", p.Planner}, [2]string{"executor", p.Executor}); err != nil {
		return nil, err
	}

	maxPar := p.MaxParallel
	if maxPar <= 0 {
		maxPar = defaultMaxParallel
	}

	b := newBuilder(name, p.MaxSteps, p.Budget)
	b.f.Channels = map[string]flow.Channel{chanResults: {Reducer: flow.ReducerAppend}}

	prompt := p.Prompt
	if prompt == "" {
		prompt = "Plan this task:\n{{json .input}}"
	}

	plan := b.agent(nodePlan, spec.Step{
		Agent: p.Planner, Prompt: prompt + "\n\nBreak it into independent steps another agent can carry out.",
	})
	plan.OutputSchema = map[string]any{
		schemaType: schemaObject,
		schemaProps: map[string]any{
			fieldSteps: map[string]any{schemaType: "array", "items": map[string]any{schemaType: typeString}},
		},
		schemaRequired: []any{fieldSteps},
	}
	plan.Next = nodeExecute

	over := resultOf(nodePlan) + "." + fieldSteps

	exec := flow.Node{ID: nodeExecute, Type: flow.NodeMap, Over: over, MaxParallel: maxPar}
	if err := spec.ApplyStep(&exec, spec.Step{
		Agent:  p.Executor,
		Prompt: "Carry out this step:\n{{.item}}\n\nIt is part of this task:\n{{json .input}}",
		Writes: map[string]string{chanResults: "output." + spec.OutputText},
	}); err != nil {
		return nil, err
	}

	b.add(exec)

	if p.Synthesizer == "" {
		b.f.Output = fmt.Sprintf("{%q: channels[%q]}", chanResults, chanResults)

		return b.done(nodePlan)
	}

	b.f.Nodes[len(b.f.Nodes)-1].Next = nodeSynthesize
	b.agent(nodeSynthesize, spec.Step{
		Agent: p.Synthesizer,
		Prompt: "Combine these step results into the final answer to the task.\n\nTask:\n{{json .input}}" +
			"\n\nStep results:\n{{json .channels.results}}",
	})

	b.f.Output = resultOf(nodeSynthesize)

	return b.done(nodePlan)
}

// verdictSchema is the structured output of a judging step.
func verdictSchema(flag string) map[string]any {
	return map[string]any{
		schemaType: schemaObject,
		schemaProps: map[string]any{
			flag:          map[string]any{schemaType: "boolean"},
			fieldFeedback: map[string]any{schemaType: typeString},
		},
		schemaRequired: []any{flag, fieldFeedback},
	}
}

func maxStepsOr(set, def int) int {
	if set > 0 {
		return set
	}

	return def
}

// taskList renders tasks for a manager's prompt.
func taskList(tasks []spec.Task) string {
	var b strings.Builder

	for _, t := range tasks {
		fmt.Fprintf(&b, "- %s (agent %s): %s", t.ID, t.Agent, t.Description)

		if t.ExpectedOutput != "" {
			fmt.Fprintf(&b, " Expected output: %s", t.ExpectedOutput)
		}

		fmt.Fprintf(&b, "\n  Result: {{with index .results %q}}{{json .}}{{else}}(not done yet){{end}}\n", t.ID)
	}

	return b.String()
}
