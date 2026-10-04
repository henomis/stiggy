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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/spec"
)

// maxFormatName bounds response format names, which providers limit.
const maxFormatName = 64

// ErrNotJSONObject is returned when an answer that must follow an output
// schema is not a JSON object. It is retryable: the model may comply on the
// next attempt.
var ErrNotJSONObject = errors.New("agentrun: the answer is not a JSON object")

// flowCache keeps the flow versions jobs belong to; a version never changes.
type flowCache struct {
	src FlowSource

	mu    sync.Mutex
	flows map[string]*flow.Flow
}

func newFlowCache(src FlowSource) *flowCache {
	return &flowCache{src: src, flows: map[string]*flow.Flow{}}
}

// schema returns the output schema of the job's node, nil when it has none
// or the cache has no source.
func (c *flowCache) schema(ctx context.Context, job *worker.Job) (map[string]any, error) {
	if c == nil || c.src == nil {
		return nil, nil //nolint:nilnil // no schema is a valid answer.
	}

	key := job.Flow + "@" + job.FlowHash

	c.mu.Lock()
	f, ok := c.flows[key]
	c.mu.Unlock()

	if !ok {
		var err error

		if f, err = c.src.Flow(ctx, job.Flow, job.FlowHash); err != nil {
			return nil, fmt.Errorf("agentrun: flow %s: %w", key, err)
		}

		c.mu.Lock()
		c.flows[key] = f
		c.mu.Unlock()
	}

	n := f.Node(job.Node)
	if n == nil || n.OutputSchema == nil {
		return nil, nil //nolint:nilnil // no schema is a valid answer.
	}

	b, err := json.Marshal(n.OutputSchema)
	if err != nil {
		return nil, worker.Permanent(fmt.Errorf("agentrun: output_schema: %w", err))
	}

	var m map[string]any
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, worker.Permanent(fmt.Errorf("agentrun: output_schema must be an object: %w", err))
	}

	return m, nil
}

// schemaInstruction asks for an answer that follows schema. It is added to
// the prompt even when the provider gets the schema as a response format:
// some providers and models ignore response formats (Ollama cloud models,
// for one), and the instruction makes them comply anyway.
func schemaInstruction(schema map[string]any) (string, error) {
	b, err := json.Marshal(schema)
	if err != nil {
		return "", worker.Permanent(fmt.Errorf("agentrun: output_schema: %w", err))
	}

	return "\n\nAnswer with only a JSON object that follows this JSON Schema:\n" + string(b), nil
}

func responseFormat(node string, schema map[string]any) (*llm.ResponseFormat, error) {
	name := node
	if len(name) > maxFormatName {
		name = name[:maxFormatName]
	}

	f, err := llm.NewRawResponseFormat(name, "The output of workflow step "+node, schema)
	if err != nil {
		return nil, fmt.Errorf("output_schema: %w", err)
	}

	return f, nil
}

// parseOutput turns an answer into the step output: {"text": answer}
// without a schema, the answer's JSON object with one. packtrail then
// validates the object against the schema.
func parseOutput(text string, schema map[string]any) (any, error) {
	if schema == nil {
		return map[string]any{spec.OutputText: text}, nil
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(stripFence(text)), &out); err != nil || out == nil {
		return nil, fmt.Errorf("%w: %.200q", ErrNotJSONObject, text)
	}

	return out, nil
}

// stripFence removes a Markdown code fence around a JSON answer, which some
// models add even in structured-output mode.
func stripFence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}

	s = strings.TrimPrefix(s, "```")
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}

	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
