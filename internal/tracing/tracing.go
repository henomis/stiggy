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

// Package tracing builds the trace exporter a fleet's `tracing:` section
// describes, as options of a phero trace provider.
package tracing

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/henomis/phero/v2/trace"
	"github.com/henomis/phero/v2/trace/jsonfile"
	pherootel "github.com/henomis/phero/v2/trace/otel"
	"github.com/henomis/phero/v2/trace/text"

	"github.com/henomis/stiggy/spec"
)

// Exporter is a built exporter: the provider options that install it, and
// the shutdown that flushes it once the provider is shut down.
type Exporter struct {
	Options  []trace.Option
	Shutdown func(ctx context.Context) error
}

// New builds the exporter t describes; namespace is the default OTel
// service name. A jsonfile exporter opens its file here.
func New(ctx context.Context, t *spec.Tracing, namespace string) (*Exporter, error) {
	switch t.Exporter {
	case spec.TracingOTLP:
		return otlp(ctx, t, namespace)
	case spec.TracingJSONFile:
		p, err := jsonfile.New(t.File)
		if err != nil {
			return nil, fmt.Errorf("tracing: %w", err)
		}

		return local(t, p), nil
	case spec.TracingText:
		return local(t, text.New(os.Stderr)), nil
	default:
		return nil, fmt.Errorf("tracing: unknown exporter %q", t.Exporter)
	}
}

// local installs a jsonfile or text processor, which captures all content
// unless the fleet turns it off or bounds it. The provider shuts it down.
func local(t *spec.Tracing, p trace.Processor) *Exporter {
	if c := t.Content; c != nil && ((c.Capture != nil && !*c.Capture) || c.MaxBytes > 0) {
		p = trace.Content(trace.ContentPolicy{Capture: c.Capture == nil || *c.Capture, MaxBytes: c.MaxBytes}, p)
	}

	return &Exporter{
		Options:  []trace.Option{trace.WithProcessor(p)},
		Shutdown: func(context.Context) error { return nil },
	}
}

// otlp installs an OTLP/HTTP exporter behind a batching OTel SDK provider.
// phero's spans and the OTel spans share ids.
func otlp(ctx context.Context, t *spec.Tracing, namespace string) (*Exporter, error) {
	var opts []otlptracehttp.Option

	switch {
	case strings.Contains(t.Endpoint, "://"):
		u, err := tracesURL(t.Endpoint)
		if err != nil {
			return nil, err
		}

		opts = append(opts, otlptracehttp.WithEndpointURL(u))
	case t.Endpoint != "":
		opts = append(opts, otlptracehttp.WithEndpoint(t.Endpoint))
	}

	if t.Insecure {
		opts = append(opts, otlptracehttp.WithInsecure())
	}

	if len(t.Headers) > 0 {
		opts = append(opts, otlptracehttp.WithHeaders(t.Headers))
	}

	exp, err := otlptracehttp.New(ctx, opts...)
	if err != nil {
		return nil, fmt.Errorf("tracing: otlp: %w", err)
	}

	res, err := serviceResource(t, namespace)
	if err != nil {
		return nil, err
	}

	tp := sdktrace.NewTracerProvider(sdktrace.WithBatcher(exp), sdktrace.WithResource(res))

	var eopts []pherootel.Option

	if c := t.Content; c != nil {
		eopts = append(eopts, pherootel.WithContent(c.Capture != nil && *c.Capture))
		if c.MaxBytes > 0 {
			eopts = append(eopts, pherootel.WithMaxAttrBytes(c.MaxBytes))
		}
	}

	e := pherootel.New(tp, eopts...)

	return &Exporter{
		Options:  []trace.Option{trace.WithIDSource(e.IDSource()), trace.WithProcessor(e)},
		Shutdown: tp.Shutdown,
	}, nil
}

// tracesPath is the OTLP/HTTP path of trace exports.
const tracesPath = "/v1/traces"

// tracesURL reads endpoint as OTEL_EXPORTER_OTLP_ENDPOINT is read: a base
// URL, completed with /v1/traces unless it already ends with it.
func tracesURL(endpoint string) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", fmt.Errorf("tracing: otlp endpoint: %w", err)
	}

	u.Path = strings.TrimSuffix(u.Path, "/")
	if !strings.HasSuffix(u.Path, tracesPath) {
		u.Path += tracesPath
	}

	return u.String(), nil
}

// serviceResource is the OTel resource of the fleet's spans: the SDK's
// default (OTEL_RESOURCE_ATTRIBUTES, OTEL_SERVICE_NAME), with service.name
// set from the fleet, or its namespace when the environment names none.
func serviceResource(t *spec.Tracing, namespace string) (*resource.Resource, error) {
	name := t.ServiceName
	if name == "" && os.Getenv("OTEL_SERVICE_NAME") == "" {
		name = namespace
	}

	if name == "" {
		return resource.Default(), nil
	}

	res, err := resource.Merge(resource.Default(), resource.NewSchemaless(attribute.String("service.name", name)))
	if err != nil {
		return nil, fmt.Errorf("tracing: resource: %w", err)
	}

	return res, nil
}
