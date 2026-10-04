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

// Package stepio is what agent and activity steps share: reading the step
// from a job, the job context as template data, and the step's writes.
package stepio

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy/spec"
)

// ErrNoStep is returned when a job's node carries no stiggy step.
var ErrNoStep = errors.New("stepio: the node has no stiggy step in its meta")

// ErrMissingField is returned when a write path names a field the output
// does not have.
var ErrMissingField = errors.New("stepio: output has no such field")

// Step decodes the job's step. Errors are permanent: the flow version is
// fixed, so retrying cannot fix its meta.
func Step(job *worker.Job) (spec.Step, error) {
	var env map[string]json.RawMessage
	if err := job.DecodeMeta(&env); err != nil {
		return spec.Step{}, worker.Permanent(fmt.Errorf("stepio: meta: %w", err))
	}

	raw, ok := env[spec.MetaKey]
	if !ok {
		return spec.Step{}, worker.Permanent(ErrNoStep)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()

	var s spec.Step
	if err := dec.Decode(&s); err != nil {
		return spec.Step{}, worker.Permanent(fmt.Errorf("stepio: meta.%s: %w", spec.MetaKey, err))
	}

	return s, nil
}

// Data returns the job context as template data: input, channels, results,
// item, index, last_node, visits, signals, errors, counters, resume.
func Data(job *worker.Job) (map[string]any, error) {
	c := job.Context

	d := map[string]any{
		"last_node": c.LastNode,
		"visits":    c.Visits,
		"counters":  c.Counters,
		"errors":    c.Errors,
		"branches":  c.Branches,
	}

	if c.Index != nil {
		d["index"] = *c.Index
	}

	raws := map[string]json.RawMessage{"input": c.Input, "item": c.Item, "resume": c.Resume}
	for k, raw := range raws {
		v, err := decode(raw)
		if err != nil {
			return nil, fmt.Errorf("stepio: %s: %w", k, err)
		}

		d[k] = v
	}

	for k, m := range map[string]map[string]json.RawMessage{
		"channels": c.Channels, "results": c.Results, "signals": c.Signals,
	} {
		out := make(map[string]any, len(m))

		for name, raw := range m {
			v, err := decode(raw)
			if err != nil {
				return nil, fmt.Errorf("stepio: %s.%s: %w", k, name, err)
			}

			out[name] = v
		}

		d[k] = out
	}

	return d, nil
}

func decode(raw json.RawMessage) (any, error) {
	if len(raw) == 0 {
		return nil, nil //nolint:nilnil // absent and null are the same here.
	}

	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}

	return v, nil
}

// Render formats a context value for a prompt: strings as they are,
// anything else as indented JSON.
func Render(v any) string {
	if s, ok := v.(string); ok {
		return s
	}

	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}

	return string(b)
}

// Writes computes the channel writes of a step from its output. A path below
// the output ("output.a.b") needs a structured output; a string output is
// parsed as JSON first.
func Writes(s spec.Step, output any) (map[string]any, error) {
	if len(s.Writes) == 0 {
		return nil, nil
	}

	writes := make(map[string]any, len(s.Writes))

	for ch, path := range s.Writes {
		fields, err := spec.ParseWritePath(path)
		if err != nil {
			return nil, worker.Permanent(err)
		}

		v, err := lookup(output, fields)
		if err != nil {
			return nil, fmt.Errorf("writes %s: %w", ch, err)
		}

		writes[ch] = v
	}

	return writes, nil
}

func lookup(output any, fields []string) (any, error) {
	if len(fields) == 0 {
		return output, nil
	}

	v := output

	if s, ok := v.(string); ok {
		if err := json.Unmarshal([]byte(s), &v); err != nil {
			return nil, fmt.Errorf("%w %q: the output is not JSON", ErrMissingField, strings.Join(fields, "."))
		}
	} else {
		// Normalize Go values (structs from activities) to JSON shapes.
		b, err := json.Marshal(v)
		if err != nil {
			return nil, fmt.Errorf("encode output: %w", err)
		}

		if err = json.Unmarshal(b, &v); err != nil {
			return nil, fmt.Errorf("encode output: %w", err)
		}
	}

	for i, f := range fields {
		m, ok := v.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("%w %q", ErrMissingField, strings.Join(fields[:i+1], "."))
		}

		if v, ok = m[f]; !ok {
			return nil, fmt.Errorf("%w %q", ErrMissingField, strings.Join(fields[:i+1], "."))
		}
	}

	return v, nil
}
