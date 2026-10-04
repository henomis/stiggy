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
	"regexp"
	"strings"
	"text/template"
)

// ErrWritePath is returned by [ParseWritePath] for a malformed path.
var ErrWritePath = errors.New(`spec: write path must be "output" or "output.<field>[.<field>...]"`)

// ParsePrompt compiles a step prompt. The template is rendered over the job
// context: .input, .channels, .results, .item, .index, .last_node, .signals,
// .errors, .visits and .resume. Besides the text/template builtins it has
// `json`, which encodes a value as compact JSON.
func ParsePrompt(name, text string) (*template.Template, error) {
	t, err := template.New(name).Funcs(promptFuncs).Option("missingkey=zero").Parse(text)
	if err != nil {
		return nil, fmt.Errorf("prompt: %w", err)
	}

	return t, nil
}

var promptFuncs = template.FuncMap{
	"json": func(v any) (string, error) {
		b, err := json.Marshal(v)
		if err != nil {
			return "", err
		}

		return string(b), nil
	},
}

var writePathPattern = regexp.MustCompile(`^output(\.[A-Za-z0-9_-]+)*$`)

// ParseWritePath checks a [Step.Writes] value and returns the field path
// below the output (empty for the whole output).
func ParseWritePath(p string) ([]string, error) {
	if !writePathPattern.MatchString(p) {
		return nil, fmt.Errorf("%w: got %q", ErrWritePath, p)
	}

	if p == "output" {
		return nil, nil
	}

	return strings.Split(p, ".")[1:], nil
}
