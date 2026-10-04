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

package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/henomis/stiggy"
)

// Timeouts of the health server.
const (
	healthReadTimeout     = 5 * time.Second
	healthShutdownTimeout = 5 * time.Second
)

// serveHealth serves the process's health on addr:
//   - /healthz answers 200 while the process runs (liveness);
//   - /readyz answers 200 once the app is ready, 503 before (readiness).
//
// The returned stop shuts the server down.
func serveHealth(addr string, app *stiggy.App, logger *slog.Logger) (func(), error) {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("-http: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		select {
		case <-app.Ready():
			_, _ = w.Write([]byte("ready\n"))
		default:
			http.Error(w, "starting", http.StatusServiceUnavailable)
		}
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: healthReadTimeout}

	go func() {
		if serr := srv.Serve(ln); serr != nil && !errors.Is(serr, http.ErrServerClosed) {
			logger.Error("stiggy: health server", "error", serr)
		}
	}()

	logger.Info("stiggy: health endpoints", "addr", ln.Addr().String())

	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), healthShutdownTimeout)
		defer cancel()

		_ = srv.Shutdown(ctx)
	}, nil
}

func splitList(s string) []string {
	var out []string

	for p := range strings.SplitSeq(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}

	return out
}
