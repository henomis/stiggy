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

// Package stiggytest runs a whole fleet in a test: an embedded JetStream
// server (packtrail's packtrailtest), the stiggy app in RoleAll, and fake
// LLMs scripted in Go, so a flow's routing and data flow can be tested like
// ordinary code.
package stiggytest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
)

// Timeout bounds how long Start waits for readiness and Run for an
// execution to end.
const Timeout = 60 * time.Second

// Harness is a running fleet.
type Harness struct {
	App    *stiggy.App
	Client *packtrail.Client
	Server *packtrailtest.Server
	Conn   *nats.Conn
}

// Start runs the fleet the options describe and stops it when the test ends.
func Start(t testing.TB, opts ...stiggy.Option) *Harness {
	t.Helper()

	srv := packtrailtest.Start(t)
	nc := srv.Connect(t)

	app, err := stiggy.New(nc, opts...)
	if err != nil {
		t.Fatalf("stiggytest: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	t.Cleanup(func() {
		cancel()

		if rerr := <-done; rerr != nil {
			t.Errorf("stiggytest: run: %v", rerr)
		}
	})

	select {
	case <-app.Ready():
	case err = <-done:
		done <- err // let Cleanup see it too

		t.Fatalf("stiggytest: stopped before ready: %v", err)
	case <-time.After(Timeout):
		t.Fatal("stiggytest: not ready in time")
	}

	return &Harness{App: app, Client: app.Client(), Server: srv, Conn: nc}
}

// Run starts flow with input and waits for the execution to end.
func (h *Harness) Run(t testing.TB, flow string, input any) *packtrail.State {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), Timeout)
	defer cancel()

	id, err := h.Client.Start(ctx, flow, input)
	if err != nil {
		t.Fatalf("stiggytest: start %s: %v", flow, err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil {
		t.Fatalf("stiggytest: wait %s: %v", id, err)
	}

	return st
}

// ErrNoReply is returned by a [Replies] model that ran out of answers.
var ErrNoReply = errors.New("stiggytest: no scripted reply left")

// Reply returns a fake model that answers each call with fn applied to the
// last user message. It reports one token per byte, so budgets can be tested.
func Reply(fn func(prompt string) string) llm.LLM {
	return llm.Func(func(ctx context.Context, msgs []llm.Message, _ ...llm.CallOption) (*llm.Result, error) {
		// Like a real provider, fail once the run is cancelled.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		prompt := lastUser(msgs)
		out := fn(prompt)

		return &llm.Result{
			Message: new(llm.AssistantMessage([]llm.ContentPart{llm.Text(out)})),
			Usage:   &llm.Usage{InputTokens: len(prompt), OutputTokens: len(out)},
		}, nil
	})
}

// Replies returns a fake model that answers successive calls with texts, in
// order, and fails once they run out.
func Replies(texts ...string) llm.LLM {
	var (
		mu sync.Mutex
		i  int
	)

	next := func() (string, bool) {
		mu.Lock()
		defer mu.Unlock()

		if i >= len(texts) {
			return "", false
		}

		i++

		return texts[i-1], true
	}

	return llm.Func(func(ctx context.Context, msgs []llm.Message, opts ...llm.CallOption) (*llm.Result, error) {
		text, ok := next()
		if !ok {
			return nil, fmt.Errorf("%w (%d used)", ErrNoReply, len(texts))
		}

		return Reply(func(string) string { return text }).Execute(ctx, msgs, opts...)
	})
}

// Request is what a [Model] sees on one call.
type Request struct {
	// System is the system prompt.
	System string
	// Prompt is the last user message: the step prompt (earlier user
	// messages come from memory).
	Prompt string
	// ToolResults are the results of the tools called so far in this run,
	// in order.
	ToolResults []string
	// Tools names the tools the agent may call.
	Tools []string
	// Schema reports whether the answer must follow a JSON schema.
	Schema bool
	// Messages is the whole conversation.
	Messages []llm.Message
}

// Call is a tool call a [Model] makes.
type Call struct {
	Tool string
	Args any
}

// Response is a [Model]'s answer: tool calls, or the final text.
type Response struct {
	Text  string
	Calls []Call
}

// Model returns a fake model driven by fn, which can call tools: return
// Calls to call them, then Text once ToolResults shows they ran.
func Model(fn func(Request) Response) llm.LLM {
	var seq atomic.Int64

	return llm.Func(func(ctx context.Context, msgs []llm.Message, opts ...llm.CallOption) (*llm.Result, error) {
		// Like a real provider, fail once the run is cancelled.
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		cfg := llm.NewCallConfig(opts...)
		req := Request{Messages: msgs, Schema: cfg.ResponseFormat != nil}

		for _, t := range cfg.Tools {
			req.Tools = append(req.Tools, t.Name())
		}

		for _, m := range msgs {
			switch m.Role {
			case llm.RoleSystem:
				req.System = m.TextContent()
			case llm.RoleUser:
				req.Prompt = strings.TrimSpace(m.TextContent())
			case llm.RoleTool:
				req.ToolResults = append(req.ToolResults, m.TextContent())
			}
		}

		resp := fn(req)

		var calls []llm.ToolCall

		for _, c := range resp.Calls {
			args, err := json.Marshal(c.Args)
			if err != nil {
				return nil, fmt.Errorf("stiggytest: tool args: %w", err)
			}

			calls = append(calls, llm.ToolCall{
				ID: fmt.Sprintf("call-%d", seq.Add(1)), Type: llm.ToolTypeFunction,
				Function: llm.FunctionCall{Name: c.Tool, Arguments: string(args)},
			})
		}

		var parts []llm.ContentPart
		if resp.Text != "" {
			parts = append(parts, llm.Text(resp.Text))
		}

		return &llm.Result{
			Message: new(llm.AssistantMessage(parts, calls...)),
			Usage:   &llm.Usage{InputTokens: len(req.Prompt), OutputTokens: len(resp.Text)},
		}, nil
	})
}

// Echo returns a fake model that answers with prefix followed by the prompt.
func Echo(prefix string) llm.LLM {
	return Reply(func(p string) string { return prefix + p })
}

func lastUser(msgs []llm.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llm.RoleUser {
			return strings.TrimSpace(msgs[i].TextContent())
		}
	}

	return ""
}
