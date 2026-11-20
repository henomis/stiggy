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

package dashboard

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/henomis/packtrail"
)

const (
	// overviewLimit is how many recent executions a namespace's overview reads.
	overviewLimit = 200
	// usageSample is how many recent executions the overview sums usage over.
	usageSample = 40
	// recentShown and failuresShown bound the overview's lists.
	recentShown   = 8
	failuresShown = 6
	// throughputHours is the span of the throughput chart, one bucket an hour.
	throughputHours = 24
	// defaultLimit and maxLimit bound an executions listing.
	defaultLimit = 100
	maxLimit     = 500
	// parallelGets bounds concurrent state reads.
	parallelGets = 8
)

// statusQuarantined is shown for executions the dispatcher gave up on.
const statusQuarantined = "quarantined"

// Summary is an execution in a list.
type Summary struct {
	Namespace string            `json:"namespace"`
	ExecID    string            `json:"exec_id"`
	Flow      string            `json:"flow"`
	Status    string            `json:"status"`
	Created   time.Time         `json:"created"`
	Updated   time.Time         `json:"updated"`
	Error     string            `json:"error,omitempty"`
	Reason    string            `json:"reason,omitempty"`
	Parent    string            `json:"parent,omitempty"`
	Attrs     map[string]string `json:"attrs,omitempty"`
	// Usage and Nodes are set when the state was read.
	Usage *Usage                `json:"usage,omitempty"`
	Nodes map[string]NodeStatus `json:"nodes,omitempty"`
}

func summaryOf(ns string, s *packtrail.Summary) Summary {
	out := Summary{
		Namespace: ns, ExecID: s.ExecID, Flow: s.Flow, Status: string(s.Status),
		Created: s.Created, Updated: s.Updated, Error: s.Error, Reason: s.Reason, Attrs: s.Attrs,
	}

	if s.Parent != nil {
		out.Parent = s.Parent.ExecID
	}

	if s.Quarantined {
		out.Status, out.Error = statusQuarantined, s.QuarantineReason
	}

	return out
}

// Bucket is one hour of the throughput chart.
type Bucket struct {
	Hour     time.Time `json:"hour"`
	Started  int       `json:"started"`
	Failed   int       `json:"failed"`
	Finished int       `json:"finished"`
}

// FlowStats is a flow of a namespace.
type FlowStats struct {
	Name   string         `json:"name"`
	Counts map[string]int `json:"counts"`
	Last   time.Time      `json:"last,omitzero"`
}

// Namespace is the overview of one namespace.
type Namespace struct {
	Name       string         `json:"name"`
	Counts     map[string]int `json:"counts"`
	Total      int            `json:"total"`
	Flows      []FlowStats    `json:"flows"`
	Usage      Usage          `json:"usage"`
	Sampled    int            `json:"sampled"`
	Throughput []Bucket       `json:"throughput"`
	Recent     []Summary      `json:"recent"`
	Failures   []Summary      `json:"failures"`
	Error      string         `json:"error,omitempty"`
}

// Waiting is an execution with steps waiting for a human.
type Waiting struct {
	Summary

	Waits []Wait `json:"waits"`
}

// Overview is everything the front page shows.
type Overview struct {
	Namespaces  []Namespace `json:"namespaces"`
	Waiting     []Waiting   `json:"waiting"`
	Agents      []Agent     `json:"agents"`
	AgentsError string      `json:"agents_error,omitempty"`
	ReadOnly    bool        `json:"read_only"`
	Generated   time.Time   `json:"generated"`
}

func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	known, err := s.namespaces(ctx)
	if err != nil {
		httpError(w, err)

		return
	}

	out := Overview{
		Namespaces: make([]Namespace, len(known)), Waiting: []Waiting{}, ReadOnly: s.readOnly,
		Generated: time.Now().UTC(),
	}

	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		waiting []Waiting
	)

	for i, ns := range known {
		wg.Go(func() {
			view, ws := s.namespaceView(ctx, ns, out.Generated)

			mu.Lock()
			defer mu.Unlock()

			out.Namespaces[i] = view

			waiting = append(waiting, ws...)
		})
	}

	wg.Go(func() {
		agents, aerr := s.agents(ctx)

		mu.Lock()
		defer mu.Unlock()

		out.Agents = agents
		if aerr != nil {
			out.AgentsError = aerr.Error()
		}
	})

	wg.Wait()

	sort.Slice(waiting, func(i, j int) bool { return waiting[i].Updated.Before(waiting[j].Updated) })

	if waiting != nil {
		out.Waiting = waiting
	}

	if out.Agents == nil {
		out.Agents = []Agent{}
	}

	writeJSON(w, out)
}

// namespaceView reads a namespace's overview and its executions waiting for
// a human.
func (s *Server) namespaceView(ctx context.Context, ns string, now time.Time) (Namespace, []Waiting) {
	view := Namespace{
		Name: ns, Counts: map[string]int{}, Flows: []FlowStats{}, Recent: []Summary{}, Failures: []Summary{},
		Throughput: throughput(nil, now),
	}

	c, err := s.client(ctx, ns)
	if err != nil {
		view.Error = err.Error()

		return view, nil
	}

	list, err := c.List(ctx, packtrail.ListFilter{Limit: overviewLimit})
	if err != nil {
		view.Error = err.Error()

		return view, nil
	}

	sums := make([]Summary, len(list))
	for i := range list {
		sums[i] = summaryOf(ns, &list[i])
	}

	view.Total = len(sums)
	view.Throughput = throughput(sums, now)
	view.Flows = flowStats(ctx, c, sums)

	for _, sm := range sums {
		view.Counts[sm.Status]++

		if len(view.Recent) < recentShown {
			view.Recent = append(view.Recent, sm)
		}

		if (sm.Status == string(packtrail.StatusFailed) || sm.Status == statusQuarantined) &&
			len(view.Failures) < failuresShown {
			view.Failures = append(view.Failures, sm)
		}
	}

	waiting := s.sampleUsage(ctx, c, &view, sums[:min(len(sums), usageSample)])

	return view, waiting
}

// flowStats counts the executions of every flow: the registered ones, and
// any other the executions name.
func flowStats(ctx context.Context, c *packtrail.Client, sums []Summary) []FlowStats {
	flows := map[string]*FlowStats{}
	get := func(name string) *FlowStats {
		f := flows[name]
		if f == nil {
			f = &FlowStats{Name: name, Counts: map[string]int{}}
			flows[name] = f
		}

		return f
	}

	if defs, err := c.Flows(ctx); err == nil {
		for _, d := range defs {
			get(d.Name)
		}
	}

	for _, sm := range sums {
		f := get(sm.Flow)
		f.Counts[sm.Status]++

		if sm.Created.After(f.Last) {
			f.Last = sm.Created
		}
	}

	out := make([]FlowStats, 0, len(flows))
	for _, f := range flows {
		out = append(out, *f)
	}

	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })

	return out
}

// sampleUsage reads the states of sample: their usage is summed into view
// and set on each, and those waiting for a human are returned.
func (s *Server) sampleUsage(ctx context.Context, c *packtrail.Client, view *Namespace, sample []Summary) []Waiting {
	var waiting []Waiting

	for i, st := range s.readStates(ctx, c, sample) {
		if st == nil {
			continue
		}

		u := s.usageOf(view.Name, st)
		sample[i].Usage, sample[i].Nodes = &u, NodeStatuses(st)
		view.Usage.Add(u)
		view.Sampled++

		if ws := WaitsOf(st); len(ws) > 0 {
			waiting = append(waiting, Waiting{Summary: sample[i], Waits: ws})
		}
	}

	// The recent list shares the sample's first entries.
	for i := range view.Recent {
		if i < len(sample) {
			view.Recent[i].Usage, view.Recent[i].Nodes = sample[i].Usage, sample[i].Nodes
		}
	}

	return waiting
}

// throughput buckets the executions started in the last day by hour.
func throughput(sums []Summary, now time.Time) []Bucket {
	top := now.UTC().Truncate(time.Hour)
	out := make([]Bucket, throughputHours)

	for i := range out {
		out[i].Hour = top.Add(-time.Duration(throughputHours-1-i) * time.Hour)
	}

	idx := func(t time.Time) int {
		d := top.Sub(t.UTC().Truncate(time.Hour))
		i := throughputHours - 1 - int(d/time.Hour)

		if d < 0 || i < 0 {
			return -1
		}

		return i
	}

	for _, s := range sums {
		if i := idx(s.Created); i >= 0 {
			out[i].Started++
		}

		switch s.Status {
		case string(packtrail.StatusCompleted), string(packtrail.StatusFailed), string(packtrail.StatusCancelled):
			if i := idx(s.Updated); i >= 0 {
				out[i].Finished++

				if s.Status == string(packtrail.StatusFailed) {
					out[i].Failed++
				}
			}
		}
	}

	return out
}

// readStates reads the states of sums, a few at a time; a state that cannot
// be read is nil.
func (s *Server) readStates(ctx context.Context, c *packtrail.Client, sums []Summary) []*packtrail.State {
	out := make([]*packtrail.State, len(sums))
	sem := make(chan struct{}, parallelGets)

	var wg sync.WaitGroup

	for i := range sums {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()

			if st, err := c.Get(ctx, sums[i].ExecID); err == nil {
				out[i] = st
			}
		})
	}

	wg.Wait()

	return out
}

// usageOf is the usage of st, cached once the execution ended.
func (s *Server) usageOf(ns string, st *packtrail.State) Usage {
	key := ns + "/" + st.ExecID
	if u, ok := s.usage.get(key); ok {
		return u
	}

	u := UsageOf(st.Counters)
	if st.Status.Terminal() {
		s.usage.put(key, u)
	}

	return u
}

func (s *Server) executions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	ns := r.PathValue("ns")

	c, err := s.client(ctx, ns)
	if err != nil {
		httpError(w, err)

		return
	}

	q := r.URL.Query()

	limit := defaultLimit
	if v := q.Get("limit"); v != "" {
		if limit, err = strconv.Atoi(v); err != nil || limit < 1 {
			httpError(w, fmt.Errorf("%w: limit %q", packtrail.ErrInvalidArgument, v))

			return
		}
	}

	list, err := c.List(ctx, packtrail.ListFilter{
		Status: packtrail.Status(q.Get("status")), Flow: q.Get("flow"), Limit: min(limit, maxLimit),
	})
	if err != nil {
		httpError(w, err)

		return
	}

	sums := make([]Summary, len(list))
	for i := range list {
		sums[i] = summaryOf(ns, &list[i])
	}

	head := sums[:min(len(sums), defaultLimit)]
	for i, st := range s.readStates(ctx, c, head) {
		if st != nil {
			u := s.usageOf(ns, st)
			head[i].Usage, head[i].Nodes = &u, NodeStatuses(st)
		}
	}

	writeJSON(w, map[string]any{"executions": sums})
}

// Execution is one execution, shaped for its page.
type Execution struct {
	Summary

	Graph      *Graph                `json:"graph,omitempty"`
	Nodes      map[string]NodeStatus `json:"nodes"`
	Waits      []Wait                `json:"waits"`
	Input      []Field               `json:"input"`
	Output     []Field               `json:"output"`
	Errors     []NodeFailure         `json:"errors"`
	Children   []Child               `json:"children"`
	Budget     []Meter               `json:"budget"`
	ForkedFrom string                `json:"forked_from,omitempty"`
	FailedNode string                `json:"failed_node,omitempty"`
	LastNode   string                `json:"last_node,omitempty"`
	Steps      int                   `json:"steps"`
	Archived   bool                  `json:"archived,omitempty"`
}

// NodeFailure is the last failure of a node.
type NodeFailure struct {
	Node    string `json:"node"`
	Error   string `json:"error"`
	Reason  string `json:"reason,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
}

// Child is a running child execution.
type Child struct {
	Node   string `json:"node"`
	ExecID string `json:"exec_id"`
	Flow   string `json:"flow"`
}

// Meter is a budgeted counter: how much of its cap is spent.
type Meter struct {
	Counter string  `json:"counter"`
	Used    float64 `json:"used"`
	Limit   float64 `json:"limit"`
}

func (s *Server) execution(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	ns, id := r.PathValue("ns"), r.PathValue("id")

	c, err := s.client(ctx, ns)
	if err != nil {
		httpError(w, err)

		return
	}

	st, err := c.Get(ctx, id)
	if err != nil {
		httpError(w, err)

		return
	}

	out := executionOf(ns, st)
	u := s.usageOf(ns, st)
	out.Usage = &u

	if def, ferr := c.Flow(ctx, st.Flow, st.FlowHash); ferr == nil {
		g := GraphOf(def)
		out.Graph = &g

		for k, v := range def.Budget {
			out.Budget = append(out.Budget, Meter{Counter: k, Used: st.Counters[k], Limit: v})
		}

		sort.Slice(out.Budget, func(i, j int) bool { return out.Budget[i].Counter < out.Budget[j].Counter })
	}

	writeJSON(w, out)
}

// executionOf shapes st, without its graph and usage.
func executionOf(ns string, st *packtrail.State) Execution {
	out := Execution{
		Summary: Summary{
			Namespace: ns, ExecID: st.ExecID, Flow: st.Flow, Status: string(st.Status),
			Created: st.Created, Updated: st.Updated, Error: st.Error, Reason: st.Reason, Attrs: st.Attrs,
		},
		Nodes: NodeStatuses(st), Waits: WaitsOf(st), Input: FieldsOf(st.Input), Output: FieldsOf(st.Output),
		Errors: []NodeFailure{}, Children: []Child{}, Budget: []Meter{},
		ForkedFrom: st.ForkedFrom, FailedNode: st.FailedNode, LastNode: st.LastNode, Steps: st.Steps,
		Archived: st.Archived,
	}

	if st.Parent != nil {
		out.Parent = st.Parent.ExecID
	}

	if out.Waits == nil {
		out.Waits = []Wait{}
	}

	if out.Input == nil {
		out.Input = []Field{}
	}

	if out.Output == nil {
		out.Output = []Field{}
	}

	for node, e := range st.Errors {
		out.Errors = append(out.Errors, NodeFailure{Node: node, Error: e.Error, Reason: e.Reason, Attempt: e.Attempt})
	}

	sort.Slice(out.Errors, func(i, j int) bool { return out.Errors[i].Node < out.Errors[j].Node })

	for key, ch := range st.Children {
		out.Children = append(out.Children, Child{Node: ch.NodeOf(key), ExecID: ch.ChildID, Flow: ch.Flow})
	}

	sort.Slice(out.Children, func(i, j int) bool { return out.Children[i].ExecID < out.Children[j].ExecID })

	return out
}

func (s *Server) timeline(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	c, err := s.client(ctx, r.PathValue("ns"))
	if err != nil {
		httpError(w, err)

		return
	}

	evs, err := c.History(ctx, r.PathValue("id"))
	if err != nil {
		httpError(w, err)

		return
	}

	writeJSON(w, TimelineOf(evs))
}

func (s *Server) flowGraph(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	c, err := s.client(ctx, r.PathValue("ns"))
	if err != nil {
		httpError(w, err)

		return
	}

	def, err := c.Flow(ctx, r.PathValue("flow"), r.URL.Query().Get("version"))
	if err != nil {
		httpError(w, err)

		return
	}

	writeJSON(w, GraphOf(def))
}

// ResumeRequest answers a step waiting for a human: an answer to a
// question, or an approval or feedback on a review.
type ResumeRequest struct {
	Node     string `json:"node"`
	Answer   string `json:"answer,omitempty"`
	Approve  bool   `json:"approve,omitempty"`
	Feedback string `json:"feedback,omitempty"`
	By       string `json:"by,omitempty"`
}

// value is the resume value stiggy's agent steps read.
func (q ResumeRequest) value() (map[string]any, error) {
	switch {
	case q.Node == "":
		return nil, fmt.Errorf("%w: node is required", packtrail.ErrInvalidArgument)
	case q.Approve:
		return map[string]any{"approve": true}, nil
	case strings.TrimSpace(q.Feedback) != "":
		return map[string]any{"feedback": q.Feedback}, nil
	case strings.TrimSpace(q.Answer) != "":
		return map[string]any{"answer": q.Answer}, nil
	default:
		return nil, fmt.Errorf("%w: give an answer, feedback or an approval", packtrail.ErrInvalidArgument)
	}
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	var req ResumeRequest

	c, ok := s.action(w, r, &req)
	if !ok {
		return
	}

	v, err := req.value()
	if err != nil {
		httpError(w, err)

		return
	}

	var opts []packtrail.ResumeOption
	if req.By != "" {
		opts = append(opts, packtrail.WithResumedBy(req.By))
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	if err = c.Resume(ctx, r.PathValue("id"), req.Node, v, opts...); err != nil {
		httpError(w, err)

		return
	}

	done(w)
}

func (s *Server) cancel(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Reason string `json:"reason"`
	}

	c, ok := s.action(w, r, &req)
	if !ok {
		return
	}

	if req.Reason == "" {
		req.Reason = "cancelled from the stiggy dashboard"
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	if err := c.Cancel(ctx, r.PathValue("id"), req.Reason); err != nil {
		httpError(w, err)

		return
	}

	done(w)
}

// action checks an action request and decodes its JSON body into v. Only a
// JSON body is accepted, so a plain cross-site form cannot act.
func (s *Server) action(w http.ResponseWriter, r *http.Request, v any) (*packtrail.Client, bool) {
	if s.readOnly {
		httpError(w, ErrReadOnly)

		return nil, false
	}

	if mt, _, err := mime.ParseMediaType(r.Header.Get("Content-Type")); err != nil || mt != "application/json" {
		http.Error(w, "the body must be application/json", http.StatusUnsupportedMediaType)

		return nil, false
	}

	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody)).Decode(v); err != nil {
		httpError(w, fmt.Errorf("%w: %w", packtrail.ErrInvalidArgument, err))

		return nil, false
	}

	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	c, err := s.client(ctx, r.PathValue("ns"))
	if err != nil {
		httpError(w, err)

		return nil, false
	}

	return c, true
}

func writeJSON(w http.ResponseWriter, v any) {
	writeStatus(w, http.StatusOK, v)
}

func writeStatus(w http.ResponseWriter, code int, v any) {
	b, err := json.Marshal(v)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_, _ = w.Write(append(b, '\n'))
}

// done answers a successful action.
func done(w http.ResponseWriter) { writeJSON(w, map[string]bool{"ok": true}) }

func httpError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway

	switch {
	case errors.Is(err, ErrUnknownNamespace), errors.Is(err, packtrail.ErrNotFound),
		errors.Is(err, packtrail.ErrUnknownFlow):
		code = http.StatusNotFound
	case errors.Is(err, packtrail.ErrInvalidArgument):
		code = http.StatusBadRequest
	case errors.Is(err, ErrReadOnly):
		code = http.StatusForbidden
	case errors.Is(err, packtrail.ErrTerminal):
		code = http.StatusConflict
	case errors.Is(err, context.DeadlineExceeded):
		code = http.StatusGatewayTimeout
	}

	writeStatus(w, code, map[string]string{"error": err.Error()})
}

// cleanPath is a request path made safe to join to a directory.
func cleanPath(p string) string {
	if c := strings.TrimPrefix(path.Clean("/"+p), "/"); c != "" {
		return c
	}

	return "."
}
