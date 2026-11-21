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
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/henomis/packtrail/event"
	"github.com/henomis/packtrail/worker"
)

func recordJSON(t *testing.T, r Record) json.RawMessage {
	t.Helper()

	b, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}

	return b
}

func oneCall(msgs ...RecordMessage) Record {
	return Record{Version: 1, Level: "prompt", RecordRun: RecordRun{Agent: "a", Calls: []RecordCall{{Messages: msgs}}}}
}

// TestInputPreviewOf checks the step prompt is the last user message of the
// first call, cut to inputMax on a rune boundary, and what is said when the
// record lost it.
func TestInputPreviewOf(t *testing.T) {
	marker, err := json.Marshal(worker.RecordTruncated{Truncated: true, Size: 9000, Head: `{"version":1,`})
	if err != nil {
		t.Fatal(err)
	}

	long := strings.Repeat("é", 400) // 800 bytes

	remote := oneCall(RecordMessage{Role: "user", Text: "ask the analyst"})
	remote.Via, remote.Remote = "remote", "ops/analyst"

	cases := []struct {
		name string
		rec  json.RawMessage
		want InputPreview
	}{
		{"agent", recordJSON(t, oneCall(RecordMessage{Role: "user", Text: "write about ants"})),
			InputPreview{Text: "write about ants", Size: 16}},
		{"remote", recordJSON(t, remote), InputPreview{Text: "ask the analyst", Size: 15}},
		{"memory", recordJSON(t, oneCall(
			RecordMessage{Role: "user", Text: "earlier"}, RecordMessage{Role: "assistant", Text: "reply"},
			RecordMessage{Role: "user", Text: "now this"},
		)), InputPreview{Text: "now this", Size: 8, Memory: 2}},
		{"long", recordJSON(t, oneCall(RecordMessage{Role: "user", Text: long})),
			InputPreview{Text: strings.Repeat("é", 300), Size: 800, Cut: true}},
		{"cut message", recordJSON(t, oneCall(RecordMessage{Role: "user", Text: "the sta", Truncated: true})),
			InputPreview{Text: "the sta", Size: 7, Cut: true}},
		{"message cut away", recordJSON(t, oneCall(RecordMessage{Role: "user", Truncated: true})),
			InputPreview{Missing: MissingNotKept}},
		{"calls left out", recordJSON(t, Record{Version: 1, Truncated: true, RecordRun: RecordRun{Agent: "a"}}),
			InputPreview{Missing: MissingNotKept}},
		{"too large", marker, InputPreview{Missing: MissingTooLarge}},
	}

	for _, c := range cases {
		got := InputPreviewOf(c.rec)
		if got == nil || *got != c.want {
			t.Errorf("%s: %+v, want %+v", c.name, got, c.want)
		}
	}

	if got := InputPreviewOf(nil); got != nil {
		t.Errorf("no record: %+v", got)
	}

	// A cut in the middle of a rune keeps the text valid.
	if got := InputPreviewOf(recordJSON(t, oneCall(RecordMessage{Role: "user", Text: "x" + long}))); !utf8.ValidString(got.Text) ||
		len(got.Text) != 599 {
		t.Errorf("rune boundary: %d bytes, valid %v", len(got.Text), utf8.ValidString(got.Text))
	}
}

// TestStoryInput checks the story gives recorded attempts their input,
// reading each record once through the previewer.
func TestStoryInput(t *testing.T) {
	rec := recordJSON(t, oneCall(RecordMessage{Role: "user", Text: "write"}))
	at := time.Unix(0, 0)
	evs := []event.Event{
		{Time: at, Data: &event.Scheduled{Key: "w", Node: "w", Generation: 1, Attempt: 1}},
		{Time: at, Data: &event.NodeFail{Key: "w", Node: "w", Generation: 1, Attempt: 1, Record: rec, WillRetry: true}},
		{Time: at, Data: &event.Scheduled{Key: "w", Node: "w", Generation: 1, Attempt: 2}},
		{Time: at, Data: &event.NodeDone{Key: "w", Node: "w", Generation: 1, Attempt: 2, Record: rec}},
		{Time: at, Data: &event.Scheduled{Key: "a", Node: "a", Generation: 1, Attempt: 1}},
		{Time: at, Data: &event.NodeDone{Key: "a", Node: "a", Generation: 1, Attempt: 1}},
	}

	s := &Server{}
	preview := s.previews("ns", "x")

	story := storyOf(evs, nil, preview)
	if len(story) != 3 || story[0].Input == nil || story[0].Input.Text != "write" || story[1].Input == nil ||
		story[2].Input != nil || story[2].Recorded {
		t.Fatalf("story = %+v", story)
	}

	if len(s.preview.m) != 2 {
		t.Fatalf("cache keeps %d previews, want 2", len(s.preview.m))
	}

	// Read again, the kept preview is used.
	s.preview.put("ns\x00x\x00w\x001\x002", &InputPreview{Text: "kept"})

	if story = storyOf(evs, nil, preview); story[1].Input.Text != "kept" {
		t.Fatalf("second read: %+v", story[1].Input)
	}
}

// TestCacheBound checks a bounded cache starts over past its size.
func TestCacheBound(t *testing.T) {
	c := cache[int]{max: 2}
	c.put("a", 1)
	c.put("b", 2)
	c.put("c", 3)

	if _, ok := c.get("a"); ok || len(c.m) != 1 {
		t.Fatalf("cache = %v", c.m)
	}
}
