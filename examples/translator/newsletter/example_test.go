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
	"io"
	"strings"
	"testing"

	"github.com/nats-io/nats.go"

	"github.com/henomis/stiggy/examples/internal/extest"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// translate stands in for the translator offline: it tags the text with the
// language asked for. A prompt reads "Translate into <language>[, ...]:\n<text>".
func translate(prompt string) string {
	head, text, _ := strings.Cut(prompt, "\n")

	_, lang, ok := strings.Cut(head, "into ")
	if !ok {
		return text
	}

	lang = strings.TrimSuffix(lang, ":")
	lang, _, _ = strings.Cut(lang, ",")

	return "[" + lang + "] " + strings.TrimSpace(text)
}

func TestExample(t *testing.T) {
	model := spec.Model{Instance: stiggytest.Reply(func(string) string {
		return "Version 2 is out! Faster runs and a brand-new designer."
	})}

	translator := stiggytest.Reply(translate)

	if extest.Live() {
		model = newModel()
		translator = model.Instance
	}

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		// The translator program is another process; here a stand-in agent
		// takes its place on the bus, under the same owner and name.
		extest.Agent(t, nc, "translation", "translator",
			"Translate faithfully, keeping the tone and the formatting. Answer with the translation only.", translator)

		return run(ctx, nc, model, out)
	}, func(out string) {
		if !strings.Contains(out, ": completed") || !strings.Contains(out, "newsletter: [Spanish] Version 2 is out!") {
			t.Fatalf("output:\n%s", out)
		}
	})
}
