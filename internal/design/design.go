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

// Package design serves the designer: a browser page where workflows are
// built from blocks. The page owns the project; the server compiles it to a
// stiggy fleet (package project), checks the fleet with the same loader and
// compiler as `stiggy validate`, and saves the project and the fleet. It
// never connects to NATS and never runs a fleet.
package design

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"

	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/internal/design/project"
	"github.com/henomis/stiggy/registry"
)

// ErrNoProject is returned when the designer was started without a project
// file.
var ErrNoProject = errors.New("design: no project file")

const (
	// maxBody bounds a request body: a project is far smaller.
	maxBody = 4 << 20
	// fileMode is the mode of the files the designer creates.
	fileMode os.FileMode = 0o644
)

// Server serves the designer's API and, optionally, its built page.
type Server struct {
	reg        *registry.Registry
	activities []string
	project    string
	output     string
	ui         string
}

// Option configures a [Server].
type Option func(*Server)

// WithActivities adds activity names a library program registers, so the
// fleet checks pass; they are only checked, never run.
func WithActivities(names ...string) Option {
	return func(s *Server) { s.activities = append(s.activities, names...) }
}

// WithProject names the project file the designer reads and saves, and the
// fleet file it generates next to it.
func WithProject(path, output string) Option {
	return func(s *Server) { s.project, s.output = path, output }
}

// WithUI serves the built page from dir. Without it, "/" explains how to
// build the page.
func WithUI(dir string) Option {
	return func(s *Server) { s.ui = dir }
}

// New returns a server checking fleets against reg.
func New(reg *registry.Registry, opts ...Option) *Server {
	s := &Server{reg: reg}
	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Handler returns the HTTP handler: /api/catalog, /api/project,
// /api/compile and the page.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/catalog", s.catalog)
	mux.HandleFunc("GET /api/project", s.readProject)
	mux.HandleFunc("PUT /api/project", s.writeProject)
	mux.HandleFunc("POST /api/compile", s.compileProject)

	if s.ui != "" {
		mux.Handle("GET /", http.FileServerFS(os.DirFS(s.ui)))
	} else {
		mux.HandleFunc("GET /{$}", placeholder)
	}

	return mux
}

// Catalog is what the registry offers, by name, for the page's pickers.
type Catalog struct {
	Providers         []string `json:"providers"`
	ToolTypes         []string `json:"tool_types"`
	Activities        []string `json:"activities"`
	MemoryTypes       []string `json:"memory_types"`
	EmbedderProviders []string `json:"embedder_providers"`
	VectorStoreTypes  []string `json:"vectorstore_types"`
}

func (s *Server) catalog(w http.ResponseWriter, _ *http.Request) {
	acts := slices.Concat(s.reg.Activities(), s.activities)
	slices.Sort(acts)

	writeJSON(w, Catalog{
		Providers:         s.reg.Providers(),
		ToolTypes:         s.reg.ToolTypes(),
		Activities:        slices.Compact(acts),
		MemoryTypes:       s.reg.MemoryTypes(),
		EmbedderProviders: s.reg.EmbedderProviders(),
		VectorStoreTypes:  s.reg.VectorStoreTypes(),
	})
}

// Problem is one thing to fix, located in the project. Path is the fleet
// path stiggy reported, when it came from stiggy.
type Problem struct {
	project.Loc

	Msg  string `json:"msg"`
	Path string `json:"path,omitempty"`
}

// Compiled is a compiled project.
type Compiled struct {
	// OK is true when the fleet compiles and stiggy accepts it.
	OK       bool      `json:"ok"`
	YAML     string    `json:"yaml"`
	Problems []Problem `json:"problems,omitempty"`
}

// compile turns a project into a fleet and checks it. ${VAR} references are
// left alone: the designer never needs the fleet's secrets.
func (s *Server) compile(p *project.Project) (Compiled, error) {
	res, err := project.Compile(p)
	if err != nil {
		return Compiled{}, err
	}

	out := Compiled{YAML: string(res.YAML)}

	for _, pr := range res.Problems {
		out.Problems = append(out.Problems, Problem{Loc: pr.Loc, Msg: pr.Msg})
	}

	// stiggy's own checks run once the design is complete: before that they
	// would only repeat its problems in stiggy's words.
	if len(out.Problems) == 0 {
		out.Problems = s.check(res)
	}

	out.OK = len(out.Problems) == 0

	return out, nil
}

// check runs the fleet through stiggy and maps its problems to blocks.
func (s *Server) check(res *project.Result) []Problem {
	fleet, src, err := config.Parse(res.YAML, config.WithoutEnv())
	if err == nil {
		_, err = compile.Compile(fleet, catalog{Registry: s.reg, extra: s.activities})
	}

	if err == nil {
		return nil
	}

	var out []Problem
	for _, p := range src.Problems(err) {
		out = append(out, Problem{Loc: res.Locate(p.Path), Msg: p.Msg, Path: p.Path})
	}

	return out
}

// catalog is the registry plus the declared activities.
type catalog struct {
	*registry.Registry

	extra []string
}

func (c catalog) HasActivity(name string) bool {
	return slices.Contains(c.extra, name) || c.Registry.HasActivity(name)
}

// readBody reads and parses a project from the request.
func readBody(w http.ResponseWriter, r *http.Request) (*project.Project, bool) {
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBody))
	if err != nil {
		http.Error(w, err.Error(), http.StatusRequestEntityTooLarge)

		return nil, false
	}

	p, err := project.Parse(data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)

		return nil, false
	}

	return p, true
}

func (s *Server) compileProject(w http.ResponseWriter, r *http.Request) {
	p, ok := readBody(w, r)
	if !ok {
		return
	}

	c, err := s.compile(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	writeJSON(w, c)
}

// File is the project as the page loads it. Project is null for a new one.
type File struct {
	Path    string           `json:"path"`
	Output  string           `json:"output"`
	Project *project.Project `json:"project"`
}

func (s *Server) readProject(w http.ResponseWriter, _ *http.Request) {
	if s.project == "" {
		http.Error(w, ErrNoProject.Error(), http.StatusNotFound)

		return
	}

	f := File{Path: s.project, Output: s.output}

	data, err := os.ReadFile(s.project)

	switch {
	case errors.Is(err, os.ErrNotExist):
		// A new project: the first save creates it.
	case err != nil:
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	default:
		if f.Project, err = project.Parse(data); err != nil {
			http.Error(w, fmt.Sprintf("%s: %v", s.project, err), http.StatusUnprocessableEntity)

			return
		}
	}

	writeJSON(w, f)
}

// Saved is the result of a save: where the files went, and the compiled
// fleet. The fleet is written even when it has problems, so the file always
// matches the project.
type Saved struct {
	File
	Compiled
}

func (s *Server) writeProject(w http.ResponseWriter, r *http.Request) {
	if s.project == "" {
		http.Error(w, ErrNoProject.Error(), http.StatusNotFound)

		return
	}

	p, ok := readBody(w, r)
	if !ok {
		return
	}

	c, err := s.compile(p)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	data, err := json.MarshalIndent(p, "", "  ")
	if err == nil {
		err = writeAtomic(s.project, append(data, '\n'))
	}

	if err == nil {
		err = writeAtomic(s.output, []byte(c.YAML))
	}

	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	writeJSON(w, Saved{File: File{Path: s.project, Output: s.output}, Compiled: c})
}

// writeAtomic writes data to path through a temporary file in the same
// directory, keeping the mode of an existing file.
func writeAtomic(path string, data []byte) error {
	mode := fileMode
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("design: %w", err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err = tmp.Write(data); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("design: %w", err)
	}

	if err = tmp.Chmod(mode); err != nil {
		_ = tmp.Close()

		return fmt.Errorf("design: %w", err)
	}

	if err = tmp.Close(); err != nil {
		return fmt.Errorf("design: %w", err)
	}

	if err = os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("design: %w", err)
	}

	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write(b)
}

const placeholderPage = `<!doctype html>
<meta charset="utf-8">
<title>stiggy design</title>
<body style="font-family: system-ui, sans-serif; max-width: 40rem; margin: 3rem auto; padding: 0 1rem">
<h1>stiggy design</h1>
<p>The designer's API is running, but no page was given. Build the page and
pass its directory:</p>
<pre>make ui
stiggy design -ui ui/dist my.stiggy.json</pre>
<p>or set <code>STIGGY_UI</code> to that directory.</p>
`

func placeholder(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, placeholderPage)
}
