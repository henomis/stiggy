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

// fake stands in for a real model: triage routes on keywords, the
// specialists answer in their own voice.
func fake(r stiggytest.Request) stiggytest.Response {
	switch {
	case strings.Contains(r.System, "Triage"):
		if len(r.ToolResults) > 0 {
			return stiggytest.Response{Text: "routed"}
		}

		ticket, _, _ := strings.Cut(r.Prompt, "\n")

		target := "route_to_tech"
		if strings.Contains(strings.ToLower(ticket), "invoice") {
			target = "route_to_billing"
		}

		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: target, Args: map[string]any{"reason": "keyword"}}}}
	case strings.Contains(r.System, "Billing"):
		return stiggytest.Response{Text: "Billing here: your invoice has been corrected."}
	default:
		return stiggytest.Response{Text: "Tech here: try restarting the service."}
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
		if !strings.Contains(out, "→ billing") || !strings.Contains(out, "→ tech") {
			t.Fatalf("output:\n%s", out)
		}
	})
}
