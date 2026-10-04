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

// Package compile validates a [spec.Fleet] and turns it into a [Plan]: the
// validated packtrail flows, the workers that run their steps and the
// schedules. Compile is a pure function: it does no I/O, so `stiggy validate`,
// `stiggy compile` and a Go test exercise exactly the same checks.
package compile

import (
	"errors"
	"fmt"
	"maps"
	"slices"
	"sort"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// Catalog tells the compiler which names are registered at runtime: LLM
// providers, tool types and activities. Package registry provides the
// runtime implementation; [StaticCatalog] is a fixed one.
type Catalog interface {
	HasProvider(name string) bool
	HasToolType(name string) bool
	HasActivity(name string) bool
	HasMemoryType(name string) bool
	HasEmbedderProvider(name string) bool
	HasVectorStoreType(name string) bool
}

// ToolValidator is implemented by catalogs that can check a tool's options
// without building it, so problems surface at validation time.
type ToolValidator interface {
	ValidateTool(t spec.Tool) error
}

// MemoryValidator checks a long-term memory's type-specific options.
type MemoryValidator interface {
	ValidateMemory(m spec.Memory) error
}

// VectorStoreValidator checks a vector store's type-specific options.
type VectorStoreValidator interface {
	ValidateVectorStore(v spec.VectorStore) error
}

// StaticCatalog is a [Catalog] over fixed lists.
type StaticCatalog struct {
	Providers         []string
	ToolTypes         []string
	Activities        []string
	MemoryTypes       []string
	EmbedderProviders []string
	VectorStoreTypes  []string
}

// HasProvider implements [Catalog].
func (c StaticCatalog) HasProvider(name string) bool { return slices.Contains(c.Providers, name) }

// HasToolType implements [Catalog].
func (c StaticCatalog) HasToolType(name string) bool { return slices.Contains(c.ToolTypes, name) }

// HasActivity implements [Catalog].
func (c StaticCatalog) HasActivity(name string) bool { return slices.Contains(c.Activities, name) }

// HasMemoryType implements [Catalog].
func (c StaticCatalog) HasMemoryType(name string) bool { return slices.Contains(c.MemoryTypes, name) }

// HasEmbedderProvider implements [Catalog].
func (c StaticCatalog) HasEmbedderProvider(name string) bool {
	return slices.Contains(c.EmbedderProviders, name)
}

// HasVectorStoreType implements [Catalog].
func (c StaticCatalog) HasVectorStoreType(name string) bool {
	return slices.Contains(c.VectorStoreTypes, name)
}

// Plan is a validated, deployable fleet.
type Plan struct {
	// Namespace is the packtrail namespace.
	Namespace string `json:"namespace"`
	// Flows are the validated flows, sorted by name.
	Flows []*flow.Flow `json:"flows"`
	// Workers are the stiggy workers the flows need, sorted by kind.
	Workers []Worker `json:"workers"`
	// Schedules are the cron schedules, sorted by name.
	Schedules []Schedule `json:"schedules,omitempty"`
}

// Worker is one packtrail worker kind stiggy runs.
type Worker struct {
	Kind     string `json:"kind"`
	Agent    string `json:"agent,omitempty"`
	Activity string `json:"activity,omitempty"`
	Remote   string `json:"remote,omitempty"`
}

// Schedule is a named [spec.Schedule].
type Schedule struct {
	spec.Schedule

	Name string `json:"name"`
}

// Compile validates f against cat and builds its plan. It reports every
// problem it finds, each as a [*spec.Error], joined with errors.Join; all of
// them match [spec.ErrInvalid].
// f is not modified.
func Compile(f *spec.Fleet, cat Catalog) (*Plan, error) {
	c := &compiler{cat: cat, kinds: map[string]Worker{}, origins: map[string]origin{}}
	c.fleet = c.expand(f)

	c.fleetLevel()
	c.models()
	c.tools()
	c.knowledge()
	c.remotes()
	c.agents()
	c.expose()
	flows := c.flows()
	scheds := c.schedules()

	if len(c.errs) > 0 {
		return nil, errors.Join(c.errs...)
	}

	workers := slices.Collect(maps.Values(c.kinds))
	sort.Slice(workers, func(i, j int) bool { return workers[i].Kind < workers[j].Kind })

	return &Plan{Namespace: f.NamespaceOrDefault(), Flows: flows, Workers: workers, Schedules: scheds}, nil
}

type compiler struct {
	fleet   *spec.Fleet
	origins map[string]origin
	cat     Catalog
	errs    []error
	kinds   map[string]Worker
}

func (c *compiler) errorf(path, format string, args ...any) {
	c.add(path, fmt.Sprintf(format, args...))
}

// add records a problem, mapping paths inside a generated flow back to the
// crew or pattern it came from.
func (c *compiler) add(path, msg string) {
	c.errs = append(c.errs, &spec.Error{Path: c.sourcePath(path), Msg: msg})
}

func (c *compiler) fleetLevel() {
	if v := c.fleet.Version; v != "" && v != spec.Version {
		c.errorf("version", "unsupported version %q (want %q)", v, spec.Version)
	}

	if err := packtrail.ValidateNamespace(c.fleet.NamespaceOrDefault()); err != nil {
		c.errorf("namespace", "%v", err)
	}
}

func (c *compiler) models() {
	for _, name := range sortedKeys(c.fleet.Models) {
		m := c.fleet.Models[name]
		path := "models." + name

		c.checkName(path, "model", name)

		if m.Instance != nil {
			continue
		}

		switch {
		case m.Provider == "":
			c.errorf(path+".provider", "provider is required")
		case !c.cat.HasProvider(m.Provider):
			c.errorf(path+".provider", "unknown provider %q", m.Provider)
		}

		if m.MaxTokens < 0 {
			c.errorf(path+".max_tokens", "must not be negative")
		}

		if m.Retries < 0 {
			c.errorf(path+".retries", "must not be negative")
		}

		if r := m.RateLimit; r != nil && (r.RequestsPerMinute <= 0 || r.MaxConcurrent < 0) {
			c.errorf(path+".rate_limit", "requests_per_minute must be positive and max_concurrent not negative")
		}
	}
}

func (c *compiler) tools() {
	for _, name := range sortedKeys(c.fleet.Tools) {
		t := c.fleet.Tools[name]
		path := "tools." + name

		c.checkName(path, "tool", name)

		if len(t.Instances) > 0 {
			continue
		}

		switch {
		case t.Type == "":
			c.errorf(path+".type", "type is required")
		case !c.cat.HasToolType(t.Type):
			c.errorf(path+".type", "unknown tool type %q", t.Type)
		default:
			if v, ok := c.cat.(ToolValidator); ok {
				if err := v.ValidateTool(t); err != nil {
					c.errorf(path, "%v", err)
				}
			}
		}
	}
}

func (c *compiler) schedules() []Schedule {
	out := make([]Schedule, 0, len(c.fleet.Schedules))

	for _, name := range sortedKeys(c.fleet.Schedules) {
		s := c.fleet.Schedules[name]
		path := "schedules." + name

		c.checkName(path, "schedule", name)

		if _, ok := c.fleet.Flows[s.Flow]; !ok {
			c.errorf(path+".flow", "unknown flow %q", s.Flow)
		}

		if err := packtrail.ValidateCron(s.Cron); err != nil {
			c.errorf(path+".cron", "%v", err)
		}

		out = append(out, Schedule{Schedule: s, Name: name})
	}

	return out
}

func (c *compiler) checkName(path, what, name string) {
	if err := packtrail.ValidateName(what+" name", name); err != nil {
		c.errorf(path, "%v", err)
	}
}

func sortedKeys[V any](m map[string]V) []string {
	return slices.Sorted(maps.Keys(m))
}

func firstDuplicate(xs []string) string {
	seen := map[string]bool{}

	for _, x := range xs {
		if seen[x] {
			return x
		}

		seen[x] = true
	}

	return ""
}
