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

// Command socialsim is a small social simulation, in the spirit of
// MiroFish: a director casts people from a news item, they react on a tiny
// social network for a few rounds, and an analyst explains how opinion
// moved.
//
// One persona agent plays every member of the cast. Per map item it gets:
//   - its identity, from a templated system prompt (role: "{{.item.name}}");
//   - its own memory, from a templated session ("persona-{{.index}}"), in
//     NATS KV so it follows the member from worker to worker;
//   - its own voice on the network: the world's post tool reads the caller
//     from stiggy.JobFromContext, so a model cannot post as someone else.
//
// The network lives in this process (a real deployment keeps it in a store
// the tools share). After each round a tick activity copies the new posts
// into the timeline channel, so the execution's history holds every post.
//
//	nats-server -js &
//	go run ./examples/socialsim
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/examples/internal/exutil"
	"github.com/henomis/stiggy/stiggytest"
)

const fleet = `
namespace: example-socialsim
agents:
  director:
    model: main
    role: Casting director
    goal: Turn a news item into a cast of ordinary people who would react to it online.
  persona:
    model: main
    role: "{{.item.name}}"
    backstory: "{{.item.bio}} Your view on the news: {{.item.stance}}"
    goal: Take part in a small social network, in character. Keep posts under 40 words.
    tools: [world]
    max_iterations: 6
    # One memory per cast member and execution: each remembers its own posts.
    # It lives in NATS KV, so a member's next round may run on any worker.
    memory: {type: nats, bucket: socialsim-personas, session: "persona-{{.index}}", per_execution: true}
  analyst:
    model: main
    role: Analyst
    goal: Explain how opinion moved during a social simulation, citing who said what.
flows:
  simulate:
    channels: {timeline: {reducer: append}}
    output: "{cast: results.cast.personas, timeline: channels.timeline, report: results.report.text}"
    nodes:
      - id: cast
        type: task
        agent: director
        prompt: |
          News: {{.input.news}}
          Create {{.input.size}} people with clearly different views on it.
        output_schema:
          type: object
          properties:
            personas:
              type: array
              items:
                type: object
                properties: {name: {type: string}, bio: {type: string}, stance: {type: string}}
                required: [name, bio, stance]
          required: [personas]
        # A real model sometimes misses the schema: retry the step.
        retry: {max_attempts: 3, delay: 1s}
        next: round
      - id: round
        type: map
        over: results.cast.personas
        max_parallel: 4
        agent: persona
        prompt: |
          Round {{.visits.round}}. The news: {{.input.news}}
          Read the feed, then post one message with the post tool: react to the news or reply to someone.
          Finish by answering with the text you posted.
        retry: {max_attempts: 3, delay: 1s}
        next: tick
      - id: tick
        type: task
        activity: tick
        writes: {timeline: output.posts}
        next: more
      - id: more
        type: choice
        rules:
          - {when: 'visits["round"] < input.rounds', to: round}
          - {default: true, to: report}
      - id: report
        type: task
        agent: analyst
        prompt: |
          News: {{.input.news}}
          Timeline of the simulation: {{json .channels.timeline}}
          In three sentences: how did opinion move, and who moved it?
`

// post is one message on the network.
type post struct {
	Round  int    `json:"round,omitempty"`
	Author string `json:"author"`
	Text   string `json:"text"`
	// step is the step that posted it (stiggy.Job.StepID).
	step string
}

// world is the social network: the posts of each execution, in order.
type world struct {
	mu    sync.Mutex
	feeds map[string][]post
}

// errNoJob is returned by a world tool called outside a flow step.
var errNoJob = errors.New("socialsim: the world's tools run only inside a flow step")

// author returns the cast member a tool call runs for. It comes from the
// job, never from the model's arguments.
func author(ctx context.Context) (string, stiggy.Job, error) {
	job, ok := stiggy.JobFromContext(ctx)
	if !ok {
		return "", job, errNoJob
	}

	var who struct{ Name string }
	if err := job.DecodeItem(&who); err != nil {
		return "", job, fmt.Errorf("socialsim: who is posting: %w", err)
	}

	return who.Name, job, nil
}

func (w *world) tools() ([]*llm.Tool, error) {
	readFeed, err := llm.NewTool("read_feed", "Read the latest posts on the network.",
		func(ctx context.Context, _ *struct{}) (string, error) {
			_, job, err := author(ctx)
			if err != nil {
				return "", err
			}

			w.mu.Lock()
			defer w.mu.Unlock()

			feed := w.feeds[job.ExecID]
			if len(feed) == 0 {
				return "The feed is empty: nobody has posted yet.", nil
			}

			var b strings.Builder
			for _, p := range feed[max(0, len(feed)-20):] {
				fmt.Fprintf(&b, "%s: %s\n", p.Author, p.Text)
			}

			return b.String(), nil
		})
	if err != nil {
		return nil, err
	}

	type postInput struct {
		Text string `json:"text" jsonschema:"The message to post, in character."`
	}

	postTool, err := llm.NewTool("post", "Post a message on the network, as yourself.",
		func(ctx context.Context, in *postInput) (string, error) {
			name, job, err := author(ctx)
			if err != nil {
				return "", err
			}

			text := strings.TrimSpace(in.Text)
			if text == "" {
				return "", errors.New("the post is empty")
			}

			w.mu.Lock()
			defer w.mu.Unlock()

			// One post per step: a retried step (or a model that posts
			// twice) replaces its post instead of adding one. Delivery is at
			// least once, so a tool with side effects needs such a key.
			feed := w.feeds[job.ExecID]
			for i := range feed {
				if feed[i].step == job.StepID {
					feed[i].Text = text

					return "Posted (your earlier post this round was replaced).", nil
				}
			}

			w.feeds[job.ExecID] = append(feed, post{Author: name, Text: text, step: job.StepID})

			return "Posted.", nil
		})
	if err != nil {
		return nil, err
	}

	return []*llm.Tool{readFeed, postTool}, nil
}

// tick copies the posts made since the last tick into the timeline,
// stamped with their round. It counts what the timeline already holds, so a
// retried tick returns the same posts.
func (w *world) tick(_ context.Context, job *worker.Job) (*worker.Result, error) {
	var timeline []post
	if raw := job.Context.Channels["timeline"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &timeline); err != nil {
			return nil, worker.Permanent(err)
		}
	}

	round := job.Context.Visits["round"]

	w.mu.Lock()
	feed := w.feeds[job.ExecID]
	fresh := make([]post, 0, max(0, len(feed)-len(timeline)))

	for _, p := range feed[min(len(timeline), len(feed)):] {
		p.Round = round
		fresh = append(fresh, p)
	}
	w.mu.Unlock()

	return &worker.Result{Output: map[string]any{"posts": fresh}}, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	nc, err := exutil.Connect()
	if err != nil {
		log.Fatal(err)
	}
	defer nc.Close()

	if err = run(ctx, nc, os.Stdout); err != nil {
		log.Fatal(err)
	}
}

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

		return stiggytest.Response{Calls: []stiggytest.Call{{
			Tool: "post", Args: map[string]any{"text": fmt.Sprintf("%s's take, round %s.", name, round)},
		}}}
	default:
		return stiggytest.Response{Text: "Posted."}
	}
}

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	f, src, err := config.Parse([]byte(fleet))
	if err != nil {
		return fmt.Errorf("%s", strings.Join(src.Describe(err), "\n"))
	}

	w := &world{feeds: map[string][]post{}}

	tools, err := w.tools()
	if err != nil {
		return err
	}

	model, desc := exutil.Model(stiggytest.Model(fake))
	fmt.Fprintln(out, "model:", desc)

	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModelSpec("main", model),
		stiggy.WithTool("world", tools...), stiggy.WithActivity("tick", w.tick), stiggy.WithLogger(exutil.Quiet()))
	if err != nil {
		return err
	}

	stopApp, err := exutil.Serve(ctx, app)
	if err != nil {
		return err
	}
	defer stopApp() //nolint:errcheck // shutdown at the end of the example

	st, err := exutil.RunFlow(ctx, app, "simulate", map[string]any{
		"news":   "The city will ban cars from the historic centre next spring.",
		"size":   3,
		"rounds": 2,
	}, out)
	if err != nil {
		return err
	}

	var res struct {
		Cast     []struct{ Name, Stance string }
		Timeline []post
		Report   string
	}
	if err = st.DecodeOutput(&res); err != nil {
		return err
	}

	fmt.Fprintln(out, "cast:")

	for _, p := range res.Cast {
		fmt.Fprintf(out, " - %s (%s)\n", p.Name, p.Stance)
	}

	fmt.Fprintf(out, "timeline (%d posts):\n", len(res.Timeline))

	for _, p := range res.Timeline {
		fmt.Fprintf(out, " [round %d] %s: %s\n", p.Round, p.Author, p.Text)
	}

	fmt.Fprintln(out, "report:", res.Report)

	return nil
}
