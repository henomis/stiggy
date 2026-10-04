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

package agentrun

import (
	"fmt"
	"strings"

	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/spec"
)

// emptyPrompt is the prompt of a step with no template and no context.
const emptyPrompt = "Begin."

// Prompt renders the step prompt over the job context. Without a template,
// the prompt is the map item (or the flow input) followed by the outputs of
// the step's context nodes (by default, the previous node). The step's
// expected output is appended either way.
func Prompt(job *worker.Job, s spec.Step) (string, error) {
	data, err := stepio.Data(job)
	if err != nil {
		return "", err
	}

	prompt := defaultPrompt(data, s.Context)

	if s.Prompt != "" {
		t, perr := spec.ParsePrompt(job.Node, s.Prompt)
		if perr != nil {
			return "", perr
		}

		var b strings.Builder
		if err = t.Execute(&b, data); err != nil {
			return "", fmt.Errorf("prompt: %w", err)
		}

		prompt = b.String()

		// A template that asks for context nodes still gets them.
		if len(s.Context) > 0 {
			prompt += "\n\n" + contextSections(data, s.Context)
		}
	}

	if s.ExpectedOutput != "" {
		prompt += "\n\nExpected output:\n" + s.ExpectedOutput
	}

	return prompt, nil
}

func defaultPrompt(data map[string]any, context []string) string {
	var sections []string

	switch {
	case data["item"] != nil:
		sections = append(sections, "Item:\n"+stepio.Render(data["item"]))
	case data["input"] != nil:
		sections = append(sections, "Input:\n"+stepio.Render(data["input"]))
	}

	if len(context) == 0 {
		if last, _ := data["last_node"].(string); last != "" {
			context = []string{last}
		}
	}

	if s := contextSections(data, context); s != "" {
		sections = append(sections, s)
	}

	if len(sections) == 0 {
		return emptyPrompt
	}

	return strings.Join(sections, "\n\n")
}

// contextSections renders the results of the given nodes, skipping those
// that have none yet.
func contextSections(data map[string]any, nodes []string) string {
	results, _ := data["results"].(map[string]any)

	var sections []string

	for _, n := range nodes {
		if results[n] != nil {
			sections = append(sections, fmt.Sprintf("Output of step %q:\n%s", n, stepio.Render(unwrapText(results[n]))))
		}
	}

	return strings.Join(sections, "\n\n")
}

// unwrapText returns the answer of a plain-text agent output ({"text": ...})
// and any other value unchanged.
func unwrapText(v any) any {
	if m, ok := v.(map[string]any); ok && len(m) == 1 {
		if t, isText := m[spec.OutputText].(string); isText {
			return t
		}
	}

	return v
}
