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

// Command router is agent-chosen routing, built
// with the Go API: a triage agent reads a support ticket and routes it to the
// billing or the tech agent, through the route_to_<node> tools stiggy gives
// it. The routes are durable: the choice is in the event log.
//
// It needs NATS with JetStream ($NATS_URL, default nats://127.0.0.1:4222)
// and Ollama serving gemma4:cloud:
//
//	nats-server -js &
//	go run ./examples/router
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/spec"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-router"))
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

func run(ctx context.Context, nc *nats.Conn, model spec.Model, out io.Writer) error {
	triage, err := stiggy.AgentTask("triage", "triage", spec.Step{
		Prompt: "Ticket: {{.input.ticket}}\n\nRoute it to billing (invoices, payments) or tech (bugs, outages), " +
			"then say in one line why.",
		Routes: []string{"billing", "tech"},
	})
	if err != nil {
		return err
	}

	billing, err := stiggy.AgentTask("billing", "billing", spec.Step{Prompt: "Answer this ticket: {{.input.ticket}}"})
	if err != nil {
		return err
	}

	tech, err := stiggy.AgentTask("tech", "tech", spec.Step{Prompt: "Answer this ticket: {{.input.ticket}}"})
	if err != nil {
		return err
	}

	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, err := stiggy.New(nc,
		stiggy.WithNamespace("example-router"),
		stiggy.WithLogger(quiet),
		stiggy.WithModelSpec("main", model),
		stiggy.WithAgent("triage", spec.Agent{Model: "main", Role: "Triage", Goal: "Send each ticket to the right team."}),
		stiggy.WithAgent("billing", spec.Agent{Model: "main", Role: "Billing support", Goal: "Solve invoice issues."}),
		stiggy.WithAgent("tech", spec.Agent{Model: "main", Role: "Tech support", Goal: "Solve technical issues."}),
		// No static next: the triage agent's route decides, and a route-less
		// answer ends the flow.
		stiggy.WithFlow(&flow.Flow{Name: "support", Start: "triage", Nodes: []flow.Node{triage, billing, tech}}),
	)
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

	for _, ticket := range []string{"My invoice charges me twice.", "The dashboard shows a 500 error."} {
		id, err := app.Client().Start(ctx, "support", map[string]any{"ticket": ticket})
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

		// The last node is the specialist that answered.
		var answer struct{ Text string }
		if err = st.DecodeOutput(&answer); err != nil {
			return err
		}

		fmt.Fprintf(out, "ticket %q → %s\n%s\n\n", ticket, st.LastNode, answer.Text)
	}

	return nil
}
