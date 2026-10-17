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
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/spec"
)

// opsRun runs an execution operation with its parsed arguments.
type opsRun func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error

// opsCommand is an execution operation: thin wrappers over packtrail's
// client, so operating a fleet does not need a second tool.
type opsCommand struct {
	args  string // positional arguments, for usage
	nargs int
	setup func(fs *flag.FlagSet) opsRun
}

// Positional arguments of the operations.
const (
	argExec     = "<exec>"
	argExecNode = "<exec> <node>"
)

// opsCommands are the execution operations, by name.
func opsCommands() map[string]opsCommand {
	return map[string]opsCommand{
		"get":      {argExec, 1, opsGet},
		"history":  {argExec, 1, opsHistory},
		"timeline": {argExec, 1, opsTimeline},
		"signal":   {"<exec> <name>", 2, opsSignal},
		"resume":   {argExecNode, 2, opsResume},
		"cancel":   {argExec, 1, opsCancel},
		"fork":     {"<exec> <seq>", 2, opsFork},
		"rerun":    {argExecNode, 2, opsRerun},
		"update":   {argExec, 1, opsUpdate},
		"progress": {argExec, 1, opsProgress},
	}
}

func opsGet(fs *flag.FlagSet) opsRun {
	seq := fs.Uint64("seq", 0, "state right after this event sequence (time travel)")

	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		var (
			st  *packtrail.State
			err error
		)

		if *seq > 0 {
			st, err = c.StateAt(ctx, pos[0], *seq)
		} else {
			st, err = c.Get(ctx, pos[0])
		}

		if err != nil {
			return err
		}

		return writeJSON(out, st)
	}
}

func opsHistory(_ *flag.FlagSet) opsRun {
	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		evs, err := c.History(ctx, pos[0])
		if err != nil {
			return err
		}

		enc := json.NewEncoder(out)
		for _, ev := range evs {
			if err = enc.Encode(ev); err != nil {
				return err
			}
		}

		return nil
	}
}

// timelineView is a timeline with each agent step's summary decoded, and the
// execution's totals.
type timelineView struct {
	*packtrail.Timeline

	Steps  []timelineStep `json:"steps"`
	Totals stepTotals     `json:"totals"`
}

type timelineStep struct {
	packtrail.TimelineStep

	Agent *stiggy.StepSummary `json:"agent,omitempty"`
}

type stepTotals struct {
	TokensIn  int     `json:"tokens_in"`
	TokensOut int     `json:"tokens_out"`
	CostUSD   float64 `json:"cost_usd"`
}

// MarshalJSON keeps the step's own encoding (queue_ms, run_ms) and adds the
// decoded summary as "agent".
func (s timelineStep) MarshalJSON() ([]byte, error) {
	b, err := json.Marshal(s.TimelineStep)
	if err != nil || s.Agent == nil {
		return b, err
	}

	var m map[string]json.RawMessage
	if err = json.Unmarshal(b, &m); err != nil {
		return nil, err
	}

	if m["agent"], err = json.Marshal(s.Agent); err != nil {
		return nil, err
	}

	delete(m, "summary")

	return json.Marshal(m)
}

// formatText is timeline's table output (formatJSON is the default).
const formatText = "text"

func opsTimeline(fs *flag.FlagSet) opsRun {
	format := fs.String("format", formatJSON, "output format: json or text")

	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		if *format != formatJSON && *format != formatText {
			return fmt.Errorf("%w: -format %q (want json or text)", errUsage, *format)
		}

		tl, err := c.Timeline(ctx, pos[0])
		if err != nil {
			return err
		}

		v := &timelineView{Timeline: tl, Steps: make([]timelineStep, 0, len(tl.Steps))}

		for _, s := range tl.Steps {
			st := timelineStep{TimelineStep: s}

			var sum stiggy.StepSummary
			if len(s.Summary) > 0 && json.Unmarshal(s.Summary, &sum) == nil && sum.Agent != "" {
				st.Agent = &sum
				v.Totals.TokensIn += sum.TokensIn
				v.Totals.TokensOut += sum.TokensOut
				v.Totals.CostUSD += sum.CostUSD

				for _, d := range sum.Delegates {
					v.Totals.TokensIn += d.TokensIn
					v.Totals.TokensOut += d.TokensOut
					v.Totals.CostUSD += d.CostUSD
				}
			}

			v.Steps = append(v.Steps, st)
		}

		if *format == formatText {
			return writeTimeline(out, v)
		}

		return writeJSON(out, v)
	}
}

// writeTimeline prints a timeline as a table, then the totals.
func writeTimeline(out io.Writer, v *timelineView) error {
	tw := tabwriter.NewWriter(out, 0, 0, tablePadding, ' ', 0)

	_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\n", v.ExecID, v.Flow, v.Status)
	_, _ = fmt.Fprintln(tw, "STEP\tTYPE\tATTEMPT\tOUTCOME\tQUEUE\tRUN\tAGENT\tTOKENS\tTOOLS")

	for _, s := range v.Steps {
		agentName, tokens, tools := "-", "-", "-"
		if a := s.Agent; a != nil {
			agentName = a.Agent
			tokens = fmt.Sprintf("%d/%d", a.TokensIn, a.TokensOut)

			names := make([]string, 0, len(a.Tools))
			for _, t := range a.Tools {
				names = append(names, fmt.Sprintf("%s×%d", t.Name, t.Calls))
			}

			if len(names) > 0 {
				tools = strings.Join(names, ",")
			}
		}

		_, _ = fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%s\t%s\t%s\t%s\t%s\n", s.Key+nodeSuffix(s.TimelineStep), s.Type,
			s.Attempt, s.Outcome, s.Queue.Round(time.Millisecond), s.Run.Round(time.Millisecond), agentName, tokens,
			tools)
	}

	_, _ = fmt.Fprintf(tw, "TOTAL\t\t\t\t\t\t\t%d/%d\t$%.4f\n", v.Totals.TokensIn, v.Totals.TokensOut,
		v.Totals.CostUSD)

	return tw.Flush()
}

// nodeSuffix names an await's or subflow's node, which have no task key.
func nodeSuffix(s packtrail.TimelineStep) string {
	if s.Key == "" {
		return s.Node
	}

	return ""
}

func opsSignal(fs *flag.FlagSet) opsRun {
	payload := fs.String("payload", "null", "signal payload (JSON)")

	return func(ctx context.Context, c *packtrail.Client, pos []string, _ io.Writer) error {
		v, err := rawJSON("-payload", *payload)
		if err != nil {
			return err
		}

		return c.Signal(ctx, pos[0], pos[1], v)
	}
}

func opsResume(fs *flag.FlagSet) opsRun {
	value := fs.String("value", "null",
		`resume value (JSON): an answer ("text"), {"approve": true} or {"feedback": "..."}`)

	return func(ctx context.Context, c *packtrail.Client, pos []string, _ io.Writer) error {
		v, err := rawJSON("-value", *value)
		if err != nil {
			return err
		}

		return c.Resume(ctx, pos[0], pos[1], v)
	}
}

func opsCancel(fs *flag.FlagSet) opsRun {
	reason := fs.String("reason", "cancelled from the stiggy CLI", "why")

	return func(ctx context.Context, c *packtrail.Client, pos []string, _ io.Writer) error {
		return c.Cancel(ctx, pos[0], *reason)
	}
}

func opsFork(_ *flag.FlagSet) opsRun {
	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		seq, err := strconv.ParseUint(pos[1], 10, 64)
		if err != nil {
			return fmt.Errorf("seq: %w", err)
		}

		id, err := c.Fork(ctx, pos[0], seq)
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(out, id)

		return err
	}
}

func opsRerun(_ *flag.FlagSet) opsRun {
	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		id, err := c.Rerun(ctx, pos[0], pos[1])
		if err != nil {
			return err
		}

		_, err = fmt.Fprintln(out, id)

		return err
	}
}

func opsUpdate(fs *flag.FlagSet) opsRun {
	writes := fs.String("writes", "{}", "channel writes (JSON object), applied with the channels' reducers")

	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		var w map[string]any
		if err := json.Unmarshal([]byte(*writes), &w); err != nil {
			return fmt.Errorf("-writes: %w", err)
		}

		st, err := c.Update(ctx, pos[0], w)
		if err != nil {
			return err
		}

		return writeJSON(out, st)
	}
}

func opsProgress(_ *flag.FlagSet) opsRun {
	return func(ctx context.Context, c *packtrail.Client, pos []string, out io.Writer) error {
		ch, err := c.Progress(ctx, pos[0])
		if err != nil {
			return err
		}

		enc := json.NewEncoder(out)
		for p := range ch {
			if err = enc.Encode(p); err != nil {
				return err
			}
		}

		return nil
	}
}

// runOps runs an execution operation.
func runOps(ctx context.Context, name string, cmd opsCommand, args []string, stdout, stderr io.Writer) error {
	var cf connFlags

	fs := newFlagSet(name, stderr)
	cf.register(fs)
	ns := fs.String("ns", spec.DefaultNamespace, "packtrail namespace of the fleet")
	run := cmd.setup(fs)

	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(pos) != cmd.nargs {
		_, _ = fmt.Fprintf(stderr, "usage: stiggy %s [flags] %s\n", name, cmd.args)

		return errUsage
	}

	logger, err := cf.logger(stderr)
	if err != nil {
		return err
	}

	nc, err := cf.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	tel, err := cf.telemetry(ctx, false)
	if err != nil {
		return err
	}

	defer shutdownTelemetry(tel, logger)

	client, err := packtrail.NewClient(nc, packtrail.WithClientNamespace(*ns), packtrail.WithClientLogger(logger),
		packtrail.WithClientTracerProvider(tel.TracerProvider))
	if err != nil {
		return err
	}

	return run(ctx, client, pos, stdout)
}

func rawJSON(flagName, s string) (json.RawMessage, error) {
	var v json.RawMessage
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return nil, fmt.Errorf("%s: %w", flagName, err)
	}

	return v, nil
}

func writeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")

	return enc.Encode(v)
}
