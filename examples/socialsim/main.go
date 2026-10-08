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
// The network lives in packtrail's long-term store, shared by every worker,
// reached through stiggy.Job.Store. After each round a tick activity copies
// the new posts into the timeline channel, so the execution's history holds
// every post.
//
//	nats-server -js &
//	go run ./examples/socialsim
package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/event"
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
    # Small models may call tools again after posting; post says the turn
    # is over, and this leaves room for a second try.
    max_iterations: 10
    # One memory per cast member and execution: each remembers its own posts.
    # It lives in NATS KV, so a member's next round may run on any worker.
    memory: {type: nats, bucket: socialsim-personas, session: "persona-{{.index}}", per_execution: true}
  analyst:
    model: main
    role: Analyst
    goal: Explain how opinion moved during a social simulation, citing who said what.
flows:
  simulate:
    # breaking is news injected from outside while the simulation runs:
    # stiggy update <exec> -writes '{"breaking": "..."}', or a fork with writes.
    channels: {timeline: {reducer: append}, breaking: {}}
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
          {{with .channels.breaking}}Breaking news: {{.}}
          {{end}}Read the feed, then post one message with the post tool: react to the news or reply to someone.
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
          {{with .channels.breaking}}Breaking news during the simulation: {{.}}
          {{end}}Timeline of the simulation: {{json .channels.timeline}}
          In three sentences: how did opinion move, and who moved it?
`

// post is one message of the timeline: ID is its key in the store.
type post struct {
	ID     string `json:"id"`
	Round  int    `json:"round,omitempty"`
	Author string `json:"author"`
	Text   string `json:"text"`
}

// stored is a post in the world; ID is its key, not stored.
type stored struct {
	ID     string    `json:"-"`
	Author string    `json:"author"`
	Text   string    `json:"text"`
	At     time.Time `json:"at"`
}

// The world (the social network) has two parts. Past rounds are the
// execution's timeline channel, so a fork or a rerun sees its own history.
// The current round lives in packtrail's long-term store, shared by every
// worker: one namespace per execution, one key per post, until the tick
// moves it into the timeline. The key comes from the posting step
// (stiggy.Job.StepID), so a retried step replaces its own post instead of
// adding one: delivery is at least once, and a tool with side effects needs
// such a key. A real deployment would also delete an execution's namespace
// once it ends.
func feedNS(execID string) string { return "feed-" + execID }

// postKey turns a step id into a store key (letters, digits, - and _).
func postKey(stepID string) string {
	sum := sha256.Sum256([]byte(stepID))

	return hex.EncodeToString(sum[:12])
}

// feed returns an execution's posts, oldest first.
func feed(ctx context.Context, store *worker.Store, execID string) ([]stored, error) {
	keys, err := store.Keys(ctx, feedNS(execID))
	if err != nil {
		return nil, err
	}

	posts := make([]stored, 0, len(keys))

	for _, k := range keys {
		raw, gerr := store.Get(ctx, feedNS(execID), k)
		if gerr != nil {
			return nil, gerr
		}

		p := stored{ID: k}
		if err = json.Unmarshal(raw, &p); err != nil {
			return nil, err
		}

		posts = append(posts, p)
	}

	slices.SortStableFunc(posts, func(a, b stored) int { return a.At.Compare(b.At) })

	return posts, nil
}

// fresh returns the store's posts that the timeline does not hold yet: the
// current round's.
func fresh(ctx context.Context, store *worker.Store, execID string, timeline []post) ([]stored, error) {
	posts, err := feed(ctx, store, execID)
	if err != nil {
		return nil, err
	}

	ticked := make(map[string]bool, len(timeline))
	for _, p := range timeline {
		ticked[p.ID] = true
	}

	return slices.DeleteFunc(posts, func(p stored) bool { return ticked[p.ID] }), nil
}

// errNoJob is returned by a world tool called outside a flow step.
var errNoJob = errors.New("socialsim: the world's tools run only inside a flow step")

// author returns the cast member a tool call runs for. It comes from the
// job, never from the model's arguments.
func author(ctx context.Context) (string, stiggy.Job, error) {
	job, ok := stiggy.JobFromContext(ctx)
	if !ok || job.Store == nil {
		return "", job, errNoJob
	}

	var who struct{ Name string }
	if err := job.DecodeItem(&who); err != nil {
		return "", job, fmt.Errorf("socialsim: who is posting: %w", err)
	}

	return who.Name, job, nil
}

func worldTools() ([]*llm.Tool, error) {
	readFeed, err := llm.NewTool("read_feed", "Read the latest posts on the network.",
		func(ctx context.Context, _ *struct{}) (string, error) {
			_, job, err := author(ctx)
			if err != nil {
				return "", err
			}

			// Past rounds from the execution, this round from the store.
			var timeline []post
			if err = job.DecodeChannel("timeline", &timeline); err != nil && !errors.Is(err, stiggy.ErrNoJobValue) {
				return "", err
			}

			current, err := fresh(ctx, job.Store, job.ExecID, timeline)
			if err != nil {
				return "", err
			}

			lines := make([]string, 0, len(timeline)+len(current))
			for _, p := range timeline {
				lines = append(lines, p.Author+": "+p.Text)
			}

			for _, p := range current {
				lines = append(lines, p.Author+": "+p.Text)
			}

			if len(lines) == 0 {
				return "The feed is empty: nobody has posted yet.", nil
			}

			var b strings.Builder
			for _, l := range lines[max(0, len(lines)-20):] {
				b.WriteString(l + "\n")
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

			ns, key := feedNS(job.ExecID), postKey(job.StepID)
			p := stored{Author: name, Text: text, At: time.Now()}

			// A replaced post keeps its place in the feed.
			if raw, gerr := job.Store.Get(ctx, ns, key); gerr == nil {
				var old stored
				if json.Unmarshal(raw, &old) == nil {
					p.At = old.At
				}
			}

			if err = job.Store.Put(ctx, ns, key, p); err != nil {
				return "", err
			}

			return "Posted. Your turn this round is over: do not call any more tools.", nil
		})
	if err != nil {
		return nil, err
	}

	return []*llm.Tool{readFeed, postTool}, nil
}

// tick moves the posts of the round into the timeline, stamped with the
// round. It skips the posts the timeline already holds, so a retried tick
// returns the same posts.
func tick(ctx context.Context, job *worker.Job) (*worker.Result, error) {
	var timeline []post
	if raw := job.Context.Channels["timeline"]; len(raw) > 0 {
		if err := json.Unmarshal(raw, &timeline); err != nil {
			return nil, worker.Permanent(err)
		}
	}

	current, err := fresh(ctx, job.Store(), job.ExecID, timeline)
	if err != nil {
		return nil, err
	}

	round := job.Context.Visits["round"]
	posts := make([]post, 0, len(current))

	for _, p := range current {
		posts = append(posts, post{ID: p.ID, Round: round, Author: p.Author, Text: p.Text})
	}

	return &worker.Result{Output: map[string]any{"posts": posts}}, nil
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

func run(ctx context.Context, nc *nats.Conn, out io.Writer) error {
	f, src, err := config.Parse([]byte(fleet))
	if err != nil {
		return fmt.Errorf("%s", strings.Join(src.Describe(err), "\n"))
	}

	tools, err := worldTools()
	if err != nil {
		return err
	}

	model, desc := exutil.Model(stiggytest.Model(fake))
	fmt.Fprintln(out, "model:", desc)

	app, err := stiggy.New(nc, stiggy.WithFleet(f), stiggy.WithModelSpec("main", model),
		stiggy.WithTool("world", tools...), stiggy.WithActivity("tick", tick), stiggy.WithLogger(exutil.Quiet()))
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

	if err = printRun(out, st, true); err != nil {
		return err
	}

	// What if? Branch the simulation right after round 1, with breaking news
	// written into the fork's state. The original is untouched; the fork
	// replays nothing before the branch point and runs round 2 and the
	// report again.
	seq, err := afterNode(ctx, app.Client(), st.ExecID, "tick")
	if err != nil {
		return err
	}

	forkID, err := app.Client().Fork(ctx, st.ExecID, seq, packtrail.WithForkWrites(map[string]any{
		"breaking": "A study finds car-free centres raised local shop sales by 20%.",
	}))
	if err != nil {
		return err
	}

	fork, err := app.Client().Wait(ctx, forkID)
	if err != nil {
		return err
	}

	fmt.Fprintf(out, "\nwhat if, after round 1: %s (fork %s: %s)\n", "breaking news", forkID, fork.Status)

	if fork.Status != packtrail.StatusCompleted {
		return fmt.Errorf("fork %s: %s %s", forkID, fork.Reason, fork.Error)
	}

	return printRun(out, fork, false)
}

// afterNode returns the sequence right after node first completed.
func afterNode(ctx context.Context, c *packtrail.Client, execID, node string) (uint64, error) {
	evs, err := c.History(ctx, execID)
	if err != nil {
		return 0, err
	}

	for _, ev := range evs {
		if d, ok := ev.Data.(*event.NodeDone); ok && ev.Type == event.NodeCompleted && d.Node == node {
			return ev.Seq, nil
		}
	}

	return 0, fmt.Errorf("socialsim: %s never completed in %s", node, execID)
}

// printRun prints a simulation's timeline and report, and its cast when
// asked.
func printRun(out io.Writer, st *packtrail.State, cast bool) error {
	var res struct {
		Cast     []struct{ Name, Stance string }
		Timeline []post
		Report   string
	}
	if err := st.DecodeOutput(&res); err != nil {
		return err
	}

	if cast {
		fmt.Fprintln(out, "cast:")

		for _, p := range res.Cast {
			fmt.Fprintf(out, " - %s (%s)\n", p.Name, p.Stance)
		}
	}

	fmt.Fprintf(out, "timeline (%d posts):\n", len(res.Timeline))

	for _, p := range res.Timeline {
		fmt.Fprintf(out, " [round %d] %s: %s\n", p.Round, p.Author, p.Text)
	}

	fmt.Fprintln(out, "report:", res.Report)

	return nil
}
