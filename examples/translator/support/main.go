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

// Command support answers a customer question, then has the shared
// translator translate the answer into the customer's language.
//
// The translator is not part of this fleet: it is named under remotes: and
// found on the bus, so start the translator program first. It needs NATS
// with JetStream ($NATS_URL, default nats://127.0.0.1:4222) and Ollama
// serving gemma4:cloud:
//
//	nats-server -js &
//	go run ./examples/translator/translator &
//	go run ./examples/translator/support
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
	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

// fleet is what `stiggy run support.yaml` would serve. The model is supplied
// from Go below, so the YAML only names it.
const fleet = `
namespace: example-support
remotes:
  translator: {owner: translation, name: translator, description: Translates text.}
agents:
  support_agent: {model: main, role: Support agent, goal: "Answer customer questions in two short sentences."}
flows:
  support:
    nodes:
      - {id: answer, type: task, agent: support_agent, prompt: "Answer: {{.input.question}}", next: translate}
      - id: translate
        type: task
        remote: translator
        # The translator is another program: when the instance found has
        # gone away, the next attempt finds another one.
        retry: {max_attempts: 3, delay: 1s}
        prompt: |
          Translate into {{.input.lang}}:
          {{.results.answer.text}}
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-support"))
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

	id, err := app.Client().Start(ctx, "support", map[string]any{"question": "How do I reset my password?", "lang": "Italian"})
	if err != nil {
		return err
	}

	st, err := app.Client().Wait(ctx, id)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "execution %s: %s\n", id, st.Status)

	if st.Status != packtrail.StatusCompleted {
		return fmt.Errorf("execution %s: %s %s", id, st.Reason, st.Error)
	}

	var translated struct{ Text string }
	if err = st.DecodeOutput(&translated); err != nil {
		return err
	}

	fmt.Fprintln(out, "support:", translated.Text)

	return nil
}
