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

func TestExample(t *testing.T) {
	model := spec.Model{Instance: stiggytest.Reply(func(p string) string {
		return "Hello, " + strings.TrimSuffix(strings.TrimPrefix(p, "Greet "), ".") + "! Welcome aboard."
	})}
	if extest.Live() {
		model = newModel()
	}

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		return run(ctx, nc, model, out)
	}, func(out string) {
		if !strings.Contains(out, "completed") || !strings.Contains(out, "Hello, Ada!") {
			t.Fatalf("output:\n%s", out)
		}
	})
}
