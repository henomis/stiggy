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
	"fmt"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"github.com/henomis/stiggy/spec"
)

// escapedDollar is the reference that stands for a literal "$".
const escapedDollar = "$$"

// envRef matches $$ and ${VAR} / ${VAR:-default}.
var envRef = regexp.MustCompile(`\$\$|\$\{([A-Za-z_][A-Za-z0-9_]*)(:-([^}]*))?\}`)

// expander replaces environment references in every string of a fleet.
type expander struct {
	lookup func(string) (string, bool)
	errs   []error
	// forbid reports environment references as errors instead of expanding
	// them, for the parts of a fleet that compile into flows.
	forbid bool
}

// walkFleet expands everything but flows, which are versioned definitions
// and must not depend on the environment. Crews and patterns compile into
// flows, so a reference in them is an error rather than silently literal.
func (e *expander) walkFleet(f *spec.Fleet) {
	v := reflect.ValueOf(f).Elem()
	t := v.Type()

	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() || field.Name == "Flows" {
			continue
		}

		e.forbid = field.Name == "Crews" || field.Name == "Patterns"
		e.walk(v.Field(i), yamlName(field))
	}
}

// walk expands the strings reachable from v, which must be settable.
func (e *expander) walk(v reflect.Value, path string) {
	switch v.Kind() { //nolint:exhaustive // only containers and strings hold strings.
	case reflect.String:
		if e.forbid {
			e.reject(v.String(), path)

			return
		}

		v.SetString(e.expand(v.String(), path))
	case reflect.Pointer:
		if !v.IsNil() {
			e.walk(v.Elem(), path)
		}
	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			if f := t.Field(i); f.IsExported() {
				e.walk(v.Field(i), join(path, yamlName(f)))
			}
		}
	case reflect.Slice:
		for i := range v.Len() {
			e.walk(v.Index(i), join(path, strconv.Itoa(i)))
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(iter.Value())
			e.walk(elem, join(path, fmt.Sprint(iter.Key().Interface())))
			v.SetMapIndex(iter.Key(), elem)
		}
	case reflect.Interface:
		if v.IsNil() {
			return
		}

		inner := reflect.New(v.Elem().Type()).Elem()
		inner.Set(v.Elem())
		e.walk(inner, path)
		v.Set(inner)
	}
}

// reject reports every ${VAR} reference in s; "$$" is not one.
func (e *expander) reject(s, path string) {
	for _, ref := range envRef.FindAllString(s, -1) {
		if ref == escapedDollar {
			continue
		}

		e.errs = append(e.errs, &spec.Error{
			Path: path,
			Msg: fmt.Sprintf("%s: environment references are not allowed in crews and patterns, "+
				"which compile into flows; use inputs ({{.input.x}}) instead", ref),
		})
	}
}

func (e *expander) expand(s, path string) string {
	if !strings.Contains(s, "$") {
		return s
	}

	return envRef.ReplaceAllStringFunc(s, func(ref string) string {
		if ref == escapedDollar {
			return "$"
		}

		m := envRef.FindStringSubmatch(ref)
		name, hasDefault, def := m[1], m[2] != "", m[3]

		if val, ok := e.lookup(name); ok && (val != "" || !hasDefault) {
			return val
		}

		if hasDefault {
			return def
		}

		e.errs = append(e.errs, &spec.Error{
			Path: path,
			Msg:  fmt.Sprintf("environment variable %s is not set (use ${%s:-default} for a fallback)", name, name),
		})

		return ref
	})
}

// yamlName returns the YAML key of a struct field; inline fields keep their
// parent's path.
func yamlName(f reflect.StructField) string {
	tag := f.Tag.Get("yaml")
	name, opts, _ := strings.Cut(tag, ",")

	if strings.Contains(opts, "inline") {
		return ""
	}

	if name == "" {
		return strings.ToLower(f.Name)
	}

	return name
}
