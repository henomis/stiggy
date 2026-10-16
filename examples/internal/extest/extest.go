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

// Package extest runs an example's program against the NATS server at
// $NATS_URL (default nats://127.0.0.1:4222), which must run with JetStream:
// nats-server -js. Without a reachable server the example is skipped.
//
// By default the examples use the scripted models their tests supply, so
// the output is checked exactly. With STIGGY_EXAMPLE_LIVE=1 they use their
// real models (Ollama serving gemma4:cloud): the output is logged, and
// only success is required, since a real model's words vary.
package extest

import (
	"bytes"
	"context"
	"io"
	"os"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"
)

// timeout bounds one example run (generous for real models).
const timeout = 5 * time.Minute

// Live reports whether examples run against a real model.
func Live() bool { return os.Getenv("STIGGY_EXAMPLE_LIVE") == "1" }

// Connect dials $NATS_URL for the test, or skips it when no server answers.
func Connect(t *testing.T) *nats.Conn {
	t.Helper()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-test"))
	if err != nil {
		t.Skipf("no NATS at %s (start nats-server -js): %v", url, err)
	}

	t.Cleanup(nc.Close)

	return nc
}

// Run calls run with a connection to $NATS_URL, then check with what it
// printed (only in scripted mode; live output is logged instead).
func Run(t *testing.T, run func(ctx context.Context, nc *nats.Conn, out io.Writer) error, check func(out string)) {
	t.Helper()

	nc := Connect(t)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var out bytes.Buffer
	if err := run(ctx, nc, &out); err != nil {
		t.Fatalf("%v\noutput so far:\n%s", err, out.String())
	}

	if Live() {
		t.Logf("live output:\n%s", out.String())

		return
	}

	check(out.String())
}

// Agent serves a plain phero agent on the NATS Agent Protocol as
// owner/name until the test ends: a stand-in for another program on the
// bus that an example reaches as a remote.
func Agent(t *testing.T, nc *nats.Conn, owner, name, description string, model llm.LLM) {
	t.Helper()

	a, err := agent.New(model, name, description)
	if err != nil {
		t.Fatalf("agent %s/%s: %v", owner, name, err)
	}

	srv, err := natsagent.New(nc, a, owner, name)
	if err != nil {
		t.Fatalf("agent %s/%s: %v", owner, name, err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- srv.Start(ctx) }()

	t.Cleanup(func() {
		cancel()
		<-done
	})

	select {
	case <-srv.Ready():
	case err = <-done:
		t.Fatalf("agent %s/%s: %v", owner, name, err)
	case <-time.After(timeout):
		t.Fatalf("agent %s/%s: not ready in time", owner, name)
	}
}
