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
//	nats-server -js &
//	go run ./examples/router
package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/examples/internal/exutil"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nc, err := exutil.Connect()
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	if err = run(ctx, nc, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

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

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	model, desc := exutil.Model(stiggytest.Model(fake))
	fmt.Fprintln(out, "model:", desc)

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

	app, err := stiggy.New(nc,
		stiggy.WithNamespace("example-router"),
		stiggy.WithLogger(exutil.Quiet()),
		stiggy.WithModel("main", model),
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

	stopApp, err := exutil.Serve(ctx, app)
	if err != nil {
		return err
	}
	defer stopApp() //nolint:errcheck // shutdown at the end of the example

	for _, ticket := range []string{"My invoice charges me twice.", "The dashboard shows a 500 error."} {
		st, err := exutil.RunFlow(ctx, app, "support", map[string]any{"ticket": ticket}, out)
		if err != nil {
			return err
		}

		fmt.Fprintf(out, "ticket %q → %s\n%s\n\n", ticket, st.LastNode, exutil.Text(st.Output))
	}

	return nil
}
