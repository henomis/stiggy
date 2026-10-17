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

package stiggy

import (
	"context"

	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/internal/jobctx"
)

// Job is the flow step an agent runs for: its execution, flow, node, agent,
// step id and attempt, the flow input and map item, and packtrail's
// long-term store. A Go tool reads it with
// [JobFromContext] to know who calls it, instead of trusting an argument the
// model passes: a persona's "post" tool posts as that persona.
type Job = jobctx.Job

// ErrNoJobValue is returned by [Job.DecodeInput] and [Job.DecodeItem] for an
// absent value.
var ErrNoJobValue = jobctx.ErrNoValue

// JobFromContext returns the job of a tool call's context. It reports false
// outside a flow step, as for an exposed agent answering a prompt.
func JobFromContext(ctx context.Context) (Job, bool) { return jobctx.From(ctx) }

// StepSummary is what an agent step did: counts, tokens, cost and timings,
// never content. It is recorded with each attempt of the step and shown by
// `stiggy timeline` (packtrail's TimelineStep.Summary holds it as JSON).
type StepSummary = agentrun.StepSummary
