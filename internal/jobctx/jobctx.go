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

// Package jobctx carries the flow step an agent runs for in its context, so
// tools can tell which execution, node and map item call them. The stiggy
// package re-exports it (stiggy.Job, stiggy.JobFromContext).
package jobctx

import (
	"context"
	"encoding/json"
	"errors"
)

// ErrNoValue is returned when decoding a part of the job that is absent.
var ErrNoValue = errors.New("stiggy: job value not present")

// Job is the flow step an agent runs for.
type Job struct {
	// ExecID, Flow and Node locate the step.
	ExecID string
	Flow   string
	Node   string
	// Agent is the step's agent. A delegate's tools see the job of the
	// step that delegates to it, with that step's agent.
	Agent string
	// StepID names this run of the step within the execution: retries and
	// redeliveries keep it, while the next visit of the node (a loop, a new
	// round) and a resume after an interrupt get a new one. Map items get
	// one each. It is the idempotency key for a tool with side effects:
	// delivery is at least once, and a retried step calls its tools again.
	StepID string
	// Attempt is the step's attempt, from 1.
	Attempt int
	// Index is the map item's index; nil outside a map node.
	Index *int
	// Input is the flow input and Item the map item (nil outside a map), as
	// JSON.
	Input json.RawMessage
	Item  json.RawMessage
}

// DecodeInput decodes the flow input into v.
func (j Job) DecodeInput(v any) error { return decode(j.Input, v) }

// DecodeItem decodes the map item into v.
func (j Job) DecodeItem(v any) error { return decode(j.Item, v) }

func decode(raw json.RawMessage, v any) error {
	if len(raw) == 0 {
		return ErrNoValue
	}

	return json.Unmarshal(raw, v)
}

type key struct{}

// With returns ctx carrying j.
func With(ctx context.Context, j Job) context.Context { return context.WithValue(ctx, key{}, j) }

// From returns the job ctx carries, if any.
func From(ctx context.Context) (Job, bool) {
	j, ok := ctx.Value(key{}).(Job)

	return j, ok
}
