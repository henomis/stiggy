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

// Command hello is the smallest fleet: one agent, one flow, defined in YAML
// and run in-process. It shows the whole loop: parse the fleet, start the
// app, start an execution, read its output.
//
//	nats-server -js &
//	go run ./examples/hello
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

// fleet is what `stiggy run fleet.yaml` would serve. The model is supplied
// from Go below, so the YAML only names it.
const fleet = `
namespace: example-hello
agents:
  greeter:
    model: main
    role: Greeter
    goal: Greet people warmly, in one short sentence.
flows:
  greet:
    nodes:
      - {id: hello, type: task, agent: greeter, prompt: "Greet {{.input.name}}."}
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

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	f, _, err := config.Parse([]byte(fleet))
	if err != nil {
		return err
	}

	model, desc := exutil.Model(stiggytest.Reply(func(p string) string {
		return "Hello, " + strings.TrimSuffix(strings.TrimPrefix(p, "Greet "), ".") + "! Welcome aboard."
	}))
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

	id, err := app.Client().Start(ctx, "greet", map[string]any{"name": "Ada"})
	if err != nil {
		return err
	}

	st, err := app.Client().Wait(ctx, id)
	if err != nil {
		return err
	}

	var answer struct{ Text string }
	if err = st.DecodeOutput(&answer); err != nil {
		return err
	}

	fmt.Fprintf(out, "execution %s: %s\n%s\n", id, st.Status, answer.Text)

	return nil
}
