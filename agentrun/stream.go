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
	"strings"
	"time"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
)

// Text deltas are coalesced into one progress event per interval or size,
// whichever comes first.
const (
	progressInterval = 200 * time.Millisecond
	progressMaxText  = 2048
)

// Progress event types.
const (
	ProgressText       = "text"
	ProgressToolCall   = "tool_call"
	ProgressToolResult = "tool_result"
)

// errNoResult is returned when a stream ends without its final event.
var errNoResult = errors.New("agentrun: the agent stream ended without a result")

// Progress is one streamed event of an agent step, published with
// Job.Progress and read with packtrail's Client.Progress. Progress is not
// stored: subscribe before it happens.
type Progress struct {
	Type    string `json:"type"`
	Agent   string `json:"agent"`
	Node    string `json:"node"`
	Text    string `json:"text,omitempty"`
	Tool    string `json:"tool,omitempty"`
	Args    string `json:"args,omitempty"`
	IsError bool   `json:"is_error,omitempty"`
}

type progress struct {
	job   *worker.Job
	agent string
	buf   strings.Builder
	last  time.Time
}

func (p *progress) send(ev Progress) {
	ev.Agent, ev.Node = p.agent, p.job.Node

	// Progress is best effort: it is not stored, and a job without a
	// progress channel (or with no subscriber) must still run.
	_ = p.job.Progress(ev)
}

func (p *progress) text(delta string) {
	p.buf.WriteString(delta)

	if time.Since(p.last) >= progressInterval || p.buf.Len() >= progressMaxText {
		p.flush()
	}
}

func (p *progress) flush() {
	if p.buf.Len() > 0 {
		p.send(Progress{Type: ProgressText, Text: p.buf.String()})
		p.buf.Reset()
	}

	p.last = time.Now()
}

// runStreaming runs the agent with RunStream, publishing text deltas and
// tool calls as progress.
func runStreaming(ctx context.Context, job *worker.Job, name string, ag *agent.Agent, prompt string,
) (*agent.Result, error) {
	p := &progress{job: job, agent: name, last: time.Now()}
	defer p.flush()

	for ev, err := range ag.RunStream(ctx, llm.Text(prompt)) {
		if err != nil {
			return nil, err
		}

		switch ev.Type {
		case agent.EventTextDelta:
			p.text(ev.TextDelta)
		case agent.EventToolCall:
			p.flush()
			p.send(Progress{Type: ProgressToolCall, Tool: ev.ToolName, Args: ev.ToolArgs})
		case agent.EventToolResult:
			p.send(Progress{Type: ProgressToolResult, Tool: ev.ToolName, IsError: ev.ToolError})
		case agent.EventDone:
			return ev.Result, nil
		case agent.EventReasoningDelta:
		}
	}

	return nil, errNoResult
}
