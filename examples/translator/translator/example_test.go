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
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy/examples/internal/extest"
	"github.com/henomis/stiggy/spec"
	"github.com/henomis/stiggy/stiggytest"
)

// prompt runs the translator program, prompts it the way any NATS Agent
// Protocol client would, then stops it.
func prompt(ctx context.Context, nc *nats.Conn, model spec.Model, out io.Writer) (err error) {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)

	go func() { done <- run(ctx, nc, model, out) }()

	defer func() {
		cancel()

		if rerr := <-done; err == nil {
			err = rerr
		}
	}()

	translator, err := discover(ctx, nc)
	if err != nil {
		return err
	}

	stream, err := translator.Prompt(ctx, "Translate into French:\nGood morning, how are you?")
	if err != nil {
		return err
	}
	defer stream.Close()

	answer, err := stream.Text(ctx)
	if err != nil {
		return err
	}

	fmt.Fprintln(out, "answer:", answer)

	return nil
}

// discover waits until the translator is on the bus.
func discover(ctx context.Context, nc *nats.Conn) (*natsagent.AgentHandle, error) {
	for {
		agents, err := natsagent.NewClient(nc).Discover(ctx, natsagent.FilterByOwner("translation"))

		switch {
		case err == nil && len(agents) > 0:
			return agents[0], nil
		case err != nil && !errors.Is(err, natsagent.ErrNoAgentsFound):
			return nil, err
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// scripted translates offline: it tags the text with the language asked
// for. A prompt reads "Translate into <language>[, ...]:\n<text>".
func scripted(prompt string) string {
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
	model := spec.Model{Instance: stiggytest.Reply(scripted)}
	if extest.Live() {
		model = newModel()
	}

	extest.Run(t, func(ctx context.Context, nc *nats.Conn, out io.Writer) error {
		return prompt(ctx, nc, model, out)
	}, func(out string) {
		if !strings.Contains(out, "serving translation/translator") ||
			!strings.Contains(out, "answer: [French] Good morning, how are you?") {
			t.Fatalf("output:\n%s", out)
		}
	})
}
