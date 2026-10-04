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

// Package config loads a fleet from YAML into a [spec.Fleet].
//
// Decoding is strict: unknown keys are errors, reported with their line.
// Flows are written as packtrail flows whose task and map nodes may also set
// stiggy's step fields (agent, activity, prompt, writes, routes); the loader
// turns those into the node's kind, dynamic edges and meta with
// [spec.ApplyStep], so the result is identical to a fleet built in Go.
//
// String values outside `flows` may reference the environment: ${VAR},
// ${VAR:-default}, and $$ for a literal $. Flows are versioned definitions
// stored by packtrail, so they are never expanded: they must not depend on
// the environment, and must never hold secrets.
package config

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// ErrEmpty is returned for a document with no fleet in it.
var ErrEmpty = errors.New("config: empty fleet definition")

// ErrMultipleDocuments is returned when the input holds more than one YAML
// document.
var ErrMultipleDocuments = errors.New("config: one fleet per file: found several YAML documents")

// Option configures loading.
type Option func(*options)

type options struct {
	lookupEnv func(string) (string, bool)
	filename  string
	noEnv     bool
}

// WithLookupEnv sets the environment lookup; the default is os.LookupEnv.
func WithLookupEnv(fn func(string) (string, bool)) Option {
	return func(o *options) { o.lookupEnv = fn }
}

// WithoutEnv leaves ${VAR} references unexpanded, for offline checks of a
// fleet whose secrets are not available.
func WithoutEnv() Option {
	return func(o *options) { o.noEnv = true }
}

// WithFilename names the source in positions; LoadFile sets it to the path.
func WithFilename(name string) Option {
	return func(o *options) { o.filename = name }
}

// LoadFile reads and parses a fleet file.
func LoadFile(path string, opts ...Option) (*spec.Fleet, *Source, error) {
	data, err := os.ReadFile(path) //nolint:gosec // the path is the user's fleet file by design.
	if err != nil {
		return nil, nil, fmt.Errorf("config: %w", err)
	}

	return Parse(data, append([]Option{WithFilename(path)}, opts...)...)
}

// Parse parses a fleet. The [Source] maps error paths back to lines; it is
// returned even when parsing fails, as long as the YAML itself is readable.
func Parse(data []byte, opts ...Option) (*spec.Fleet, *Source, error) {
	o := options{lookupEnv: os.LookupEnv}
	for _, opt := range opts {
		opt(&o)
	}

	src, err := newSource(o.filename, data)
	if err != nil {
		return nil, nil, err
	}

	var f file
	if err = decodeStrict(data, &f); err != nil {
		return nil, src, err
	}

	fleet := f.Fleet

	var errs []error

	if !o.noEnv {
		exp := &expander{lookup: o.lookupEnv}
		exp.walkFleet(&fleet)
		errs = exp.errs
	}

	fleet.Flows = make(map[string]*flow.Flow, len(f.Flows))

	for name, ff := range f.Flows {
		fl, ferrs := ff.toFlow(name)
		errs = append(errs, ferrs...)
		fleet.Flows[name] = fl
	}

	if len(errs) > 0 {
		return nil, src, errors.Join(errs...)
	}

	return &fleet, src, nil
}

func decodeStrict(data []byte, out any) error {
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)

	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return ErrEmpty
		}

		return fmt.Errorf("config: %w", err)
	}

	var extra any

	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
		return nil
	case err != nil:
		return fmt.Errorf("config: %w", err)
	case extra != nil:
		return ErrMultipleDocuments
	default:
		return nil
	}
}

// file is the YAML shape of a fleet: the spec, with flows whose nodes may
// carry step fields.
type file struct {
	spec.Fleet `yaml:",inline"`

	Flows map[string]fileFlow `yaml:"flows,omitempty"`
}

// fileFlow mirrors [flow.Flow] with [fileNode] nodes. TestFileFlowMirrorsFlow
// keeps the two in sync when packtrail adds fields.
type fileFlow struct {
	Version          string                  `yaml:"version,omitempty"`
	Name             string                  `yaml:"name,omitempty"`
	Description      string                  `yaml:"description,omitempty"`
	Start            string                  `yaml:"start,omitempty"`
	Channels         map[string]flow.Channel `yaml:"channels,omitempty"`
	Output           string                  `yaml:"output,omitempty"`
	Nodes            []fileNode              `yaml:"nodes"`
	Budget           map[string]float64      `yaml:"budget,omitempty"`
	MaxSteps         int                     `yaml:"max_steps,omitempty"`
	SearchAttributes map[string]string       `yaml:"search_attributes,omitempty"`
	Retention        flow.Duration           `yaml:"retention,omitempty"`
	Triggers         []flow.Trigger          `yaml:"triggers,omitempty"`
}

// fileNode is a packtrail node plus stiggy's step fields.
type fileNode struct {
	flow.Node `yaml:",inline"`
	spec.Step `yaml:",inline"`
}

func (ff *fileFlow) toFlow(name string) (*flow.Flow, []error) {
	path := "flows." + name

	var errs []error

	if ff.Name != "" && ff.Name != name {
		errs = append(errs, &spec.Error{
			Path: path + ".name",
			Msg:  fmt.Sprintf("name %q does not match the flow key %q (omit it)", ff.Name, name),
		})
	}

	f := &flow.Flow{
		Version:          ff.Version,
		Name:             name,
		Description:      ff.Description,
		Start:            ff.Start,
		Channels:         ff.Channels,
		Output:           ff.Output,
		Nodes:            make([]flow.Node, len(ff.Nodes)),
		Budget:           ff.Budget,
		MaxSteps:         ff.MaxSteps,
		SearchAttributes: ff.SearchAttributes,
		Retention:        ff.Retention,
		Triggers:         ff.Triggers,
	}

	for i, fn := range ff.Nodes {
		f.Nodes[i] = fn.Node

		if err := spec.ApplyStep(&f.Nodes[i], fn.Step); err != nil {
			errs = append(errs, &spec.Error{Path: fmt.Sprintf("%s.nodes.%s", path, nodeKey(fn.ID, i)), Msg: err.Error()})
		}
	}

	return f, errs
}

func nodeKey(id string, i int) string {
	if id == "" {
		return fmt.Sprint(i)
	}

	return id
}
