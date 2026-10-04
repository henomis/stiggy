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

package stiggy

import (
	"errors"
	"fmt"
	"log/slog"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/embedding"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/vectorstore"

	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

// Role selects which parts of a fleet a process runs. Every process of a
// deployment uses the same fleet definition.
type Role string

// Roles.
const (
	// RoleAll runs the engine and every worker: one process, typical in
	// development.
	RoleAll Role = "all"
	// RoleEngine runs the packtrail engine: it provisions the namespace,
	// registers the flows and schedules, and drives executions.
	RoleEngine Role = "engine"
	// RoleWorkers runs the agent and activity workers. Scale it out freely.
	RoleWorkers Role = "workers"
)

func (r Role) runsEngine() bool  { return r == RoleAll || r == RoleEngine }
func (r Role) runsWorkers() bool { return r == RoleAll || r == RoleWorkers }

// ErrOption is returned for an invalid option.
var ErrOption = errors.New("stiggy: invalid option")

// Option configures an [App].
type Option func(*options) error

type options struct {
	fleet      spec.Fleet
	activities map[string]worker.Handler
	registry   *registry.Registry
	role       Role
	logger     *slog.Logger
	engineOpts []packtrail.Option
	workerOpts []worker.Option
	only       []string
}

func newOptions() *options {
	return &options{
		fleet: spec.Fleet{
			Models:       map[string]spec.Model{},
			Tools:        map[string]spec.Tool{},
			Agents:       map[string]spec.Agent{},
			Flows:        map[string]*flow.Flow{},
			Schedules:    map[string]spec.Schedule{},
			Embedders:    map[string]spec.Embedder{},
			VectorStores: map[string]spec.VectorStore{},
			Knowledge:    map[string]spec.Knowledge{},
			Remotes:      map[string]spec.Remote{},
			Crews:        map[string]spec.Crew{},
			Patterns:     map[string]spec.Pattern{},
		},
		activities: map[string]worker.Handler{},
		role:       RoleAll,
	}
}

// put adds a named entry, refusing duplicates.
func put[V any](m map[string]V, what, name string, v V) error {
	if _, dup := m[name]; dup {
		return fmt.Errorf("%w: %s %q defined twice", ErrOption, what, name)
	}

	m[name] = v

	return nil
}

// WithFleet adds everything a fleet defines (typically loaded from YAML with
// package config). Other options may add to it; a name defined twice is an
// error.
func WithFleet(f *spec.Fleet) Option {
	return func(o *options) error {
		if f == nil {
			return fmt.Errorf("%w: nil fleet", ErrOption)
		}

		if f.Version != "" {
			o.fleet.Version = f.Version
		}

		if f.Namespace != "" {
			o.fleet.Namespace = f.Namespace
		}

		if f.Expose != nil {
			if err := mergeExpose(&o.fleet, *f.Expose); err != nil {
				return err
			}
		}

		return errors.Join(
			putAll(o.fleet.Models, "model", f.Models),
			putAll(o.fleet.Tools, "tool", f.Tools),
			putAll(o.fleet.Agents, "agent", f.Agents),
			putAll(o.fleet.Flows, "flow", f.Flows),
			putAll(o.fleet.Schedules, "schedule", f.Schedules),
			putAll(o.fleet.Embedders, "embedder", f.Embedders),
			putAll(o.fleet.VectorStores, "vector store", f.VectorStores),
			putAll(o.fleet.Knowledge, "knowledge", f.Knowledge),
			putAll(o.fleet.Remotes, "remote", f.Remotes),
			putAll(o.fleet.Crews, "crew", f.Crews),
			putAll(o.fleet.Patterns, "pattern", f.Patterns),
		)
	}
}

func putAll[V any](dst map[string]V, what string, src map[string]V) error {
	errs := make([]error, 0, len(src))

	for _, name := range sortedKeys(src) {
		errs = append(errs, put(dst, what, name, src[name]))
	}

	return errors.Join(errs...)
}

// WithNamespace sets the packtrail namespace (default "stiggy").
func WithNamespace(ns string) Option {
	return func(o *options) error {
		o.fleet.Namespace = ns

		return nil
	}
}

// WithModel adds a ready LLM client under name.
func WithModel(name string, l llm.LLM) Option {
	return func(o *options) error {
		if l == nil {
			return fmt.Errorf("%w: model %q is nil", ErrOption, name)
		}

		return put(o.fleet.Models, "model", name, spec.Model{Instance: l})
	}
}

// WithModelSpec adds a model built by a registered provider.
func WithModelSpec(name string, m spec.Model) Option {
	return func(o *options) error { return put(o.fleet.Models, "model", name, m) }
}

// WithTool adds ready tools under one name; agents list the name to get all
// of them.
func WithTool(name string, tools ...*llm.Tool) Option {
	return func(o *options) error {
		if len(tools) == 0 {
			return fmt.Errorf("%w: tool %q has no tools", ErrOption, name)
		}

		return put(o.fleet.Tools, "tool", name, spec.Tool{Instances: tools})
	}
}

// WithToolSpec adds a tool built by a registered tool type.
func WithToolSpec(name string, t spec.Tool) Option {
	return func(o *options) error { return put(o.fleet.Tools, "tool", name, t) }
}

// WithAgent adds an agent.
func WithAgent(name string, a spec.Agent) Option {
	return func(o *options) error { return put(o.fleet.Agents, "agent", name, a) }
}

// WithEmbedder adds a ready embedding client under name.
func WithEmbedder(name string, e embedding.Embedder) Option {
	return func(o *options) error {
		if e == nil {
			return fmt.Errorf("%w: embedder %q is nil", ErrOption, name)
		}

		return put(o.fleet.Embedders, "embedder", name, spec.Embedder{Instance: e})
	}
}

// WithVectorStore adds a ready vector store under name.
func WithVectorStore(name string, s vectorstore.Store) Option {
	return func(o *options) error {
		if s == nil {
			return fmt.Errorf("%w: vector store %q is nil", ErrOption, name)
		}

		return put(o.fleet.VectorStores, "vector store", name, spec.VectorStore{Instance: s})
	}
}

// WithKnowledge adds a searchable document collection; agents list its name
// in Knowledge to get a search tool.
func WithKnowledge(name string, k spec.Knowledge) Option {
	return func(o *options) error { return put(o.fleet.Knowledge, "knowledge", name, k) }
}

// WithCrew adds a crew; it runs as a flow named name.
func WithCrew(name string, c spec.Crew) Option {
	return func(o *options) error { return put(o.fleet.Crews, "crew", name, c) }
}

// WithPattern adds a multi-agent pattern (supervisor, swarm,
// evaluator_optimizer, debate, plan_execute); it runs as a flow named name.
// The patterns package builds the same flows for WithFlow.
func WithPattern(name string, p spec.Pattern) Option {
	return func(o *options) error { return put(o.fleet.Patterns, "pattern", name, p) }
}

// WithRemote adds an agent served by someone else on the NATS Agent
// Protocol; steps run it with Remote, agents list it in Delegates.
func WithRemote(name string, r spec.Remote) Option {
	return func(o *options) error { return put(o.fleet.Remotes, "remote", name, r) }
}

// WithExpose serves agents and flows on the NATS Agent Protocol. Several
// calls add up; the owner may be set once.
func WithExpose(e spec.Expose) Option {
	return func(o *options) error { return mergeExpose(&o.fleet, e) }
}

func mergeExpose(f *spec.Fleet, e spec.Expose) error {
	if f.Expose == nil {
		f.Expose = &spec.Expose{}
	}

	if e.Owner != "" {
		if f.Expose.Owner != "" && f.Expose.Owner != e.Owner {
			return fmt.Errorf("%w: expose owner set to both %q and %q", ErrOption, f.Expose.Owner, e.Owner)
		}

		f.Expose.Owner = e.Owner
	}

	f.Expose.Agents = append(f.Expose.Agents, e.Agents...)
	f.Expose.Flows = append(f.Expose.Flows, e.Flows...)

	return nil
}

// WithActivity adds a Go activity: steps with `activity: name` run h.
func WithActivity(name string, h worker.Handler) Option {
	return func(o *options) error {
		if h == nil {
			return fmt.Errorf("%w: activity %q is nil", ErrOption, name)
		}

		return put(o.activities, "activity", name, h)
	}
}

// WithFlow adds a flow. Build agent and activity nodes with [AgentTask],
// [ActivityTask] or spec.ApplyStep.
func WithFlow(f *flow.Flow) Option {
	return func(o *options) error {
		if f == nil {
			return fmt.Errorf("%w: nil flow", ErrOption)
		}

		return put(o.fleet.Flows, "flow", f.Name, f)
	}
}

// WithSchedule adds a cron schedule.
func WithSchedule(name string, s spec.Schedule) Option {
	return func(o *options) error { return put(o.fleet.Schedules, "schedule", name, s) }
}

// WithRegistry sets the registry of providers, tool types and activities
// (default registry.New()). Activities added with [WithActivity] are
// registered into it.
func WithRegistry(r *registry.Registry) Option {
	return func(o *options) error {
		if r == nil {
			return fmt.Errorf("%w: nil registry", ErrOption)
		}

		o.registry = r

		return nil
	}
}

// WithRole sets which parts of the fleet this process runs (default RoleAll).
func WithRole(r Role) Option {
	return func(o *options) error {
		switch r {
		case RoleAll, RoleEngine, RoleWorkers:
			o.role = r

			return nil
		default:
			return fmt.Errorf("%w: unknown role %q", ErrOption, r)
		}
	}
}

// WithLogger sets the logger (default slog.Default()).
func WithLogger(l *slog.Logger) Option {
	return func(o *options) error {
		o.logger = l

		return nil
	}
}

// WithOnly limits the workers this process runs to the named agents,
// activities and remotes (and the exposed agents to the named agents), so a
// deployment can place, say, GPU-bound or secret-holding agents on their own
// hosts. Other processes of the deployment run the rest.
func WithOnly(names ...string) Option {
	return func(o *options) error {
		o.only = append(o.only, names...)

		return nil
	}
}

// WithEngineOptions passes options to packtrail.New, after stiggy's own.
func WithEngineOptions(opts ...packtrail.Option) Option {
	return func(o *options) error {
		o.engineOpts = append(o.engineOpts, opts...)

		return nil
	}
}

// WithWorkerOptions passes options to every worker.New, after stiggy's own.
func WithWorkerOptions(opts ...worker.Option) Option {
	return func(o *options) error {
		o.workerOpts = append(o.workerOpts, opts...)

		return nil
	}
}

// AgentTask returns a task node that runs agent with step's other settings
// (prompt, writes, routes); set the node's other fields (next, retry,
// timeout, ...) on the result.
func AgentTask(id, agent string, step spec.Step) (flow.Node, error) {
	step.Agent = agent
	n := flow.Node{ID: id, Type: flow.NodeTask}

	return n, spec.ApplyStep(&n, step)
}

// ActivityTask returns a task node that runs a Go activity.
func ActivityTask(id, activity string, step spec.Step) (flow.Node, error) {
	step.Activity = activity
	n := flow.Node{ID: id, Type: flow.NodeTask}

	return n, spec.ApplyStep(&n, step)
}
