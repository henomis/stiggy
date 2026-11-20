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
	"testing"
	"time"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/event"
	"github.com/henomis/packtrail/flow"
)

func TestUsageOf(t *testing.T) {
	u := UsageOf(map[string]float64{
		"tokens_in": 30, "tokens_out": 12, "cost_usd": 0.5, "tool_calls": 2,
		"tokens_in__agent__writer": 20, "cost_usd__agent__writer": 0.4,
		"tokens_in__agent__critic": 10, "cost_usd__agent__critic": 0.1,
		"tokens_in__model__gpt-4o": 30,
	})

	if u.TokensIn != 30 || u.TokensOut != 12 || u.CostUSD != 0.5 || u.ToolCalls != 2 {
		t.Fatalf("totals = %+v", u)
	}

	if len(u.ByAgent) != 2 || u.ByAgent[0].Name != "writer" || u.ByAgent[0].TokensIn != 20 {
		t.Fatalf("by agent = %+v", u.ByAgent)
	}

	if len(u.ByModel) != 1 || u.ByModel[0].Name != "gpt-4o" {
		t.Fatalf("by model = %+v", u.ByModel)
	}

	u.Add(UsageOf(map[string]float64{"tokens_in": 5, "tokens_in__agent__critic": 5, "cost_usd__agent__critic": 0.4}))

	if u.TokensIn != 35 || u.ByAgent[0].Name != "critic" || u.ByAgent[0].CostUSD != 0.5 {
		t.Fatalf("sum = %+v", u)
	}
}

func TestFieldsOf(t *testing.T) {
	fs := FieldsOf(json.RawMessage(`{"topic": "ants", "n": 3, "tags": ["a", "b"], "meta": {"fine": true}}`))

	want := []struct{ key, kind, text string }{
		{"meta", kindObject, "1 field"}, {"n", kindNumber, "3"}, {"tags", kindList, "2 items"}, {"topic", kindText, "ants"},
	}

	if len(fs) != len(want) {
		t.Fatalf("fields = %+v", fs)
	}

	for i, w := range want {
		if fs[i].Key != w.key || fs[i].Kind != w.kind || fs[i].Text != w.text {
			t.Errorf("field %d = %+v, want %+v", i, fs[i], w)
		}
	}

	if fs[0].Items[0].Key != "fine" || fs[2].Items[1].Text != "b" {
		t.Fatalf("nested = %+v", fs)
	}

	if FieldsOf(nil) != nil || FieldsOf(json.RawMessage("null")) != nil {
		t.Fatal("empty values give fields")
	}

	if v := FieldsOf(json.RawMessage(`"plain"`)); len(v) != 1 || v[0].Key != "value" || v[0].Text != "plain" {
		t.Fatalf("scalar = %+v", v)
	}
}

func TestGraphOf(t *testing.T) {
	g := GraphOf(&flow.Flow{Name: "triage", Nodes: []flow.Node{
		{ID: "classify", Type: flow.NodeTask, Next: "route", OnFailure: "fallback"},
		{ID: "route", Type: flow.NodeChoice, Rules: []flow.Rule{
			{Name: "urgent", When: "x", To: "page"}, {Default: true, To: flow.End},
		}},
		{ID: "page", Type: flow.NodeTask},
		{ID: "fallback", Type: flow.NodeTask},
	}})

	if g.Start != "classify" || len(g.Nodes) != 4 {
		t.Fatalf("graph = %+v", g)
	}

	want := map[string]string{"classify>route": EdgeNext, "classify>fallback": EdgeFailure, "route>page": EdgeRule}
	if len(g.Edges) != len(want) {
		t.Fatalf("edges = %+v", g.Edges)
	}

	for _, e := range g.Edges {
		if want[e.From+">"+e.To] != e.Kind {
			t.Errorf("edge %+v", e)
		}
	}
}

func TestGraphOfFanout(t *testing.T) {
	g := GraphOf(&flow.Flow{Name: "brief", Nodes: []flow.Node{
		{ID: "gather", Type: flow.NodeFanout, Branches: []string{"a", "b"}, Next: "merge"},
		{ID: "a", Type: flow.NodeTask},
		{ID: "b", Type: flow.NodeTask},
		{ID: "merge", Type: flow.NodeJoin},
	}})

	got := map[string]bool{}
	for _, e := range g.Edges {
		got[e.From+">"+e.To] = true
	}

	for _, want := range []string{"gather>a", "gather>b", "a>merge", "b>merge"} {
		if !got[want] {
			t.Errorf("missing %s in %+v", want, g.Edges)
		}
	}

	if got["gather>merge"] {
		t.Errorf("fanout drawn straight to its join: %+v", g.Edges)
	}
}

func TestNodeStatuses(t *testing.T) {
	var st *packtrail.State

	err := json.Unmarshal([]byte(`{
		"visits": {"a": 1, "b": 2, "c": 1},
		"results": {"a": {}},
		"tasks": {
			"b": {"node": "b", "status": "retrying", "attempt": 2},
			"c": {"node": "c", "status": "interrupted", "interrupt": {
				"kind": "review", "agent": "w", "node": "c",
				"stiggy": {"pending": {"kind": "review", "text": "a draft", "route": "publish"}}
			}}
		}
	}`), &st)
	if err != nil {
		t.Fatal(err)
	}

	ns := NodeStatuses(st)

	if ns["a"].State != StateDone || ns["b"].State != StateRetry || ns["b"].Attempt != 2 ||
		ns["b"].Visits != 2 || ns["c"].State != StateWaiting {
		t.Fatalf("statuses = %+v", ns)
	}

	ws := WaitsOf(st)
	if len(ws) != 1 || ws[0].Node != "c" || ws[0].Kind != "review" || ws[0].Text != "a draft" || ws[0].Route != "publish" {
		t.Fatalf("waits = %+v", ws)
	}
}

func TestTimelineOf(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	at := func(s int) time.Time { return t0.Add(time.Duration(s) * time.Second) }

	tl := TimelineOf([]event.Event{
		{Type: event.ExecutionStarted, Time: at(0), Data: &event.Started{}},
		{Type: event.NodeScheduled, Time: at(1), Data: &event.Scheduled{Key: "a", Node: "a", Generation: 1, Attempt: 1}},
		{Type: event.NodeFailed, Time: at(2), Data: &event.NodeFail{Key: "a", Generation: 1, Attempt: 1, WillRetry: true}},
		{Type: event.NodeScheduled, Time: at(3), Data: &event.Scheduled{Key: "a", Node: "a", Generation: 1, Attempt: 2}},
		{Type: event.NodeInterrupted, Time: at(4), Data: &event.Interrupted{Key: "a", Node: "a", Generation: 1}},
		{Type: event.NodeScheduled, Time: at(9), Data: &event.Scheduled{
			Key: "a", Node: "a", Generation: 2, Attempt: 1, ResumedBy: "ann",
		}},
		{Type: event.NodeCompleted, Time: at(10), Data: &event.NodeDone{
			Key: "a", Generation: 2, Attempt: 1, Usage: map[string]float64{"tokens_in": 4, "tokens_out": 1},
		}},
		{Type: event.ExecutionCompleted, Time: at(11), Data: &event.Completed{}},
	})

	want := []struct {
		outcome    string
		start, end int
	}{{SpanRetry, 1, 2}, {SpanWaiting, 3, 4}, {SpanHuman, 4, 9}, {SpanDone, 9, 10}}

	if len(tl.Spans) != len(want) || !tl.Start.Equal(at(0)) || !tl.End.Equal(at(11)) {
		t.Fatalf("timeline = %+v", tl)
	}

	for i, w := range want {
		s := tl.Spans[i]
		if s.Outcome != w.outcome || !s.Start.Equal(at(w.start)) || !s.End.Equal(at(w.end)) {
			t.Errorf("span %d = %+v, want %+v", i, s, w)
		}
	}

	if tl.Spans[2].By != "ann" || tl.Spans[3].Tokens != 5 {
		t.Fatalf("spans = %+v", tl.Spans)
	}
}
