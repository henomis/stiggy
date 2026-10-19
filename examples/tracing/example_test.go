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

package main

import (
	"context"
	"io"
	"slices"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/henomis/stiggy/examples/internal/extest"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

const (
	draft   = "NATS is a fast messaging system, and JetStream adds persistence so messages survive restarts."
	revised = "NATS moves messages; JetStream keeps them."
)

// newsroom scripts the four agents, told apart by their role: the
// researcher asks the fact checker, the editor sends the first draft back.
func newsroom() spec.Model {
	var edits atomic.Int64

	return spec.Model{Instance: stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch {
		case strings.Contains(r.System, "Role: Researcher"):
			if len(r.ToolResults) == 0 {
				return stiggytest.Response{Calls: []stiggytest.Call{
					{Tool: "ask_checker", Args: map[string]any{"input": "JetStream is the persistence layer of NATS."}},
				}}
			}

			return stiggytest.Response{Text: "NATS is a messaging system; JetStream persists streams (checked)."}
		case strings.Contains(r.System, "Role: Fact checker"):
			return stiggytest.Response{Text: "Accurate."}
		case strings.Contains(r.System, "Role: Writer"):
			if strings.Contains(r.Prompt, "shorter") {
				return stiggytest.Response{Text: revised}
			}

			return stiggytest.Response{Text: draft}
		case strings.Contains(r.System, "Role: Editor"):
			if len(r.ToolResults) == 0 && edits.Add(1) == 1 {
				return stiggytest.Response{Calls: []stiggytest.Call{
					{Tool: "route_to_write", Args: map[string]any{"reason": "too long"}},
				}}
			}

			if len(r.ToolResults) > 0 {
				return stiggytest.Response{Text: "Make it one sentence, shorter."}
			}

			return stiggytest.Response{Text: "Approved."}
		}

		return stiggytest.Response{Text: "unexpected agent"}
	})}
}

// kept is an in-memory exporter that keeps its spans when the program shuts
// its provider down (the plain one clears them).
type kept struct{ *tracetest.InMemoryExporter }

func (kept) Shutdown(context.Context) error { return nil }

func TestExample(t *testing.T) {
	model := newsroom()
	if extest.Live() {
		model = newModel()
	}

	spans := kept{tracetest.NewInMemoryExporter()}

	var printed string

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		return run(ctx, nc, model, spans, out)
	}, func(out string) {
		printed = out

		// research and its checker, write, review (route + handoff note),
		// write again, review: 6 runs, 8 model calls. The scripted model
		// counts a token per byte.
		lines := strings.Split(strings.TrimSpace(out), "\n")
		if len(lines) != 4 || !strings.HasSuffix(lines[0], ": completed") || lines[1] != revised ||
			lines[2] != "6 agent runs, 8 model calls, 2 tool calls, 738 tokens in, 248 tokens out" ||
			len(lines[3]) != len("trace ")+32 {
			t.Fatalf("output:\n%s", out)
		}
	})

	if extest.Live() {
		return
	}

	// One trace, with the root span the program started.
	got := spans.GetSpans()
	if len(got) == 0 {
		t.Fatal("no spans exported")
	}

	var names []string

	for _, s := range got {
		names = append(names, s.Name)

		if s.SpanContext.TraceID() != got[0].SpanContext.TraceID() {
			t.Fatalf("span %s is in another trace", s.Name)
		}
	}

	if tid := got[0].SpanContext.TraceID().String(); !strings.Contains(printed, "trace "+tid) {
		t.Fatalf("printed %q, exported trace %s", printed, tid)
	}

	for _, want := range []string{
		"article", "stiggy.step research", "stiggy.step write", "stiggy.step review",
		"invoke_agent researcher", "execute_tool ask_checker", "invoke_agent checker", "invoke_agent editor",
	} {
		if !slices.Contains(names, want) {
			t.Errorf("no span %q in %v", want, names)
		}
	}
}
