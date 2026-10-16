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

// Command mapreduce is a parallel map-reduce, in
// YAML: a planner returns structured topics (output_schema), a researcher
// runs once per topic in parallel (a map node), an append channel gathers
// the notes, and a writer turns them into a summary.
//
// It needs NATS with JetStream ($NATS_URL, default nats://127.0.0.1:4222)
// and Ollama serving gemma4:cloud:
//
//	nats-server -js &
//	go run ./examples/mapreduce
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
	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

const fleet = `
namespace: example-mapreduce
agents:
  planner: {model: main, role: Planner, goal: Split a subject into three research topics.}
  researcher: {model: main, role: Researcher, goal: Write one factual sentence about a topic.}
  writer: {model: main, role: Writer, goal: Summarize research notes in three sentences.}
flows:
  research:
    channels: {notes: {reducer: append}}
    output: "{summary: results.summarize.text, notes: channels.notes}"
    nodes:
      - id: plan
        type: task
        agent: planner
        prompt: "List three research topics about {{.input.subject}}."
        output_schema:
          type: object
          properties: {topics: {type: array, items: {type: string}, maxItems: 5}}
          required: [topics]
        # A real model sometimes misses the schema: retry the step.
        retry: {max_attempts: 3, delay: 1s}
        next: research
      - id: research
        type: map
        over: results.plan.topics
        max_parallel: 3
        agent: researcher
        prompt: "Write one sentence about {{.item}}."
        writes: {notes: output.text}
        next: summarize
      - id: summarize
        type: task
        agent: writer
        prompt: "Summarize these notes about {{.input.subject}}: {{json .channels.notes}}"
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-mapreduce"))
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

	id, err := app.Client().Start(ctx, "research", map[string]any{"subject": "NATS"})
	if err != nil {
		return err
	}

	st, err := app.Client().Wait(ctx, id)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "execution %s: %s (%v agent steps)\n", id, st.Status, st.Counters["agent_steps"])

	if st.Status != packtrail.StatusCompleted {
		return fmt.Errorf("execution %s: %s %s", id, st.Reason, st.Error)
	}

	var res struct {
		Summary string
		Notes   []string
	}
	if err = st.DecodeOutput(&res); err != nil {
		return err
	}

	fmt.Fprintf(out, "%d notes, gathered in parallel:\n", len(res.Notes))

	for _, n := range res.Notes {
		fmt.Fprintln(out, " -", n)
	}

	fmt.Fprintln(out, "summary:", res.Summary)

	return nil
}
