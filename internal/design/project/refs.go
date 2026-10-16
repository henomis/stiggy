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

package project

import (
	"fmt"
	"regexp"
	"strings"
)

// References point at values in a workflow: {{ref start topic}} is the
// workflow input "topic", {{ref <node> text}} the text answer of a block,
// {{ref <node> angles}} a field of its structured answer, {{ref item name}}
// a field of the current loop item. They name blocks by id, so renaming a
// block never breaks them.
var refPattern = regexp.MustCompile(`\{\{\s*ref\s+([A-Za-z0-9_-]+)(?:\s+([A-Za-z0-9_.]+))?\s*\}\}`)

// Reference roots that are not blocks.
const (
	RefStart = "start"
	RefItem  = "item"
)

// refInput is where workflow inputs live in a job.
const refInput = "input"

// ref is one parsed reference.
type ref struct {
	token string
	root  string
	path  string
}

func parseRefs(s string) []ref {
	ms := refPattern.FindAllStringSubmatch(s, -1)
	out := make([]ref, 0, len(ms))

	for _, m := range ms {
		out = append(out, ref{token: m[0], root: m[1], path: m[2]})
	}

	return out
}

// target is what a reference resolves to.
type target struct {
	// expr is the value as an expression: input.topic, results.plan.text,
	// item.name.
	expr string
	// scalar is true when the value is text, a number or a boolean; other
	// values are written into prompts as JSON.
	scalar bool
}

// resolve turns a reference into its target, or reports why it cannot.
func (w *wfc) resolve(r ref) (target, error) {
	tail := ""
	if r.path != "" {
		tail = "." + r.path
	}

	switch r.root {
	case RefStart:
		if r.path == "" {
			return target{expr: refInput}, nil
		}

		return target{expr: refInput + tail, scalar: scalarType(w.inputType(r.path))}, nil
	case RefItem:
		return target{expr: "item" + tail, scalar: r.path != ""}, nil
	}

	n, ok := w.byID[r.root]
	if !ok {
		return target{}, fmt.Errorf("refers to a block that no longer exists")
	}

	id, ok := w.idOf[n.ID]
	if !ok {
		return target{}, fmt.Errorf("refers to %q, which produces no value", n.Name)
	}

	return target{expr: "results." + id + tail, scalar: r.path != "" && scalarType(w.outputType(n, r.path))}, nil
}

func scalarType(t string) bool {
	return t == "" || t == FieldText || t == FieldNumber || t == FieldBoolean
}

// template rewrites the references of s for a Go template prompt.
func (w *wfc) template(s string) (string, error) {
	return w.rewrite(s, func(t target) string {
		if t.scalar {
			return "{{." + t.expr + "}}"
		}

		return "{{json ." + t.expr + "}}"
	})
}

func (w *wfc) rewrite(s string, write func(target) string) (string, error) {
	var errs []string

	out := refPattern.ReplaceAllStringFunc(s, func(tok string) string {
		r := parseRefs(tok)[0]

		t, err := w.resolve(r)
		if err != nil {
			errs = append(errs, err.Error())

			return tok
		}

		return write(t)
	})

	if len(errs) > 0 {
		return out, fmt.Errorf("%s", errs[0])
	}

	return out, nil
}

// onlyRef reports whether s is exactly one reference.
func onlyRef(s string) (ref, bool) {
	s = strings.TrimSpace(s)

	rs := parseRefs(s)
	if len(rs) != 1 || rs[0].token != s {
		return ref{}, false
	}

	return rs[0], true
}
