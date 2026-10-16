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

// Command remote uses an agent another team serves on the NATS Agent
// Protocol. The "legal" team runs a plain phero agent with phero's NATS
// server; the stiggy fleet only names it under remotes:, then prompts it as
// a flow step (remote:) and lets its own writer delegate to it as a tool.
// The prompts carry Stiggy-Exec/-Node/-Attempt headers, which the remote
// agent reads to identify the step.
//
// It needs NATS with JetStream ($NATS_URL, default nats://127.0.0.1:4222)
// and Ollama serving gemma4:cloud:
//
//	nats-server -js &
//	go run ./examples/remote
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/llm/openai"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

const fleet = `
namespace: example-remote
remotes:
  legal: {owner: legal-team, name: reviewer, description: Reviews text for legal risks.}
agents:
  writer: {model: main, role: Writer, goal: Write short announcements., delegates: [legal]}
flows:
  announce:
    nodes:
      - id: draft
        type: task
        agent: writer
        prompt: "Write a two-sentence announcement of {{.input.product}}. Ask legal if you are unsure about a claim."
        next: review
      - id: review
        type: task
        remote: legal
        prompt: "Review this announcement for legal risks, in one sentence: {{.results.draft.text}}"
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-remote"))
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	if err = run(ctx, nc, newModel(), os.Stdout); err != nil {
		log.Fatal(err)
	}
}

// newModel is gemma4:cloud through a local Ollama. Cloud models answer 429
// to concurrent requests, so calls go one at a time and failed ones are
// retried.
func newModel() spec.Model {
	return spec.Model{
		Instance:  openai.New("ollama", openai.WithOllamaBaseURL(), openai.WithModel("gemma4:cloud")),
		Retries:   5,
		RateLimit: &spec.RateLimit{MaxConcurrent: 1},
	}
}

// legalTeam serves the reviewer as another team would: a phero agent behind
// phero's NATS server, knowing nothing about stiggy.
func legalTeam(ctx context.Context, nc *nats.Conn, model llm.LLM, out io.Writer) (*natsagent.Server, error) {
	reviewer, err := agent.New(model, "reviewer", "Role: Legal reviewer\n\nSpot legal risks in short texts.")
	if err != nil {
		return nil, err
	}

	h := handler(func(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
		if req, ok := natsagent.RequestFrom(ctx); ok && req.Header.Get(agentrun.HeaderNode) != "" {
			fmt.Fprintf(out, "legal team: prompt from step %q of execution %s\n",
				req.Header.Get(agentrun.HeaderNode), req.Header.Get(agentrun.HeaderExec))
		}

		return reviewer.Run(ctx, parts...)
	})

	srv, err := natsagent.New(nc, h, "legal-team", "reviewer")
	if err != nil {
		return nil, err
	}

	go func() { _ = srv.Start(ctx) }()

	select {
	case <-srv.Ready():
		return srv, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

type handler func(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error)

func (h handler) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	return h(ctx, parts...)
}

func run(ctx context.Context, nc *nats.Conn, model spec.Model, out io.Writer) error {
	teamCtx, stopTeam := context.WithCancel(ctx)
	defer stopTeam()

	if _, err := legalTeam(teamCtx, nc, model.Instance, out); err != nil {
		return err
	}

	f, src, err := config.Parse([]byte(fleet))
	if err != nil {
		return fmt.Errorf("%s", strings.Join(src.Describe(err), "\n"))
	}

	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModelSpec("main", model), stiggy.WithLogger(quiet))
	if err != nil {
		return err
	}

	// Run serves the fleet until ctx ends; Ready says when it can take work.
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	select {
	case <-app.Ready():
	case err = <-done:
		cancel()

		return err
	}

	defer func() {
		cancel()
		<-done
	}()

	id, err := app.Client().Start(ctx, "announce", map[string]any{"product": "stiggy"})
	if err != nil {
		return err
	}

	st, err := app.Client().Wait(ctx, id)
	if err != nil {
		return err
	}

	if st.Status != packtrail.StatusCompleted {
		return fmt.Errorf("execution %s: %s %s %s", id, st.Status, st.Reason, st.Error)
	}

	var draft, review struct{ Text string }
	if err = st.Result("draft", &draft); err != nil {
		return err
	}

	if err = st.DecodeOutput(&review); err != nil {
		return err
	}

	fmt.Fprintf(out, "draft: %s\nlegal review: %s\n", draft.Text, review.Text)

	return nil
}
