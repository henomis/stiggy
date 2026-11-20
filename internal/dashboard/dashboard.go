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

// Package dashboard serves the dashboard: a browser page showing what stiggy
// deployments do across every packtrail namespace of a NATS account — the
// executions, where each one is in its flow, what it spends, and which steps
// wait for a human. It reads through packtrail's client and the NATS Agent
// Protocol and, unless read-only, answers waiting steps and cancels
// executions. It is separate from the designer, which never touches NATS.
package dashboard

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	natsagent "github.com/henomis/phero/v2/nats"
)

const (
	// maxBody bounds a request body.
	maxBody = 1 << 20
	// requestTTL bounds one API request against NATS.
	requestTTL = 20 * time.Second
	// knownTTL is how long the namespace list is reused.
	knownTTL = 5 * time.Second
)

// ErrUnknownNamespace is returned for a namespace that is not provisioned or
// not allowed.
var ErrUnknownNamespace = errors.New("dashboard: unknown namespace")

// ErrReadOnly is returned for an action on a read-only dashboard.
var ErrReadOnly = errors.New("dashboard: read-only")

// Server serves the dashboard's API and, optionally, its built page.
type Server struct {
	nc       *nats.Conn
	allow    []string
	ui       fs.FS
	readOnly bool
	logger   *slog.Logger

	mu      sync.Mutex
	clients map[string]*packtrail.Client
	known   []string
	knownAt time.Time

	usage usageCache
}

// Option configures a [Server].
type Option func(*Server)

// WithNamespaces restricts the dashboard to these namespaces. Without it,
// every namespace provisioned on the account is shown.
func WithNamespaces(ns ...string) Option {
	return func(s *Server) { s.allow = append(s.allow, ns...) }
}

// WithUI serves the built page from fsys. Without it, "/" explains how to
// build the page.
func WithUI(fsys fs.FS) Option {
	return func(s *Server) { s.ui = fsys }
}

// WithReadOnly turns off answering and cancelling.
func WithReadOnly() Option {
	return func(s *Server) { s.readOnly = true }
}

// WithLogger sets the logger of the packtrail clients.
func WithLogger(l *slog.Logger) Option {
	return func(s *Server) { s.logger = l }
}

// New returns a dashboard reading through nc.
func New(nc *nats.Conn, opts ...Option) *Server {
	s := &Server{nc: nc, clients: map[string]*packtrail.Client{}, logger: slog.New(slog.DiscardHandler)}
	for _, opt := range opts {
		opt(s)
	}

	s.usage.m = map[string]Usage{}

	return s
}

// Handler returns the HTTP handler.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/overview", s.overview)
	mux.HandleFunc("GET /api/live", s.live)
	mux.HandleFunc("GET /api/ns/{ns}/executions", s.executions)
	mux.HandleFunc("GET /api/ns/{ns}/executions/{id}", s.execution)
	mux.HandleFunc("GET /api/ns/{ns}/executions/{id}/timeline", s.timeline)
	mux.HandleFunc("GET /api/ns/{ns}/executions/{id}/progress", s.progress)
	mux.HandleFunc("POST /api/ns/{ns}/executions/{id}/resume", s.resume)
	mux.HandleFunc("POST /api/ns/{ns}/executions/{id}/cancel", s.cancel)
	mux.HandleFunc("GET /api/ns/{ns}/flows/{flow}", s.flowGraph)

	if s.ui != nil {
		mux.Handle("GET /", spa(s.ui))
	} else {
		mux.HandleFunc("GET /{$}", placeholder)
	}

	return securityHeaders(mux)
}

// namespaces lists the namespaces shown: provisioned, and allowed.
func (s *Server) namespaces(ctx context.Context) ([]string, error) {
	s.mu.Lock()
	if s.known != nil && time.Since(s.knownAt) < knownTTL {
		known := s.known
		s.mu.Unlock()

		return known, nil
	}
	s.mu.Unlock()

	found, err := packtrail.Namespaces(ctx, s.nc)
	if err != nil {
		return nil, err
	}

	if s.allow != nil {
		found = slices.DeleteFunc(found, func(ns string) bool { return !slices.Contains(s.allow, ns) })
	}

	s.mu.Lock()
	s.known, s.knownAt = found, time.Now()
	s.mu.Unlock()

	return found, nil
}

// client returns the client of a shown namespace. A client is kept only once
// it attached: packtrail keeps a client's first attach error.
func (s *Server) client(ctx context.Context, ns string) (*packtrail.Client, error) {
	known, err := s.namespaces(ctx)
	if err != nil {
		return nil, err
	}

	if !slices.Contains(known, ns) {
		return nil, ErrUnknownNamespace
	}

	s.mu.Lock()
	c := s.clients[ns]
	s.mu.Unlock()

	if c != nil {
		return c, nil
	}

	c, err = packtrail.NewClient(s.nc, packtrail.WithClientNamespace(ns), packtrail.WithClientLogger(s.logger))
	if err != nil {
		return nil, err
	}

	if _, err = c.Flows(ctx); err != nil {
		return nil, err
	}

	s.mu.Lock()
	s.clients[ns] = c
	s.mu.Unlock()

	return c, nil
}

// agents lists the agents on the bus.
func (s *Server) agents(ctx context.Context) ([]Agent, error) {
	handles, err := natsagent.NewClient(s.nc).Discover(ctx)
	if err != nil && !errors.Is(err, natsagent.ErrNoAgentsFound) {
		return nil, err
	}

	byKey := map[string]*Agent{}
	out := []Agent{}

	for _, h := range handles {
		key := h.Owner + "/" + h.Name
		if a, ok := byKey[key]; ok {
			a.Instances++

			continue
		}

		out = append(out, Agent{Owner: h.Owner, Name: h.Name, Framework: h.Agent, Session: h.Session, Instances: 1})
		byKey[key] = &out[len(out)-1]
	}

	slices.SortFunc(out, func(a, b Agent) int {
		if a.Owner != b.Owner {
			if a.Owner < b.Owner {
				return -1
			}

			return 1
		}

		if a.Name < b.Name {
			return -1
		}

		if a.Name > b.Name {
			return 1
		}

		return 0
	})

	return out, nil
}

// Agent is an agent on the bus, its replicas counted once.
type Agent struct {
	Owner     string `json:"owner"`
	Name      string `json:"name"`
	Framework string `json:"framework"`
	Session   string `json:"session,omitempty"`
	Instances int    `json:"instances"`
}

// usageCache keeps the usage of ended executions, which never changes.
type usageCache struct {
	mu sync.Mutex
	m  map[string]Usage
}

func (u *usageCache) get(key string) (Usage, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()

	v, ok := u.m[key]

	return v, ok
}

func (u *usageCache) put(key string, v Usage) {
	u.mu.Lock()
	defer u.mu.Unlock()

	u.m[key] = v
}

// spa serves the built page, falling back to index.html for the page's own
// routes.
func spa(root fs.FS) http.Handler {
	files := http.FileServerFS(root)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, err := fs.Stat(root, cleanPath(r.URL.Path)); err != nil {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}

		files.ServeHTTP(w, r)
	})
}

func placeholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte("stiggy dashboard: this server was built without its page.\n\n" +
		"Build it into the binary with `make dashboard`. The API is served under /api/.\n"))
}

func securityHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "no-referrer")
		h.ServeHTTP(w, r)
	})
}
