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
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/henomis/packtrail/event"
	"github.com/henomis/packtrail/flow"

	"github.com/henomis/stiggy/spec"
)

// History is an execution's event log, read for people: its timeline, what
// every step did, what humans were asked and answered, and the moments it
// can be viewed at.
type History struct {
	Timeline     Timeline                `json:"timeline"`
	Steps        map[string]*StepHistory `json:"steps"`
	Conversation []Exchange              `json:"conversation"`
	Moments      []Moment                `json:"moments"`
	// Story is the whole execution in order: every agent's answer, every
	// human reply, every routing decision.
	Story []Entry `json:"story"`
}

// StepHistory is one node: how it is set up and every attempt it made.
type StepHistory struct {
	Node   string      `json:"node"`
	Config *StepConfig `json:"config,omitempty"`
	Runs   []Run       `json:"runs"`
	// Choices are a choice node's decisions, in order.
	Choices []Mark `json:"choices,omitempty"`
}

// StepConfig is how a step is set up in its flow.
type StepConfig struct {
	Prompt         string            `json:"prompt,omitempty"`
	ExpectedOutput string            `json:"expected_output,omitempty"`
	Routes         []string          `json:"routes,omitempty"`
	Context        []string          `json:"context,omitempty"`
	Writes         map[string]string `json:"writes,omitempty"`
	MaxAttempts    int               `json:"max_attempts,omitempty"`
	Backoff        string            `json:"backoff,omitempty"`
	RetryDelay     string            `json:"retry_delay,omitempty"`
	Timeout        string            `json:"timeout,omitempty"`
	Cached         bool              `json:"cached,omitempty"`
	Schema         bool              `json:"schema,omitempty"`
}

// Run is one attempt of a step.
type Run struct {
	Key string `json:"key"`
	// Visit counts the step's visits from 1; resuming after a human stays
	// in the same visit.
	Visit   int       `json:"visit"`
	Attempt int       `json:"attempt"`
	Start   time.Time `json:"start"`
	End     time.Time `json:"end,omitzero"`
	// Outcome is a span outcome: running, done, cached, retry, failed,
	// waiting or cancelled.
	Outcome string  `json:"outcome"`
	Error   string  `json:"error,omitempty"`
	Next    string  `json:"next,omitempty"`
	Output  []Field `json:"output,omitempty"`
	// Writes names the channels the attempt wrote.
	Writes []string `json:"writes,omitempty"`
	Usage  *Usage   `json:"usage,omitempty"`
	// ResumedBy is who answered the human, when this attempt resumed one.
	ResumedBy string `json:"resumed_by,omitempty"`
	// RetryAt is when a retried attempt runs again.
	RetryAt time.Time `json:"retry_at,omitzero"`
}

// Exchange is one question or review put to a human, and the reply.
type Exchange struct {
	Node    string    `json:"node"`
	Agent   string    `json:"agent,omitempty"`
	Kind    string    `json:"kind"`
	Text    string    `json:"text"`
	AskedAt time.Time `json:"asked_at"`
	// Reply is the answer or the feedback; Approved is set for an approval.
	Reply     string    `json:"reply,omitempty"`
	Approved  bool      `json:"approved,omitempty"`
	By        string    `json:"by,omitempty"`
	RepliedAt time.Time `json:"replied_at,omitzero"`
}

// Moment is a point the execution can be viewed at: the end of one
// decision of the engine, told in words.
type Moment struct {
	Seq  uint64    `json:"seq"`
	Time time.Time `json:"time"`
	Text string    `json:"text"`
	// Node is the step the moment is about, when there is one.
	Node string `json:"node,omitempty"`
}

// textUpdated tells of channels written from outside the flow.
const textUpdated = "channels edited by hand"

// HistoryOf reads evs, with def for the steps' setup (def may be nil).
func HistoryOf(evs []event.Event, def *flow.Flow) History {
	return historyOf(evs, def, previewOf)
}

// historyOf is HistoryOf, reading the input of each recorded attempt with
// preview.
func historyOf(evs []event.Event, def *flow.Flow, preview previewer) History {
	h := &historian{
		h: History{
			Timeline: TimelineOf(evs), Steps: map[string]*StepHistory{},
			Conversation: []Exchange{}, Moments: []Moment{},
		},
		runs:    map[string]runRef{},
		visits:  newVisitCounter(),
		pending: map[string]int{},
	}

	if def != nil {
		for i := range def.Nodes {
			h.step(def.Nodes[i].ID).Config = configOf(&def.Nodes[i])
		}
	}

	var texts []string

	node := ""

	for _, ev := range evs {
		if text, n := h.add(ev); text != "" {
			texts = append(texts, text)

			if node == "" {
				node = n
			}
		}

		if ev.DecisionEnd {
			if len(texts) > 0 {
				h.h.Moments = append(h.h.Moments, Moment{
					Seq: ev.Seq, Time: ev.Time, Text: strings.Join(texts, "; "), Node: node,
				})
			}

			texts, node = nil, ""
		}
	}

	h.h.Story = storyOf(evs, def, preview)

	return h.h
}

// historian reads events one at a time.
type historian struct {
	h History
	// runs finds the run of each key/generation/attempt.
	runs map[string]runRef
	// order lists the runs as they started.
	order  []runRef
	visits *visitCounter
	// pending maps a key to its exchange waiting for a reply.
	pending map[string]int
}

// runRef is a run: its node, and its index among the node's runs.
type runRef struct {
	node string
	i    int
}

func (h *historian) at(r runRef) *Run { return &h.h.Steps[r.node].Runs[r.i] }

// lastOpen is the latest run of key, if it is still open.
func (h *historian) lastOpen(key string) *Run {
	for i := len(h.order) - 1; i >= 0; i-- {
		if r := h.at(h.order[i]); r.Key == key {
			if r.End.IsZero() {
				return r
			}

			return nil
		}
	}

	return nil
}

func (h *historian) step(node string) *StepHistory {
	s := h.h.Steps[node]
	if s == nil {
		s = &StepHistory{Node: node, Runs: []Run{}}
		h.h.Steps[node] = s
	}

	return s
}

func (h *historian) run(key string, gen, attempt int) *Run {
	ref, ok := h.runs[spanID(key, gen, attempt)]
	if !ok {
		return nil
	}

	return h.at(ref)
}

// add reads one event and tells it in words ("" for events not worth a
// moment), with the step it is about.
func (h *historian) add(ev event.Event) (string, string) {
	switch d := ev.Data.(type) {
	case *event.Started:
		return "started", ""
	case *event.Forked:
		return "forked from " + d.From + " at event " + strconv.FormatUint(d.Seq, 10), ""
	case *event.Entered:
		h.visits.enter(d.Node)

		return "", ""
	case *event.Scheduled:
		return h.scheduled(ev.Time, d), d.Node
	case *event.NodeDone:
		return h.done(ev.Time, d), d.Node
	case *event.NodeFail:
		return h.failed(ev.Time, d), d.Node
	case *event.Interrupted:
		return h.interrupted(ev.Time, d), d.Node
	case *event.Timer:
		h.timer(d)

		return "", ""
	case *event.Choice:
		m, _ := markOf(ev)
		s := h.step(d.Node)
		s.Choices = append(s.Choices, m)

		return d.Node + " " + m.Text, d.Node
	case *event.Completed:
		return "completed", ""
	case *event.Failed:
		return "failed: " + d.Error, d.Node
	case *event.Cancelled:
		return "cancelled" + suffix(d.Reason), ""
	case *event.Signal:
		return "received signal " + d.Name, ""
	case *event.Update:
		return textUpdated, ""
	default:
		if m, ok := markOf(ev); ok {
			return strings.TrimSpace(m.Node + " " + m.Text), m.Node
		}

		return "", ""
	}
}

func (h *historian) scheduled(at time.Time, d *event.Scheduled) string {
	visit := h.visits.visit(d.Key, d.Node, d.Generation)
	s := h.step(d.Node)
	ref := runRef{node: d.Node, i: len(s.Runs)}

	h.runs[spanID(d.Key, d.Generation, d.Attempt)] = ref
	h.order = append(h.order, ref)
	s.Runs = append(s.Runs, Run{
		Key: d.Key, Visit: visit, Attempt: d.Attempt, Start: at, Outcome: SpanRunning, ResumedBy: d.ResumedBy,
	})

	if i, ok := h.pending[d.Key]; ok && len(d.Resume) > 0 {
		x := &h.h.Conversation[i]
		x.By, x.RepliedAt = d.ResumedBy, at
		x.Reply, x.Approved = replyOf(d.Resume)

		delete(h.pending, d.Key)

		if x.Approved {
			return d.Node + " approved" + by(d.ResumedBy)
		}

		return d.Node + " answered" + by(d.ResumedBy)
	}

	if d.Attempt > 1 {
		return d.Node + " retried, attempt " + strconv.Itoa(d.Attempt)
	}

	return d.Node + " started"
}

func (h *historian) done(at time.Time, d *event.NodeDone) string {
	r := h.run(d.Key, d.Generation, d.Attempt)
	if r == nil {
		return d.Node + " finished"
	}

	r.End, r.Outcome, r.Next, r.Output = at, SpanDone, d.Next, FieldsOf(d.Output)
	if d.Cached {
		r.Outcome = SpanCached
	}

	for k := range d.Writes {
		r.Writes = append(r.Writes, k)
	}

	sort.Strings(r.Writes)

	if len(d.Usage) > 0 {
		u := UsageOf(d.Usage)
		r.Usage = &u
	}

	if d.Next != "" {
		return d.Node + " finished, chose " + d.Next
	}

	return d.Node + " finished"
}

func (h *historian) failed(at time.Time, d *event.NodeFail) string {
	r := h.run(d.Key, d.Generation, d.Attempt)
	if r != nil {
		r.End, r.Outcome, r.Error = at, SpanFailed, d.Error
		if d.WillRetry {
			r.Outcome = SpanRetry
		}

		if len(d.Usage) > 0 {
			u := UsageOf(d.Usage)
			r.Usage = &u
		}
	}

	if d.WillRetry {
		return d.Node + " failed, will retry"
	}

	return d.Node + " failed"
}

func (h *historian) interrupted(at time.Time, d *event.Interrupted) string {
	if r := h.lastOpen(d.Key); r != nil {
		r.End, r.Outcome = at, SpanWaiting

		if len(d.Usage) > 0 {
			u := UsageOf(d.Usage)
			r.Usage = &u
		}
	}

	x := Exchange{Node: d.Node, Kind: waitQuestion, AskedAt: at}

	var in interrupt
	if len(d.Payload) > 0 && json.Unmarshal(d.Payload, &in) == nil {
		x.Agent, x.Kind, x.Text = in.Agent, in.Kind, in.Question

		if p := in.Stiggy.Pending; p != nil {
			x.Kind, x.Text = p.Kind, p.Text
		}
	}

	if x.Kind == "" {
		x.Kind = waitQuestion
	}

	h.pending[d.Key] = len(h.h.Conversation)
	h.h.Conversation = append(h.h.Conversation, x)

	if x.Kind == waitReview {
		return d.Node + " waits for a review"
	}

	return d.Node + " asks a human"
}

// timer notes when a retried attempt runs again.
func (h *historian) timer(d *event.Timer) {
	if d.Key == "" || d.Purpose != event.TimerRetry {
		return
	}

	for i := len(h.order) - 1; i >= 0; i-- {
		if r := h.at(h.order[i]); r.Key == d.Key {
			if r.Outcome == SpanRetry {
				r.RetryAt = d.At
			}

			return
		}
	}
}

// replyOf reads a resume value: an answer, feedback, or an approval.
func replyOf(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) == nil {
		return s, false
	}

	var v struct {
		Answer   string `json:"answer"`
		Feedback string `json:"feedback"`
		Approve  bool   `json:"approve"`
	}

	if json.Unmarshal(raw, &v) != nil {
		return string(raw), false
	}

	switch {
	case v.Approve:
		return "", true
	case v.Feedback != "":
		return v.Feedback, false
	default:
		return v.Answer, false
	}
}

func configOf(n *flow.Node) *StepConfig {
	c := &StepConfig{Cached: n.Cache != nil, Schema: n.OutputSchema != nil}

	if s, ok, err := spec.StepOf(n); ok && err == nil {
		c.Prompt, c.ExpectedOutput, c.Routes, c.Context, c.Writes = s.Prompt, s.ExpectedOutput, s.Routes, s.Context, s.Writes
	}

	if n.Retry != nil {
		c.MaxAttempts, c.Backoff = n.Retry.MaxAttempts, n.Retry.Backoff
		if d := time.Duration(n.Retry.Delay); d > 0 {
			c.RetryDelay = d.String()
		}
	}

	if d := time.Duration(n.Timeout); d > 0 {
		c.Timeout = d.String()
	}

	return c
}

// visitCounter numbers the visits of steps by their entries (NodeEntered),
// so a resume after a human, which starts a new generation, stays in the
// same visit. Map items are not entered one by one: they count their
// generations instead.
type visitCounter struct {
	entered map[string]int // node -> entries so far
	gens    map[string]int // key -> generations seen
	of      map[string]int // key#generation -> visit
}

func newVisitCounter() *visitCounter {
	return &visitCounter{entered: map[string]int{}, gens: map[string]int{}, of: map[string]int{}}
}

func (v *visitCounter) enter(node string) { v.entered[node]++ }

// visit is the visit a generation of a task instance belongs to.
func (v *visitCounter) visit(key, node string, gen int) int {
	gk := key + "#" + strconv.Itoa(gen)
	if n, ok := v.of[gk]; ok {
		return n
	}

	v.gens[key]++

	n := v.entered[node]
	if n == 0 || key != node {
		n = v.gens[key]
	}

	v.of[gk] = n

	return n
}

func by(who string) string {
	if who == "" {
		return ""
	}

	return " by " + who
}

func suffix(s string) string {
	if s == "" {
		return ""
	}

	return ": " + s
}
