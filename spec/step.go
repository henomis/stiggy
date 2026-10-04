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

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/henomis/packtrail/flow"
)

// MetaKey is the key under which a node's [Step] is stored in its meta.
// Other meta keys are left to the user.
const MetaKey = "stiggy"

// Worker kind prefixes. Kinds with these prefixes belong to stiggy; any other
// kind on a plain task node is left to external packtrail workers.
const (
	AgentKindPrefix    = "agent-"
	ActivityKindPrefix = "act-"
	RemoteKindPrefix   = "remote-"
)

// OutputText is the field holding an agent's answer when its step has no
// output schema: packtrail outputs are JSON objects, so a plain answer is
// {"text": "..."} and `writes: {ch: output.text}` stores the text itself.
const OutputText = "text"

// MemoryChannelPrefix starts the name of the hidden channel that holds an
// agent's execution memory; user channels must not use it.
const MemoryChannelPrefix = "_mem_"

// MemoryChannel returns the channel holding agent's execution memory.
func MemoryChannel(agent string) string { return MemoryChannelPrefix + agent }

// RouteHuman is the reserved route that hands control to a human: the step
// is interrupted and resumes with the human's answer.
const RouteHuman = "human"

// Errors returned by [ApplyStep] and [StepOf].
var (
	ErrStepTarget   = errors.New("spec: a step runs exactly one of agent, activity or remote")
	ErrStepNodeType = errors.New("spec: agent, activity and remote steps must be task or map nodes")
	ErrStepConflict = errors.New("spec: field is derived from the step")
)

// Step is the stiggy part of a task or map node: what runs and how its result
// flows back into the execution. It travels to the worker in the node's meta,
// so it is versioned with the flow and must never hold secrets.
type Step struct {
	// Agent names an entry of [Fleet.Agents].
	Agent string `yaml:"agent,omitempty" json:"agent,omitempty"`
	// Activity names a registered activity.
	Activity string `yaml:"activity,omitempty" json:"activity,omitempty"`
	// Remote names an entry of [Fleet.Remotes]: an agent served by someone
	// else, prompted over the NATS Agent Protocol.
	Remote string `yaml:"remote,omitempty" json:"remote,omitempty"`
	// Prompt is a Go text/template rendered over the job context (see
	// [ParsePrompt]). Empty means a default prompt built from the context.
	Prompt string `yaml:"prompt,omitempty" json:"prompt,omitempty"`
	// Writes maps flow channels to parts of the step output: "output" is the
	// whole output, "output.a.b" a field of it.
	Writes map[string]string `yaml:"writes,omitempty" json:"writes,omitempty"`
	// Routes are the nodes the agent may choose to continue with, plus the
	// reserved [RouteHuman]. They become the node's dynamic edges.
	Routes []string `yaml:"routes,omitempty" json:"routes,omitempty"`
	// ExpectedOutput describes the answer the step wants; it is appended to the prompt.
	ExpectedOutput string `yaml:"expected_output,omitempty" json:"expected_output,omitempty"`
	// Context lists the nodes whose results the default prompt includes
	// (the task context). Empty means the previous node.
	Context []string `yaml:"context,omitempty" json:"context,omitempty"`
	// HumanInput asks a human to review the agent's answer before the step
	// completes: the execution pauses until it is approved, or re-runs the
	// agent with the human's feedback.
	HumanInput bool `yaml:"human_input,omitempty" json:"human_input,omitempty"`
}

// IsZero reports whether s configures nothing.
func (s Step) IsZero() bool {
	return s.targets() == 0 && s.Prompt == "" && len(s.Writes) == 0 && len(s.Routes) == 0 &&
		s.ExpectedOutput == "" && len(s.Context) == 0 && !s.HumanInput
}

// Kind returns the packtrail worker kind that runs the step.
func (s Step) Kind() string {
	switch {
	case s.Agent != "":
		return AgentKind(s.Agent)
	case s.Remote != "":
		return RemoteKind(s.Remote)
	default:
		return ActivityKind(s.Activity)
	}
}

// targets counts the step's targets (agent, activity, remote).
func (s Step) targets() int {
	n := 0

	for _, t := range []string{s.Agent, s.Activity, s.Remote} {
		if t != "" {
			n++
		}
	}

	return n
}

// RemoteKind returns the worker kind of a remote agent.
func RemoteKind(remote string) string { return RemoteKindPrefix + remote }

// AgentKind returns the worker kind of an agent.
func AgentKind(agent string) string { return AgentKindPrefix + agent }

// ActivityKind returns the worker kind of an activity.
func ActivityKind(activity string) string { return ActivityKindPrefix + activity }

// IsStiggyKind reports whether kind uses one of stiggy's reserved prefixes.
func IsStiggyKind(kind string) bool {
	return strings.HasPrefix(kind, AgentKindPrefix) || strings.HasPrefix(kind, ActivityKindPrefix) ||
		strings.HasPrefix(kind, RemoteKindPrefix)
}

// DynamicTargets returns the routes that are flow nodes, i.e. all routes but
// [RouteHuman].
func (s Step) DynamicTargets() []string {
	var out []string

	for _, r := range s.Routes {
		if r != RouteHuman {
			out = append(out, r)
		}
	}

	return out
}

// ApplyStep makes n run s: it sets the worker kind, the dynamic edges implied
// by the routes, and the step meta. It refuses to override a kind, dynamic
// edges or meta key the node already sets, since those are derived from s.
// A zero step leaves n unchanged.
func ApplyStep(n *flow.Node, s Step) error {
	if s.IsZero() {
		return nil
	}

	if s.targets() != 1 {
		return fmt.Errorf("node %q: %w", n.ID, ErrStepTarget)
	}

	if (n.Type != flow.NodeTask && n.Type != flow.NodeMap) || n.Flow != "" {
		return fmt.Errorf("node %q: %w", n.ID, ErrStepNodeType)
	}

	if n.Kind != "" {
		return fmt.Errorf("node %q: kind: %w", n.ID, ErrStepConflict)
	}

	if len(s.Routes) > 0 && len(n.Dynamic) > 0 {
		return fmt.Errorf("node %q: dynamic: %w (use routes)", n.ID, ErrStepConflict)
	}

	if _, ok := n.Meta[MetaKey]; ok {
		return fmt.Errorf("node %q: meta.%s: %w", n.ID, MetaKey, ErrStepConflict)
	}

	meta, err := s.metaValue()
	if err != nil {
		return fmt.Errorf("node %q: %w", n.ID, err)
	}

	n.Kind = s.Kind()
	n.Dynamic = s.DynamicTargets()

	if n.Meta == nil {
		n.Meta = map[string]any{}
	}

	n.Meta[MetaKey] = meta

	return nil
}

// metaValue encodes s in the JSON shape packtrail stores, so a flow built in
// Go hashes the same as one parsed from YAML or JSON.
func (s Step) metaValue() (map[string]any, error) {
	b, err := json.Marshal(s)
	if err != nil {
		return nil, fmt.Errorf("encode step: %w", err)
	}

	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, fmt.Errorf("encode step: %w", err)
	}

	return m, nil
}

// StepOf returns the step stored in n's meta and whether there is one.
func StepOf(n *flow.Node) (Step, bool, error) {
	raw, ok := n.Meta[MetaKey]
	if !ok {
		return Step{}, false, nil
	}

	b, err := json.Marshal(raw)
	if err != nil {
		return Step{}, false, fmt.Errorf("node %q: meta.%s: %w", n.ID, MetaKey, err)
	}

	dec := json.NewDecoder(strings.NewReader(string(b)))
	dec.DisallowUnknownFields()

	var s Step
	if err = dec.Decode(&s); err != nil {
		return Step{}, false, fmt.Errorf("node %q: meta.%s: %w", n.ID, MetaKey, err)
	}

	return s, true, nil
}

// HasRoute reports whether s may route to target.
func (s Step) HasRoute(target string) bool { return slices.Contains(s.Routes, target) }
