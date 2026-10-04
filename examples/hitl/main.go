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

// Command hitl is human in the loop (ask_human and human_input): the planner may ask a human a question through the human
// route, and its plan is reviewed before the step completes. The execution
// pauses durably at each point and resumes with Client.Resume; here a
// simulated human answers, so the example runs unattended.
//
//	nats-server -js &
//	go run ./examples/hitl
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/examples/internal/exutil"
	"github.com/henomis/stiggy/stiggytest"
)

const fleet = `
namespace: example-hitl
agents:
  planner:
    model: main
    role: Trip planner
    goal: Plan short trips. If the destination is missing, ask the human with ask_human before planning.
flows:
  trip:
    nodes:
      - id: plan
        type: task
        agent: planner
        prompt: "Plan a weekend trip for {{.input.traveller}}."
        routes: [human]
        human_input: true
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

// human answers whatever the paused step waits for: a question, or a
// review (asking for a shorter plan once, then approving).
type human struct{ reviews int }

func (h *human) reply(kind string) any {
	if kind == "question" {
		return "Rome"
	}

	h.reviews++
	if h.reviews == 1 {
		return map[string]any{"feedback": "Make it shorter, two lines at most."}
	}

	return map[string]any{"approve": true}
}

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	f, _, err := config.Parse([]byte(fleet))
	if err != nil {
		return err
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

	c := app.Client()

	id, err := c.Start(ctx, "trip", map[string]any{"traveller": "Ada"})
	if err != nil {
		return err
	}

	h := &human{}

	for {
		// Wait until the execution ends or pauses for a human.
		st, werr := c.WaitUntil(ctx, id, func(s *packtrail.State) bool { return pending(s) != nil })
		if werr != nil && st == nil {
			return werr
		}

		if st.Status.Terminal() {
			fmt.Fprintf(out, "execution %s: %s\nfinal plan: %s\n", id, st.Status, exutil.Text(st.Output))

			return nil
		}

		in := pending(st)
		fmt.Fprintf(out, "paused for a %s: %s%s\n", in.Kind, in.Question, exutil.Text(in.Answer))

		answer := h.reply(in.Kind)
		fmt.Fprintf(out, "human: %v\n", answer)

		if err = c.Resume(ctx, id, "plan", answer); err != nil {
			return err
		}
	}
}

type interrupt struct {
	Kind     string
	Question string
	Answer   json.RawMessage
}

// pending returns what a paused step waits for, nil when nothing waits.
func pending(st *packtrail.State) *interrupt {
	for _, t := range st.Tasks {
		if t.Status != "interrupted" {
			continue
		}

		var in interrupt
		if json.Unmarshal(t.Interrupt, &in) == nil {
			return &in
		}
	}

	return nil
}
