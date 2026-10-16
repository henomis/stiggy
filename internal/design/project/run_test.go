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

package project_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/internal/design/project"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// TestRunStory runs a compiled project for real: the story workflow plans,
// researches each angle in a loop, writes, checks, waits for a person's
// approval and returns its output.
func TestRunStory(t *testing.T) {
	res, err := project.Compile(load(t, "testdata/story.stiggy.json"))
	if err != nil || len(res.Problems) > 0 {
		t.Fatalf("compile: %v %+v", err, res.Problems)
	}

	f, _, err := config.Parse(res.YAML, config.WithoutEnv())
	if err != nil {
		t.Fatal(err)
	}

	fake := stiggytest.Model(func(r stiggytest.Request) stiggytest.Response {
		switch {
		case r.Schema && strings.Contains(r.System, "editor"):
			return stiggytest.Response{Text: `{"angles": ["history", "today"]}`}
		case r.Schema:
			return stiggytest.Response{Text: `{"ok": true, "issues": ""}`}
		case strings.Contains(r.System, "researcher"):
			return stiggytest.Response{Text: "notes on " + r.Prompt}
		default:
			return stiggytest.Response{Text: "the article"}
		}
	})

	for name := range f.Models {
		f.Models[name] = spec.Model{Instance: fake}
	}

	h := stiggytest.Start(t, stiggy.WithFleet(f))

	ctx, cancel := context.WithTimeout(context.Background(), stiggytest.Timeout)
	defer cancel()

	id, err := h.Client.Start(ctx, "story", map[string]any{"topic": "NATS"})
	if err != nil {
		t.Fatal(err)
	}

	// The approval is buffered until the workflow reaches it.
	if err = h.Client.Signal(ctx, id, "sign_off", map[string]any{"approved": true}); err != nil {
		t.Fatal(err)
	}

	st, err := h.Client.Wait(ctx, id)
	if err != nil {
		t.Fatal(err)
	}

	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status %s: %+v", st.Status, st)
	}

	var out map[string]any
	if err = json.Unmarshal(st.Output, &out); err != nil {
		t.Fatal(err)
	}

	if out["article"] != "the article" || out["topic"] != "NATS" || out["status"] != "approved" {
		t.Fatalf("output %s", st.Output)
	}

	research, ok := st.Results["research"]
	if !ok || !strings.Contains(string(research), "history") || !strings.Contains(string(research), "today") {
		t.Fatalf("the loop did not run once per angle: %s", research)
	}
}
