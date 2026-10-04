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
	"errors"
	"fmt"
	"strconv"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail/worker"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy/internal/stepio"
	"github.com/henomis/stiggy/spec"
)

// Headers stiggy sends with every prompt to a remote agent. They identify the
// step so the agent and its tools can deduplicate retried prompts; like any
// header, they are a claim, never a basis for authorization.
const (
	HeaderExec        = "Stiggy-Exec"
	HeaderFlow        = "Stiggy-Flow"
	HeaderNode        = "Stiggy-Node"
	HeaderAttempt     = "Stiggy-Attempt"
	HeaderTraceparent = "traceparent"
)

// ErrRemoteMismatch is returned when a job's step names another remote than
// the one the worker serves.
var ErrRemoteMismatch = errors.New("agentrun: the step names another remote")

// Resolver finds remote agents; *natsagent.Resolver is one.
type Resolver interface {
	Resolve(ctx context.Context, owner, name string) (*natsagent.AgentHandle, error)
	Invalidate(owner, name string)
}

// Remote is an agent served by someone else, prompted over the NATS Agent
// Protocol.
type Remote struct {
	Name     string
	Spec     spec.Remote
	Resolver Resolver
}

// RemoteHandler returns the worker handler of a remote agent's steps.
func RemoteHandler(rm Remote, opts ...Option) worker.Handler {
	r := &runner{}
	for _, opt := range opts {
		opt(r)
	}

	return func(ctx context.Context, job *worker.Job) (*worker.Result, error) {
		return rm.handle(ctx, job, r.flows)
	}
}

func (rm Remote) handle(ctx context.Context, job *worker.Job, flows *flowCache) (*worker.Result, error) {
	step, err := stepio.Step(job)
	if err != nil {
		return nil, err
	}

	if step.Remote != rm.Name {
		return nil, worker.Permanent(fmt.Errorf("%w: %q, worker serves %q", ErrRemoteMismatch, step.Remote, rm.Name))
	}

	schema, err := flows.schema(ctx, job)
	if err != nil {
		return nil, err
	}

	prompt, err := Prompt(job, step)
	if err != nil {
		return nil, worker.Permanent(err)
	}

	// A remote agent gets no response format over the protocol: the schema
	// travels in the prompt, and the answer is parsed like any other.
	if schema != nil {
		instr, serr := schemaInstruction(schema)
		if serr != nil {
			return nil, serr
		}

		prompt += instr
	}

	usage := map[string]float64{CounterAgentSteps: 1}

	text, err := rm.prompt(ctx, job, prompt)
	if err != nil {
		return nil, worker.WithUsage(err, usage)
	}

	output, err := parseOutput(text, schema)
	if err != nil {
		return nil, worker.WithUsage(err, usage)
	}

	writes, err := stepio.Writes(step, output)
	if err != nil {
		return nil, worker.WithUsage(err, usage)
	}

	return &worker.Result{Output: output, Writes: writes, Usage: usage}, nil
}

// prompt sends one prompt and returns the final answer. Errors the protocol
// marks permanent (4xx but 429) fail the step; others are retried by the
// node's policy, after dropping the cached instance.
func (rm Remote) prompt(ctx context.Context, job *worker.Job, prompt string) (string, error) {
	h, err := rm.Resolver.Resolve(ctx, rm.Spec.Owner, rm.Spec.Name)
	if err != nil {
		return "", fmt.Errorf("agentrun: resolve %s/%s: %w", rm.Spec.Owner, rm.Spec.Name, err)
	}

	stream, err := h.Send(ctx, &natsagent.Request{Prompt: prompt, Header: headers(job)})
	if err == nil {
		defer stream.Close()

		var text string

		if text, err = stream.Text(ctx); err == nil {
			return text, nil
		}
	}

	if natsagent.Permanent(err) {
		return "", worker.Permanent(fmt.Errorf("agentrun: remote %s: %w", rm.Name, err))
	}

	rm.Resolver.Invalidate(rm.Spec.Owner, rm.Spec.Name)

	return "", fmt.Errorf("agentrun: remote %s: %w", rm.Name, err)
}

func headers(job *worker.Job) nats.Header {
	h := nats.Header{}
	h.Set(HeaderExec, job.ExecID)
	h.Set(HeaderFlow, job.Flow)
	h.Set(HeaderNode, job.Node)
	h.Set(HeaderAttempt, strconv.Itoa(job.Attempt))

	if job.Traceparent != "" {
		h.Set(HeaderTraceparent, job.Traceparent)
	}

	return h
}
