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
	"strconv"
	"time"

	"github.com/henomis/packtrail/event"
)

// Span outcomes.
const (
	SpanRunning = "running"
	SpanDone    = "done"
	SpanCached  = "cached"
	SpanRetry   = "retry"
	SpanFailed  = "failed"
	SpanWaiting = "waiting"
	SpanHuman   = "human"
	SpanCancel  = "cancelled"
)

// Span is one attempt of a task instance, or the time a human took to answer
// it.
type Span struct {
	Node    string    `json:"node"`
	Key     string    `json:"key"`
	Attempt int       `json:"attempt,omitempty"`
	Start   time.Time `json:"start"`
	// End is zero while the span is open.
	End     time.Time `json:"end,omitzero"`
	Outcome string    `json:"outcome"`
	Error   string    `json:"error,omitempty"`
	// Next is the route the attempt chose.
	Next string `json:"next,omitempty"`
	// By is who answered, on a human span.
	By      string  `json:"by,omitempty"`
	Tokens  float64 `json:"tokens,omitempty"`
	CostUSD float64 `json:"cost_usd,omitempty"`
}

// Mark is a point in the timeline: a choice, a fan-out, a signal.
type Mark struct {
	Time time.Time `json:"time"`
	Node string    `json:"node,omitempty"`
	Text string    `json:"text"`
}

// Timeline is an execution laid out in time.
type Timeline struct {
	Start time.Time `json:"start"`
	End   time.Time `json:"end,omitzero"`
	Spans []Span    `json:"spans"`
	Marks []Mark    `json:"marks"`
}

// TimelineOf lays out an execution's events.
func TimelineOf(evs []event.Event) Timeline {
	b := &builder{
		tl:     Timeline{Spans: []Span{}, Marks: []Mark{}},
		open:   map[string]int{},
		paused: map[string]int{},
	}

	for _, ev := range evs {
		b.add(ev)
	}

	return b.tl
}

// builder lays out events one at a time.
type builder struct {
	tl     Timeline
	open   map[string]int // key/generation/attempt -> its running span
	paused map[string]int // key -> its human span
}

func (b *builder) add(ev event.Event) {
	if b.tl.Start.IsZero() {
		b.tl.Start = ev.Time
	}

	if ev.Type.Terminal() {
		b.tl.End = ev.Time
	}

	switch d := ev.Data.(type) {
	case *event.Scheduled:
		b.scheduled(ev.Time, d)
	case *event.NodeDone:
		b.close(spanID(d.Key, d.Generation, d.Attempt), ev.Time, func(s *Span) {
			s.Outcome, s.Next = SpanDone, d.Next
			if d.Cached {
				s.Outcome = SpanCached
			}

			s.spent(d.Usage)
		})
	case *event.NodeFail:
		b.close(spanID(d.Key, d.Generation, d.Attempt), ev.Time, func(s *Span) {
			s.Outcome, s.Error = SpanFailed, d.Error
			if d.WillRetry {
				s.Outcome = SpanRetry
			}

			s.spent(d.Usage)
		})
	case *event.Interrupted:
		b.closeKey(d.Key, ev.Time, func(s *Span) {
			s.Outcome = SpanWaiting
			s.spent(d.Usage)
		})

		b.paused[d.Key] = len(b.tl.Spans)
		b.tl.Spans = append(b.tl.Spans, Span{Node: d.Node, Key: d.Key, Start: ev.Time, Outcome: SpanHuman})
	case *event.NodeCancel:
		b.closeKey(d.Key, ev.Time, func(s *Span) { s.Outcome = SpanCancel })
	default:
		if m, ok := markOf(ev); ok {
			b.tl.Marks = append(b.tl.Marks, m)
		}
	}
}

// scheduled opens an attempt's span, ending the human span it resumes.
func (b *builder) scheduled(at time.Time, d *event.Scheduled) {
	if i, ok := b.paused[d.Key]; ok {
		b.tl.Spans[i].End, b.tl.Spans[i].By = at, d.ResumedBy

		delete(b.paused, d.Key)
	}

	b.open[spanID(d.Key, d.Generation, d.Attempt)] = len(b.tl.Spans)
	b.tl.Spans = append(b.tl.Spans, Span{
		Node: d.Node, Key: d.Key, Attempt: d.Attempt, Start: at, Outcome: SpanRunning,
	})
}

func (b *builder) close(id string, at time.Time, f func(*Span)) {
	i, ok := b.open[id]
	if !ok {
		return
	}

	delete(b.open, id)

	b.tl.Spans[i].End = at
	f(&b.tl.Spans[i])
}

// closeKey closes every open span of a task instance.
func (b *builder) closeKey(key string, at time.Time, f func(*Span)) {
	for id, i := range b.open {
		if b.tl.Spans[i].Key == key {
			b.close(id, at, f)
		}
	}
}

func (s *Span) spent(u map[string]float64) {
	s.Tokens, s.CostUSD = u["tokens_in"]+u["tokens_out"], u["cost_usd"]
}

func markOf(ev event.Event) (Mark, bool) {
	switch d := ev.Data.(type) {
	case *event.Choice:
		text := "chose " + d.To
		if d.Name != "" {
			text += " (" + d.Name + ")"
		}

		return Mark{Time: ev.Time, Node: d.Node, Text: text}, true
	case *event.Fanout:
		return Mark{Time: ev.Time, Node: d.Node, Text: "fanned out to " + strconv.Itoa(len(d.Branches)) + " branches"}, true
	case *event.Join:
		return Mark{Time: ev.Time, Node: d.Node, Text: joinText(d)}, true
	case *event.Signal:
		return Mark{Time: ev.Time, Text: "signal " + d.Name}, true
	case *event.Child:
		return Mark{Time: ev.Time, Text: "started a child execution"}, true
	default:
		return Mark{}, false
	}
}

func joinText(d *event.Join) string {
	if d.OK {
		return "joined " + strconv.Itoa(len(d.Succeeded)) + " branches"
	}

	return "join failed: " + strconv.Itoa(len(d.Failed)) + " branches failed"
}

func spanID(key string, gen, attempt int) string {
	return key + "/" + strconv.Itoa(gen) + "/" + strconv.Itoa(attempt)
}
