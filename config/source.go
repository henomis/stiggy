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

package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/henomis/stiggy/spec"
)

// Source maps the dotted paths of [spec.Error] values back to lines of the
// YAML they came from.
type Source struct {
	// Filename names the source; empty for in-memory input.
	Filename string

	lines map[string]int
}

func newSource(filename string, data []byte) (*Source, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}

	s := &Source{Filename: filename, lines: map[string]int{}}

	if len(root.Content) > 0 {
		s.record("", root.Content[0])
	}

	return s, nil
}

// record walks a YAML node, recording the line of each mapping key and of
// each sequence item. Items that are mappings with an "id" are addressed by
// that id, like flow nodes in error paths.
func (s *Source) record(path string, n *yaml.Node) {
	switch n.Kind {
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			key, val := n.Content[i], n.Content[i+1]
			p := join(path, key.Value)
			s.lines[p] = key.Line
			s.record(p, val)
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			p := join(path, itemKey(item, i))
			s.lines[p] = item.Line
			s.record(p, item)
		}
	case yaml.DocumentNode, yaml.ScalarNode, yaml.AliasNode:
	}
}

func itemKey(item *yaml.Node, i int) string {
	if item.Kind == yaml.MappingNode {
		for j := 0; j+1 < len(item.Content); j += 2 {
			if item.Content[j].Value == "id" && item.Content[j+1].Kind == yaml.ScalarNode {
				return item.Content[j+1].Value
			}
		}
	}

	return strconv.Itoa(i)
}

func join(path, key string) string {
	switch {
	case path == "":
		return key
	case key == "":
		return path
	}

	return path + "." + key
}

// Line returns the line of path, or of its closest recorded ancestor; 0 when
// nothing matches.
func (s *Source) Line(path string) int {
	if s == nil {
		return 0
	}

	for p := path; p != ""; {
		if l, ok := s.lines[p]; ok {
			return l
		}

		i := strings.LastIndexByte(p, '.')
		if i < 0 {
			break
		}

		p = p[:i]
	}

	return 0
}

// Describe flattens err into one message per problem. Each [*spec.Error] is
// prefixed with its file and line when they are known.
func (s *Source) Describe(err error) []string {
	var out []string

	for _, e := range leaves(err) {
		var se *spec.Error
		if !errors.As(e, &se) {
			out = append(out, e.Error())

			continue
		}

		out = append(out, s.position(se.Path)+se.Error())
	}

	return out
}

func (s *Source) position(path string) string {
	name := "<input>"
	if s != nil && s.Filename != "" {
		name = s.Filename
	}

	if l := s.Line(path); l > 0 {
		return fmt.Sprintf("%s:%d: ", name, l)
	}

	return name + ": "
}

// leaves splits joined errors into the individual problems.
func leaves(err error) []error {
	multi, ok := err.(interface{ Unwrap() []error }) //nolint:errorlint // splitting a join, not a chain.
	if !ok {
		return []error{err}
	}

	var out []error
	for _, e := range multi.Unwrap() {
		out = append(out, leaves(e)...)
	}

	return out
}
