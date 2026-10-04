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

// Package extest runs an example against an embedded NATS server, so the
// examples are tested like the rest of the code.
//
// By default the model environment is cleared and the examples use their
// scripted models, so the output is checked exactly. With
// STIGGY_EXAMPLE_LIVE=1 the environment is kept (OLLAMA_MODEL=gemma4:cloud,
// OPENAI_API_KEY, ...): the examples run against a real model, the output is
// logged, and only success is required, since a real model's words vary.
package extest

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail/packtrailtest"
)

// timeout bounds one example run (generous for real models).
const timeout = 5 * time.Minute

// Live reports whether examples run against a real model.
func Live() bool { return os.Getenv("STIGGY_EXAMPLE_LIVE") == "1" }

// Run calls run with an embedded server, then check with what it printed
// (only in scripted mode; live output is logged instead).
func Run(t *testing.T, run func(ctx context.Context, nc *nats.Conn, out io.Writer) error, check func(out string)) {
	t.Helper()

	if !Live() {
		for _, k := range []string{"OPENAI_API_KEY", "ANTHROPIC_API_KEY", "OLLAMA_MODEL"} {
			t.Setenv(k, "")
		}
	}

	srv := packtrailtest.Start(t)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var out bytes.Buffer
	if err := run(ctx, srv.Connect(t), &out); err != nil {
		t.Fatalf("%v\noutput so far:\n%s", err, out.String())
	}

	if Live() {
		t.Logf("live output:\n%s", out.String())

		return
	}

	check(out.String())
}
