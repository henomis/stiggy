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
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/henomis/stiggy/examples/internal/extest"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// fake asks for the destination, then plans, then shortens the plan when
// the reviewer asks for it.
func fake(r stiggytest.Request) stiggytest.Response {
	switch {
	case strings.Contains(r.Prompt, "shorter"):
		return stiggytest.Response{Text: "Rome: Colosseum Saturday, Trastevere Sunday."}
	case strings.Contains(r.Prompt, "The human answered:"):
		return stiggytest.Response{Text: "Rome: Saturday Colosseum and Forum, Sunday Vatican and Trastevere, with long lunches."}
	default:
		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "ask_human", Args: map[string]any{"question": "Where to?"}}}}
	}
}

func TestExample(t *testing.T) {
	model := spec.Model{Instance: stiggytest.Model(fake)}
	if extest.Live() {
		model = newModel()
	}

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		return run(ctx, nc, model, out)
	}, func(out string) {
		for _, want := range []string{
			"paused for a question: Where to?", "human: Rome", "paused for a review",
			"final plan: Rome: Colosseum Saturday",
		} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q in:\n%s", want, out)
			}
		}
	})
}
