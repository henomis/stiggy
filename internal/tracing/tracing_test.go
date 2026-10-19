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

package tracing_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/henomis/phero/v2/trace"

	"github.com/henomis/stiggy/internal/tracing"
	"github.com/henomis/stiggy/spec"
)

// TestJSONFile checks the jsonfile exporter writes spans with their
// content, and none once the fleet turns capture off.
func TestJSONFile(t *testing.T) {
	off := false

	for name, tc := range map[string]struct {
		content *spec.TracingContent
		want    bool
	}{
		"default":     {nil, true},
		"capture off": {&spec.TracingContent{Capture: &off}, false},
	} {
		t.Run(name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "trace.ndjson")

			exp, err := tracing.New(t.Context(), &spec.Tracing{Exporter: spec.TracingJSONFile, File: file,
				Content: tc.content}, "ns")
			if err != nil {
				t.Fatal(err)
			}

			p := trace.NewProvider(exp.Options...)
			ctx := trace.WithProvider(t.Context(), p)
			_, span := trace.Start(ctx, "execute_tool t", &trace.ToolCall{Name: "t", Arguments: "secret-args"})
			span.End(nil)

			if err = p.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			if err = exp.Shutdown(t.Context()); err != nil {
				t.Fatal(err)
			}

			data, err := os.ReadFile(file)
			if err != nil {
				t.Fatal(err)
			}

			if !strings.Contains(string(data), `"execute_tool t"`) {
				t.Fatalf("no span written: %s", data)
			}

			if got := strings.Contains(string(data), "secret-args"); got != tc.want {
				t.Errorf("content captured = %v, want %v: %s", got, tc.want, data)
			}
		})
	}
}

// TestOTLP checks the otlp exporter posts spans to the collector, reading
// a URL endpoint as a base: /v1/traces is appended unless already there.
func TestOTLP(t *testing.T) {
	var (
		mu    sync.Mutex
		paths []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()

		paths = append(paths, r.URL.Path)

		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	for endpoint, want := range map[string]string{
		srv.URL:                      "/v1/traces",
		srv.URL + "/":                "/v1/traces",
		srv.URL + "/otlp":            "/otlp/v1/traces",
		srv.URL + "/v1/traces":       "/v1/traces",
		srv.URL + "/otlp/v1/traces/": "/otlp/v1/traces",
	} {
		mu.Lock()
		paths = nil
		mu.Unlock()

		exp, err := tracing.New(t.Context(), &spec.Tracing{Exporter: spec.TracingOTLP, Endpoint: endpoint}, "ns")
		if err != nil {
			t.Fatal(err)
		}

		p := trace.NewProvider(exp.Options...)
		_, span := trace.Start(trace.WithProvider(t.Context(), p), "stiggy.step s", nil)
		span.End(nil)

		ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)

		if err = errors.Join(p.Shutdown(ctx), exp.Shutdown(ctx)); err != nil {
			t.Fatalf("%s: shutdown: %v", endpoint, err)
		}

		cancel()

		mu.Lock()
		got := slices.Clone(paths)
		mu.Unlock()

		if len(got) == 0 || got[0] != want {
			t.Errorf("%s: posted to %v, want %s", endpoint, got, want)
		}
	}
}

func TestUnknownExporter(t *testing.T) {
	if _, err := tracing.New(t.Context(), &spec.Tracing{Exporter: "zipkin"}, "ns"); err == nil {
		t.Fatal("an unknown exporter was built")
	}
}
