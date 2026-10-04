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

	"github.com/henomis/packtrail"

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

	nc, err := cf.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	client, err := packtrail.NewClient(nc, packtrail.WithClientNamespace(*ns))
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
