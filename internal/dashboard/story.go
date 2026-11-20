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
	"strconv"
	"time"

	"github.com/henomis/packtrail/event"
	"github.com/henomis/packtrail/flow"
)

// Story entry kinds.
const (
	// EntryStep is an attempt of a step: an agent's answer, an activity's
	// result, or a failure.
	EntryStep = "step"
	// EntryHuman is a human's reply to a step waiting for one.
	EntryHuman = "human"
	// EntryEvent is a line about the flow itself: a choice, a fan-out, a
	// signal.
	EntryEvent = "event"
	// EntryStart and EntryEnd open and close the story.
	EntryStart = "start"
	EntryEnd   = "end"
)

// Entry is one thing that happened in an execution, in order: who did it and
// what came of it.
type Entry struct {
	Kind string    `json:"kind"`
	Time time.Time `json:"time"`
	// End is when a step's attempt ended.
	End  time.Time `json:"end,omitzero"`
	Node string    `json:"node,omitempty"`
	// Who is the agent, activity or remote that ran the step, or the human
	// who replied; Role says which.
	Who  string `json:"who,omitempty"`
	Role string `json:"role,omitempty"`

	Visit   int    `json:"visit,omitempty"`
	Attempt int    `json:"attempt,omitempty"`
	Outcome string `json:"outcome,omitempty"`

	// Text is what a step put to a human (Asks: question or review), a
	// human's reply, or an event in words.
	Text string `json:"text,omitempty"`
	Asks string `json:"asks,omitempty"`
	// Output is what the step answered, or what the execution started with
	// or returned.
	Output []Field `json:"output,omitempty"`
	// Kept is set when the step kept the answer a human just approved,
	// shown above rather than repeated.
	Kept     bool   `json:"kept,omitempty"`
	Approved bool   `json:"approved,omitempty"`
	Next     string `json:"next,omitempty"`
	Error    string `json:"error,omitempty"`
	Usage    *Usage `json:"usage,omitempty"`
}

// storyteller reads events into a story.
type storyteller struct {
	story []Entry
	// who maps a node to what runs it.
	who map[string][2]string
	// visits numbers the visits; started remembers when each attempt
	// began.
	visits  *visitCounter
	gens    map[string]int
	started map[string]time.Time
	// asked is the text put to a human, by key, until the reply.
	asked map[string]string
	// approved is the text a human approved, by key, until the step ends.
	approved map[string]string
}

// StoryOf tells the execution evs from start to end, with def for who runs
// each step (def may be nil).
func StoryOf(evs []event.Event, def *flow.Flow) []Entry {
	s := &storyteller{
		story: []Entry{}, who: map[string][2]string{}, visits: newVisitCounter(), gens: map[string]int{},
		started: map[string]time.Time{}, asked: map[string]string{}, approved: map[string]string{},
	}

	if def != nil {
		for i := range def.Nodes {
			if name, role, _ := runnerOf(&def.Nodes[i]); name != "" {
				s.who[def.Nodes[i].ID] = [2]string{name, role}
			}
		}
	}

	for _, ev := range evs {
		s.add(ev)
	}

	return s.story
}

func (s *storyteller) add(ev event.Event) {
	switch d := ev.Data.(type) {
	case *event.Started:
		s.story = append(s.story, Entry{Kind: EntryStart, Time: ev.Time, Output: FieldsOf(d.Input)})
	case *event.Forked:
		s.story = append(s.story, Entry{
			Kind: EntryStart, Time: ev.Time,
			Text: "forked from " + d.From + " at event " + strconv.FormatUint(d.Seq, 10),
		})
	case *event.Entered:
		s.visits.enter(d.Node)
	case *event.Scheduled:
		s.scheduled(ev.Time, d)
	case *event.NodeDone:
		s.done(ev.Time, d)
	case *event.NodeFail:
		e := s.step(d.Key, d.Node, d.Generation, d.Attempt, ev.Time)
		e.Outcome, e.Error, e.Usage = SpanFailed, d.Error, usageOrNil(d.Usage)

		if d.WillRetry {
			e.Outcome = SpanRetry
		}

		s.story = append(s.story, e)
	case *event.Interrupted:
		s.interrupted(ev.Time, d)
	case *event.Completed:
		s.story = append(s.story, Entry{Kind: EntryEnd, Time: ev.Time, Outcome: SpanDone, Output: FieldsOf(d.Output)})
	case *event.Failed:
		s.story = append(s.story, Entry{
			Kind: EntryEnd, Time: ev.Time, Outcome: SpanFailed, Node: d.Node, Error: d.Error, Text: d.Reason,
		})
	case *event.Cancelled:
		s.story = append(s.story, Entry{Kind: EntryEnd, Time: ev.Time, Outcome: SpanCancel, Text: d.Reason})
	case *event.Update:
		s.story = append(s.story, Entry{Kind: EntryEvent, Time: ev.Time, Text: textUpdated})
	default:
		if m, ok := markOf(ev); ok {
			s.story = append(s.story, Entry{Kind: EntryEvent, Time: m.Time, Node: m.Node, Text: m.Text})
		}
	}
}

// step starts the entry of an attempt that just ended.
func (s *storyteller) step(key, node string, gen, attempt int, end time.Time) Entry {
	id := spanID(key, gen, attempt)

	return Entry{
		Kind: EntryStep, Time: s.started[id], End: end, Node: node,
		Who: s.who[node][0], Role: s.who[node][1],
		Visit: s.gens[key+"#"+strconv.Itoa(gen)], Attempt: attempt,
	}
}

func (s *storyteller) scheduled(at time.Time, d *event.Scheduled) {
	s.gens[d.Key+"#"+strconv.Itoa(d.Generation)] = s.visits.visit(d.Key, d.Node, d.Generation)

	s.started[spanID(d.Key, d.Generation, d.Attempt)] = at

	text, ok := s.asked[d.Key]
	if !ok || len(d.Resume) == 0 {
		return
	}

	delete(s.asked, d.Key)

	reply, approved := replyOf(d.Resume)
	if approved {
		s.approved[d.Key] = text
	}

	s.story = append(s.story, Entry{
		Kind: EntryHuman, Time: at, Node: d.Node, Who: d.ResumedBy, Role: EntryHuman,
		Text: reply, Approved: approved,
	})
}

func (s *storyteller) done(at time.Time, d *event.NodeDone) {
	e := s.step(d.Key, d.Node, d.Generation, d.Attempt, at)
	e.Outcome, e.Next, e.Output, e.Usage = SpanDone, d.Next, FieldsOf(d.Output), usageOrNil(d.Usage)

	if d.Cached {
		e.Outcome = SpanCached
	}

	if text, ok := s.approved[d.Key]; ok {
		delete(s.approved, d.Key)

		if answerText(d.Output) == text {
			e.Kept, e.Output = true, nil
		}
	}

	s.story = append(s.story, e)
}

func (s *storyteller) interrupted(at time.Time, d *event.Interrupted) {
	e := s.step(d.Key, d.Node, d.Generation, 0, at)

	// The attempt is the latest one started for this generation.
	for a := 1; ; a++ {
		t, ok := s.started[spanID(d.Key, d.Generation, a)]
		if !ok {
			break
		}

		e.Time, e.Attempt = t, a
	}

	e.Outcome, e.Usage, e.Asks = SpanWaiting, usageOrNil(d.Usage), waitQuestion

	var in interrupt
	if len(d.Payload) > 0 && json.Unmarshal(d.Payload, &in) == nil {
		e.Asks, e.Text = in.Kind, in.Question

		if p := in.Stiggy.Pending; p != nil {
			e.Asks, e.Text, e.Next = p.Kind, p.Text, p.Route
		}
	}

	if e.Asks == "" {
		e.Asks = waitQuestion
	}

	s.asked[d.Key] = e.Text
	s.story = append(s.story, e)
}

// answerText is the text of an agent's plain answer ({"text": ...}), or "".
func answerText(raw json.RawMessage) string {
	var v struct {
		Text string `json:"text"`
	}

	if json.Unmarshal(raw, &v) != nil {
		return ""
	}

	return v.Text
}

func usageOrNil(m map[string]float64) *Usage {
	if len(m) == 0 {
		return nil
	}

	u := UsageOf(m)

	return &u
}
