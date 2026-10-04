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

// Command stiggy validates, compiles and runs a fleet described in YAML, and
// starts executions of its flows.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
)

const usage = `usage: stiggy <command> [flags] [args]

offline:
  validate [-activities a,b] [-no-env] <fleet.yaml>...
      check fleet files; every problem is reported with its line
  compile [-activities a,b] [-no-env] [-format yaml|json] <fleet.yaml>
      print the packtrail flows and the workers the fleet compiles to

online (NATS from -server, $NATS_URL, or nats://127.0.0.1:4222):
  run [-server URL] [-creds FILE] [-role all|engine|workers] [-only a,b] [-http ADDR] <fleet.yaml>
      serve the fleet until interrupted; every process of a deployment
      uses the same file and picks its part with -role (and -only for
      workers); -http serves /healthz and /readyz
  start [-server URL] [-creds FILE] [-ns NAMESPACE] [-input JSON] [-id ID] [-wait] <flow>
      start an execution and print its id; -wait prints the final state
  agents [-server URL] [-creds FILE] [-owner OWNER] [-agent FRAMEWORK]
      list the NATS Agent Protocol agents on the bus (exposed stiggy agents
      and flows, phero agents, any v0.3 agent)

executions (same connection flags, and -ns NAMESPACE):
  get <exec> [-seq N]            state now, or right after event N
  history <exec>                 the event log, as JSON lines
  progress <exec>                streamed agent progress, as JSON lines
  signal <exec> <name> [-payload JSON]
  resume <exec> <node> [-value JSON]   answer an agent waiting for a human
  update <exec> -writes JSON     write channels (edit state while paused)
  cancel <exec> [-reason TEXT]
  fork <exec> <seq>              branch an execution from a point in history
  rerun <exec> <node>            run again from a node

-activities names the Go activities a library program registers, so a fleet
written for the library can be checked by the binary.
-no-env leaves ${VAR} references unexpanded, so a fleet can be checked
without its secrets (flows are never expanded anyway).

Executions can also be inspected, signalled, resumed, forked and cancelled
with packtrail's own CLI and packtrail-ui on the fleet's namespace.
`

// Command names and output formats.
const (
	cmdNameValidate = "validate"
	cmdNameCompile  = "compile"
	cmdNameRun      = "run"
	cmdNameStart    = "start"
	cmdNameAgents   = "agents"
	formatYAML      = "yaml"
	formatJSON      = "json"
)

var (
	// errReported means the problem was already printed.
	errReported = errors.New("reported")
	// errUsage means the command line was wrong; usage was printed.
	errUsage = errors.New("usage")
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errReported) && !errors.Is(err, errUsage) {
			_, _ = fmt.Fprintln(os.Stderr, "stiggy:", err)
		}

		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		_, _ = fmt.Fprint(stderr, usage)

		return errUsage
	}

	switch args[0] {
	case cmdNameValidate:
		return cmdValidate(args[1:], stdout, stderr)
	case cmdNameCompile:
		return cmdCompile(args[1:], stdout, stderr)
	case cmdNameRun:
		return cmdRun(ctx, args[1:], stderr)
	case cmdNameStart:
		return cmdStart(ctx, args[1:], stdout, stderr)
	case cmdNameAgents:
		return cmdAgents(ctx, args[1:], stdout, stderr)
	case "help", "-h", "-help", "--help":
		_, _ = fmt.Fprint(stdout, usage)

		return nil
	default:
		if cmd, ok := opsCommands()[args[0]]; ok {
			return runOps(ctx, args[0], cmd, args[1:], stdout, stderr)
		}

		_, _ = fmt.Fprintf(stderr, "unknown command %q\n\n%s", args[0], usage)

		return errUsage
	}
}

func newFlagSet(name string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }

	return fs
}

// parseArgs parses flags placed before, between or after the positional
// arguments ("start article -wait" and "start -wait article" both work).
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	var pos []string

	for {
		if err := fs.Parse(args); err != nil {
			return nil, errUsage
		}

		if fs.NArg() == 0 {
			return pos, nil
		}

		pos = append(pos, fs.Arg(0))
		args = fs.Args()[1:]
	}
}
