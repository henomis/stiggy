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

// Command translator serves a translator agent on its own: no workflow, just
// the agent, exposed on the NATS Agent Protocol as translation/translator.
// Every prompt runs a fresh translator. Any program on the bus can use it;
// the support and newsletter programs next to this one do, as a step of
// their workflows, by its owner and name.
//
// It needs NATS with JetStream ($NATS_URL, default nats://127.0.0.1:4222)
// and Ollama serving gemma4:cloud. Start it first and leave it running:
//
//	nats-server -js &
//	go run ./examples/translator/translator &
//	go run ./examples/translator/support
//	go run ./examples/translator/newsletter
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

	"github.com/henomis/phero/v2/llm/openai"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

// fleet is what `stiggy run translator.yaml` would serve: an agent and
// nothing else. Each prompt runs a fresh translator; there is no workflow.
const fleet = `
namespace: example-translation
agents:
  translator:
    model: main
    role: Translator
    goal: Translate faithfully, keeping the tone and the formatting. Answer with the translation only.
    description: Translates text into the language the prompt asks for.
expose:
  owner: translation
  agents: [translator]
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-translator"))
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

// run serves the translator until ctx ends.
func run(ctx context.Context, nc *nats.Conn, model spec.Model, out io.Writer) error {
	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	f, _, err := config.Parse([]byte(fleet))
	if err != nil {
		return err
	}

	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModelSpec("main", model), stiggy.WithLogger(quiet))
	if err != nil {
		return err
	}

	// Run serves the fleet until ctx ends; Ready says when it can take work.
	done := make(chan error, 1)

	go func() { done <- app.Run(ctx) }()

	select {
	case <-app.Ready():
		fmt.Fprintln(out, "serving translation/translator; press Ctrl+C to stop")
	case err = <-done:
		return err
	}

	return <-done
}
