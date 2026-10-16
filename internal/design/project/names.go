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
	"strconv"
	"strings"
	"unicode"
)

// slug turns a label into an identifier that works as a YAML key, a template
// field (.results.<slug>) and an expression name: lower-case letters, digits
// and underscores, starting with a letter.
func slug(s, fallback string) string {
	var b strings.Builder

	under := false

	for _, r := range strings.ToLower(s) {
		switch {
		case r < unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			b.WriteRune(r)

			under = false
		case !under && b.Len() > 0:
			b.WriteByte('_')

			under = true
		}
	}

	out := strings.TrimRight(b.String(), "_")
	if out == "" {
		out = fallback
	}

	if out[0] >= '0' && out[0] <= '9' {
		out = fallback + "_" + out
	}

	return out
}

// names hands out unique names within one scope.
type names map[string]bool

// take returns base, or base_2, base_3... if taken.
func (n names) take(base string) string {
	name := base
	for i := 2; n[name]; i++ {
		name = base + "_" + strconv.Itoa(i)
	}

	n[name] = true

	return name
}

// section is one top-level map of the fleet (models, agents...): entries in
// order, merged when equal.
type section struct {
	order []string
	vals  map[string]omap
	byKey map[string]string
	names names
}

func newSection() *section {
	return &section{vals: map[string]omap{}, byKey: map[string]string{}, names: names{}}
}

// add stores v under a name made from base, or returns the name of an equal
// entry already there. added is false when it merged.
func (s *section) add(base string, v omap) (name string, added bool) {
	k := key(v)
	if n, ok := s.byKey[k]; ok {
		return n, false
	}

	name = s.names.take(base)
	s.order = append(s.order, name)
	s.vals[name] = v
	s.byKey[k] = name

	return name, true
}

func (s *section) omap() omap {
	m := make(omap, 0, len(s.order))
	for _, n := range s.order {
		m = append(m, kv{n, s.vals[n]})
	}

	return m
}
