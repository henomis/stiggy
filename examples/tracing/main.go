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

// Command tracing runs a small newsroom fleet and exports its OpenTelemetry
// spans to Jaeger. One root span covers the execution: every step (research,
// write, review, write again) joins its trace, with the agents' runs, their
// model calls, the delegate the researcher asks and the editor's route
// nested inside.
//
// It needs NATS with JetStream and Jaeger, both in docker-compose.yaml, and
// Ollama serving gemma4:cloud:
//
//	docker compose -f examples/tracing/docker-compose.yaml up -d
//	go run ./examples/tracing
//
// Spans go to $OTEL_EXPORTER_OTLP_ENDPOINT (default http://localhost:4318).
package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"io"
	"log"
	"log/slog"
	"os"
	"os/signal"

	"github.com/nats-io/nats.go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/llm/openai"
	"github.com/henomis/phero/v2/trace"
	pherootel "github.com/henomis/phero/v2/trace/otel"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/spec"
)

// fleetYAML is also served as is by `stiggy run fleet.yaml`.
//
//go:embed fleet.yaml
var fleetYAML []byte

// serviceName is the OTel service the spans belong to.
const serviceName = "example-tracing"

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	url := os.Getenv("NATS_URL")
	if url == "" {
		url = nats.DefaultURL
	}

	nc, err := nats.Connect(url, nats.Name("stiggy-example-tracing"))
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	exporter, err := newExporter(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if err = run(ctx, nc, newModel(), exporter, os.Stdout); err != nil {
		log.Fatal(err)
	}

	ui := os.Getenv("JAEGER_UI")
	if ui == "" {
		ui = "http://localhost:16686"
	}

	fmt.Printf("open %s/search?service=%s\n", ui, serviceName)
}

// newExporter sends spans over OTLP/HTTP: to $OTEL_EXPORTER_OTLP_ENDPOINT
// when set, else to Jaeger on localhost.
func newExporter(ctx context.Context) (sdktrace.SpanExporter, error) {
	var opts []otlptracehttp.Option
	if os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT") == "" && os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT") == "" {
		opts = append(opts, otlptracehttp.WithEndpoint("localhost:4318"), otlptracehttp.WithInsecure())
	}

	return otlptracehttp.New(ctx, opts...)
}

// newModel is gemma4:cloud through a local Ollama, one call at a time.
func newModel() spec.Model {
	return spec.Model{
		Instance:  openai.New("ollama", openai.WithOllamaBaseURL(), openai.WithModel("gemma4:cloud")),
		Retries:   5,
		RateLimit: &spec.RateLimit{MaxConcurrent: 1},
	}
}

func run(ctx context.Context, nc *nats.Conn, model spec.Model, exporter sdktrace.SpanExporter, out io.Writer) error {
	f, _, err := config.Parse(fleetYAML)
	if err != nil {
		return err
	}

	// The model comes from Go (the test scripts it); the YAML's is for
	// `stiggy run`.
	delete(f.Models, "main")

	// An OTel SDK provider batches the spans to the exporter; phero's spans
	// become OTel spans with the same ids. Shutting it down, after the app
	// has stopped, flushes the last ones.
	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(resource.NewSchemaless(attribute.String("service.name", serviceName))))

	defer func() {
		if serr := tp.Shutdown(context.WithoutCancel(ctx)); serr != nil {
			fmt.Fprintln(os.Stderr, "tracing: shutdown:", serr)
		}
	}()

	exp := pherootel.New(tp, pherootel.WithContent(true))

	quiet := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	// WithTracing replaces the fleet's tracing section: this program owns
	// the OTel provider, so its root span and the steps' share it.
	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModelSpec("main", model), stiggy.WithLogger(quiet),
		stiggy.WithTracing(trace.WithIDSource(exp.IDSource()), trace.WithProcessor(exp)))
	if err != nil {
		return err
	}

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

	return article(ctx, app.Client(), tp, out)
}

// article runs one execution under a root span: its traceparent goes with
// the start, and every step of the execution joins that trace.
func article(ctx context.Context, client *packtrail.Client, tp *sdktrace.TracerProvider, out io.Writer) error {
	ctx, root := tp.Tracer(serviceName).Start(ctx, "article")
	defer root.End()

	carrier := propagation.MapCarrier{}
	propagation.TraceContext{}.Inject(ctx, carrier)

	id, err := client.Start(ctx, "article", map[string]any{"topic": "NATS JetStream"},
		packtrail.WithTraceparent(carrier.Get("traceparent")))
	if err != nil {
		return err
	}

	st, err := client.Wait(ctx, id)
	if err != nil {
		return err
	}

	if st.Status != packtrail.StatusCompleted {
		return errors.New("execution " + id + ": " + string(st.Status) + ": " + st.Error)
	}

	var output struct{ Article string }
	if err = st.DecodeOutput(&output); err != nil {
		return err
	}

	c := st.Counters
	fmt.Fprintf(out, "execution %s: %s\n%s\n", id, st.Status, output.Article)
	fmt.Fprintf(out, "%v agent runs, %v model calls, %v tool calls, %v tokens in, %v tokens out\n",
		c["agent_steps"], c["llm_calls"], c["tool_calls"], c["tokens_in"], c["tokens_out"])
	fmt.Fprintf(out, "trace %s\n", root.SpanContext().TraceID())

	return nil
}
