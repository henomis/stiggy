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

// Command crew is a crew in YAML: a researcher and a writer
// work through tasks, the writer's draft is checked by an editor
// (guardrail), and the crew is also served on the NATS Agent Protocol, so
// any v0.3 client can call it as a single agent. The example calls it both
// ways: as a flow with the packtrail client, and as an agent with phero's
// protocol client.
//
//	nats-server -js &
//	go run ./examples/crew
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

	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/examples/internal/exutil"
	"github.com/henomis/stiggy/stiggytest"
)

const fleet = `
namespace: example-crew
agents:
  researcher: {model: main, role: Researcher, goal: "Find three accurate facts, as a bullet list."}
  writer: {model: main, role: Writer, goal: "Write short, clear paragraphs."}
  editor: {model: main, role: Editor, goal: "Accept drafts under 80 words that use the research."}
crews:
  brief:
    tasks:
      - id: research
        agent: researcher
        description: "Research {{.input.prompt}}."
        expected_output: Three facts as a bullet list.
      - id: write
        agent: writer
        description: Write a one-paragraph brief from the research.
        expected_output: One paragraph under 80 words.
        guardrail: {agent: editor, max_retries: 1}
    budget: {agent_steps: 10}
expose:
  owner: examples
  flows: [brief]
`

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

func fake(r stiggytest.Request) stiggytest.Response {
	switch {
	case strings.Contains(r.System, "Researcher"):
		return stiggytest.Response{Text: "- NATS is a messaging system\n- JetStream adds persistence\n- KV is built on streams"}
	case strings.Contains(r.System, "Editor"):
		return stiggytest.Response{Text: `{"valid": true, "feedback": ""}`}
	default:
		return stiggytest.Response{Text: "NATS is a messaging system; JetStream adds persistence, and KV builds on it."}
	}
}

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	f, src, err := config.Parse([]byte(fleet))
	if err != nil {
		return fmt.Errorf("%s", strings.Join(src.Describe(err), "\n"))
	}

	model, desc := exutil.Model(stiggytest.Model(fake))
	fmt.Fprintln(out, "model:", desc)

	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModel("main", model), stiggy.WithLogger(exutil.Quiet()))
	if err != nil {
		return err
	}

	stopApp, err := exutil.Serve(ctx, app)
	if err != nil {
		return err
	}
	defer stopApp() //nolint:errcheck // shutdown at the end of the example

	// 1. As a flow.
	st, err := exutil.RunFlow(ctx, app, "brief", map[string]any{"prompt": "NATS"}, out)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "as a flow (write ran %d times):\n%s\n\n", st.Visits["write"], exutil.Text(st.Output))

	// 2. As a NATS Agent Protocol agent, the way any v0.3 client would.
	client := natsagent.NewClient(nc)

	agents, err := client.Discover(ctx, natsagent.FilterByOwner("examples"))
	if err != nil {
		return err
	}

	for _, a := range agents {
		if a.Name != "brief" {
			continue
		}

		stream, err := a.Prompt(ctx, "NATS JetStream")
		if err != nil {
			return err
		}

		answer, err := stream.Text(ctx)
		stream.Close()

		if err != nil {
			return err
		}

		fmt.Fprintf(out, "as an agent (%s/%s via %s):\n%s\n", a.Owner, a.Name, a.Agent, answer)
	}

	return nil
}
