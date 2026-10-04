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
//	nats-server -js &
//	go run ./examples/mapreduce
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

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/examples/internal/exutil"
	"github.com/henomis/stiggy/stiggytest"
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
	case r.Schema:
		return stiggytest.Response{Text: `{"topics": ["JetStream", "Key-Value", "Object Store"]}`}
	case strings.Contains(r.System, "Researcher"):
		topic := strings.TrimSuffix(strings.TrimPrefix(r.Prompt, "Write one sentence about "), ".")

		return stiggytest.Response{Text: topic + " is part of NATS."}
	default:
		return stiggytest.Response{Text: "NATS covers streaming, key-value and object storage."}
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

	st, err := exutil.RunFlow(ctx, app, "research", map[string]any{"subject": "NATS"}, out)
	if err != nil {
		return err
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
