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
// It needs NATS with JetStream ($NATS_URL, default nats://127.0.0.1:4222)
// and Ollama serving gemma4:cloud:
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
	"log/slog"
	"os"
	"os/signal"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
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

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-hitl"))
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

func run(ctx context.Context, nc *nats.Conn, model spec.Model, out io.Writer) error {
	f, _, err := config.Parse([]byte(fleet))
	if err != nil {
		return err
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
			fmt.Fprintf(out, "execution %s: %s\nfinal plan: %s\n", id, st.Status, text(st.Output))

			return nil
		}

		in := pending(st)
		fmt.Fprintf(out, "paused for a %s: %s%s\n", in.Kind, in.Question, text(in.Answer))

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

// text returns the answer of a plain agent output ({"text": ...}), or the
// raw JSON of any other output.
func text(raw json.RawMessage) string {
	var o struct{ Text string }
	if json.Unmarshal(raw, &o) == nil && o.Text != "" {
		return o.Text
	}

	return string(raw)
}
