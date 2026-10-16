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
	"fmt"
	"io"
	"regexp"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy/examples/internal/extest"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

var (
	rolePattern  = regexp.MustCompile(`Role: (.+)`)
	roundPattern = regexp.MustCompile(`Round (\d+)`)
)

// toolTurns counts the tool results of the current run: those after the
// last user message (earlier ones come from the persona's memory).
func toolTurns(msgs []llm.Message) int {
	n := 0

	for _, m := range msgs {
		switch m.Role {
		case llm.RoleUser:
			n = 0
		case llm.RoleTool:
			n++
		}
	}

	return n
}

// fake scripts the three agents: the director casts three people, each
// persona reads the feed and posts, the analyst sums up.
func fake(r stiggytest.Request) stiggytest.Response {
	switch turns := toolTurns(r.Messages); {
	case r.Schema:
		return stiggytest.Response{Text: `{"personas": [
			{"name": "Ada", "bio": "A cyclist and nurse.", "stance": "for the ban"},
			{"name": "Bo", "bio": "A taxi driver.", "stance": "against the ban"},
			{"name": "Cy", "bio": "A shop owner downtown.", "stance": "undecided"}]}`}
	case strings.Contains(r.System, "Role: Analyst") && strings.Contains(r.Prompt, "Breaking news"):
		return stiggytest.Response{Text: "The sales study moved Bo and Cy towards the ban in round 2."}
	case strings.Contains(r.System, "Role: Analyst"):
		return stiggytest.Response{Text: "Ada and Bo held their ground; Cy moved towards Ada after round 1."}
	case turns == 0:
		return stiggytest.Response{Calls: []stiggytest.Call{{Tool: "read_feed", Args: map[string]any{}}}}
	case turns == 1:
		name, round := "someone", "?"
		if m := rolePattern.FindStringSubmatch(r.System); m != nil {
			name = m[1]
		}

		if m := roundPattern.FindStringSubmatch(r.Prompt); m != nil {
			round = m[1]
		}

		take := "take"
		if strings.Contains(r.Prompt, "Breaking news") {
			take = "take on the study"
		}

		return stiggytest.Response{Calls: []stiggytest.Call{{
			Tool: "post", Args: map[string]any{"text": fmt.Sprintf("%s's %s, round %s.", name, take, round)},
		}}}
	default:
		return stiggytest.Response{Text: "Posted."}
	}
}

func TestExample(t *testing.T) {
	model := spec.Model{Instance: stiggytest.Model(fake)}
	if extest.Live() {
		model = newModel()
	}

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		return run(ctx, nc, model, out)
	}, func(out string) {
		for _, w := range []string{
			"timeline (6 posts):",
			"[round 1] Ada: Ada's take, round 1.",
			"[round 1] Bo: Bo's take, round 1.",
			"[round 2] Cy: Cy's take, round 2.",
			"report: Ada and Bo held their ground",
			// The fork keeps round 1 and reruns round 2 with the news.
			"what if, after round 1: breaking news",
			"[round 2] Bo: Bo's take on the study, round 2.",
			"report: The sales study moved Bo and Cy",
		} {
			if !strings.Contains(out, w) {
				t.Fatalf("missing %q in output:\n%s", w, out)
			}
		}
	})
}
