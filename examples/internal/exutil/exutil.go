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

// Package exutil is what the examples share: the NATS connection, the
// model (a real one when an API key is set, a scripted one otherwise), and
// running an app for the length of an example.
package exutil

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"

	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/llm/anthropic"
	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/internal/natsconn"
)

// readyTimeout bounds how long an example waits for its app.
const readyTimeout = 30 * time.Second

// errNotReady is returned when an app does not become ready in time.
var errNotReady = errors.New("app not ready in time")

// Connect dials $NATS_URL (default nats://127.0.0.1:4222). NATS must run
// with JetStream: nats-server -js.
func Connect() (*nats.Conn, error) {
	url := os.Getenv("NATS_URL")
	if url == "" {
		url = natsconn.DefaultURL
	}

	return natsconn.Connect(natsconn.Config{URL: url, Name: "stiggy-example"})
}

// Model returns a real model when one is configured in the environment:
//   - OPENAI_API_KEY (OPENAI_MODEL, default gpt-4o-mini);
//   - ANTHROPIC_API_KEY (ANTHROPIC_MODEL);
//   - OLLAMA_MODEL (Ollama on localhost).
//
// Otherwise it returns scripted, so every example runs offline.
func Model(scripted llm.LLM) (llm.LLM, string) {
	switch {
	case os.Getenv("OPENAI_API_KEY") != "":
		m := envOr("OPENAI_MODEL", "gpt-4o-mini")

		return openai.New(os.Getenv("OPENAI_API_KEY"), openai.WithModel(m)), "openai " + m
	case os.Getenv("ANTHROPIC_API_KEY") != "":
		var opts []anthropic.Option
		if m := os.Getenv("ANTHROPIC_MODEL"); m != "" {
			opts = append(opts, anthropic.WithModel(m))
		}

		return anthropic.New(os.Getenv("ANTHROPIC_API_KEY"), opts...), "anthropic"
	case os.Getenv("OLLAMA_MODEL") != "":
		m := os.Getenv("OLLAMA_MODEL")

		return openai.New("ollama", openai.WithOllamaBaseURL(), openai.WithModel(m)), "ollama " + m
	default:
		return scripted, "scripted (set OPENAI_API_KEY, ANTHROPIC_API_KEY or OLLAMA_MODEL for a real model)"
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}

	return def
}

// Quiet is a logger that only reports warnings and errors, so examples
// print their own story.
func Quiet() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
}

// Serve runs app until the returned stop is called, and returns once it is
// ready.
func Serve(ctx context.Context, app *stiggy.App) (stop func() error, err error) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	stop = func() error {
		cancel()

		return <-done
	}

	select {
	case <-app.Ready():
		return stop, nil
	case err = <-done:
		cancel()

		return nil, fmt.Errorf("app stopped: %w", err)
	case <-time.After(readyTimeout):
		_ = stop()

		return nil, errNotReady
	}
}

// errNotCompleted is returned when an example's execution did not complete.
var errNotCompleted = errors.New("execution did not complete")

// RunFlow starts flow with input, waits for it, and prints its status.
func RunFlow(ctx context.Context, app *stiggy.App, flow string, input any, out io.Writer) (*packtrail.State, error) {
	id, err := app.Client().Start(ctx, flow, input)
	if err != nil {
		return nil, err
	}

	st, err := app.Client().Wait(ctx, id)
	if err != nil {
		return nil, err
	}

	fmt.Fprintf(out, "execution %s: %s (%v agent steps, %v tokens out)\n",
		id, st.Status, st.Counters["agent_steps"], st.Counters["tokens_out"])

	if st.Status != packtrail.StatusCompleted {
		return st, fmt.Errorf("%w: %s %s", errNotCompleted, st.Reason, st.Error)
	}

	return st, nil
}

// Text returns the answer of a plain agent output ({"text": ...}), or the
// raw JSON of any other output.
func Text(raw []byte) string {
	var o struct{ Text string }
	if json.Unmarshal(raw, &o) == nil && o.Text != "" {
		return o.Text
	}

	return string(raw)
}
