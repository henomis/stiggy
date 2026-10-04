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

package compile

import (
	"errors"
	"maps"
	"strings"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/patterns"
	"github.com/henomis/stiggy/spec"
)

// origin is where a generated flow comes from.
type origin struct {
	// path is "crews.<name>" or "patterns.<name>".
	path string
	// nodes is the path under which the source lists its nodes
	// ("crews.<name>.tasks"), empty when nodes have no source of their own.
	nodes string
}

// expand returns f with its crews and patterns expanded into flows; f is not
// modified.
func (c *compiler) expand(f *spec.Fleet) *spec.Fleet {
	if len(f.Crews) == 0 && len(f.Patterns) == 0 {
		return f
	}

	out := *f
	out.Flows = maps.Clone(f.Flows)

	if out.Flows == nil {
		out.Flows = map[string]*flow.Flow{}
	}

	gen := func(kind, name string, build func() (*flow.Flow, error)) {
		path := kind + "." + name

		if _, clash := out.Flows[name]; clash {
			c.errs = append(c.errs, &spec.Error{Path: path, Msg: "a flow with this name already exists"})

			return
		}

		fl, err := build()
		if err != nil {
			var se *spec.Error
			if errors.As(err, &se) {
				err = &spec.Error{Path: join(path, se.Path), Msg: se.Msg}
			}

			c.errs = append(c.errs, err)

			return
		}

		out.Flows[name] = fl

		o := origin{path: path}
		if kind == "crews" {
			o.nodes = path + ".tasks"
		}

		c.origins[name] = o
	}

	for _, name := range sortedKeys(f.Crews) {
		gen("crews", name, func() (*flow.Flow, error) { return patterns.Crew(name, f.Crews[name]) })
	}

	for _, name := range sortedKeys(f.Patterns) {
		gen("patterns", name, func() (*flow.Flow, error) { return patterns.Expand(name, f.Patterns[name]) })
	}

	return &out
}

// sourcePath maps a path inside a generated flow ("flows.<name>...") to the
// crew or pattern that generated it.
func (c *compiler) sourcePath(path string) string {
	rest, ok := strings.CutPrefix(path, "flows.")
	if !ok {
		return path
	}

	name, sub, _ := strings.Cut(rest, ".")

	o, generated := c.origins[name]
	if !generated {
		return path
	}

	if node, isNode := strings.CutPrefix(sub, "nodes."); isNode && o.nodes != "" {
		return join(o.nodes, node)
	}

	return o.path
}

func join(a, b string) string {
	switch {
	case a == "":
		return b
	case b == "":
		return a
	default:
		return a + "." + b
	}
}
