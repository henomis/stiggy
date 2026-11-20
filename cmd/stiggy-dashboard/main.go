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

// Command stiggy-dashboard shows what stiggy deployments are doing, across
// every namespace of a NATS account, in the browser: each execution as a
// trail through its flow, its spending, its timeline, its agents at work,
// and the steps waiting for a human, which it can answer.
package main

import (
	"context"
	"embed"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/henomis/packtrail"

	"github.com/henomis/stiggy/internal/dashboard"
	"github.com/henomis/stiggy/internal/logflags"
	"github.com/henomis/stiggy/internal/natsconn"
	"github.com/henomis/stiggy/internal/webapp"
)

const (
	name        = "stiggy-dashboard"
	defaultAddr = "127.0.0.1:7358"
)

const usage = `usage: stiggy-dashboard [flags]

Shows every namespace's executions in the browser (http://127.0.0.1:7358):
flows drawn as trails, live agent steps, usage, failures, and the steps
waiting for a human, which it can answer unless -read-only.

flags:
  -server URL       NATS server URL(s) (default $NATS_URL or nats://127.0.0.1:4222)
  -creds FILE       NATS credentials file
  -addr ADDR        address to listen on (default 127.0.0.1:7358)
  -ns a,b           namespaces to show (default: every namespace on the account)
  -read-only        only watch: do not answer waiting steps or cancel executions
  -ui DIR           serve the page from DIR instead of the built-in one (development)
  -log-level LEVEL  debug, info, warn or error (default $STIGGY_LOG_LEVEL or info)
  -log-format FMT   text or json (default $STIGGY_LOG_FORMAT or text)
  -version          print the version
`

// version is set at build time with -ldflags "-X main.version=...".
var version string

// page is the built dashboard (make dashboard writes it here).
//
//go:embed all:web
var page embed.FS

// errUsage means the command line was wrong; usage was printed.
var errUsage = errors.New("usage")

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		if !errors.Is(err, errUsage) {
			_, _ = fmt.Fprintln(os.Stderr, name+":", err)
		}

		stop()
		os.Exit(1)
	}
}

func run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() { _, _ = fmt.Fprint(stderr, usage) }

	server := os.Getenv("NATS_URL")
	if server == "" {
		server = natsconn.DefaultURL
	}

	fs.StringVar(&server, "server", server, "NATS server URL(s)")
	creds := fs.String("creds", "", "NATS credentials file")
	addr := fs.String("addr", defaultAddr, "address to listen on")
	ns := fs.String("ns", "", "comma-separated namespaces to show")
	readOnly := fs.Bool("read-only", false, "do not answer waiting steps or cancel executions")
	ui := fs.String("ui", "", "serve the page from this directory instead of the built-in one")
	showVersion := fs.Bool("version", false, "print the version")
	logs := logflags.Register(fs, "STIGGY")

	if err := fs.Parse(args); err != nil {
		return errUsage
	}

	if fs.NArg() != 0 {
		fs.Usage()

		return errUsage
	}

	if *showVersion {
		webapp.Version(stdout, name, version)

		return nil
	}

	logger, err := logs.NewLogger(stderr)
	if err != nil {
		return err
	}

	opts := []dashboard.Option{dashboard.WithLogger(logger)}

	if names := webapp.SplitList(*ns); len(names) > 0 {
		for _, n := range names {
			if err = packtrail.ValidateNamespace(n); err != nil {
				return fmt.Errorf("-ns: %w", err)
			}
		}

		opts = append(opts, dashboard.WithNamespaces(names...))
	}

	if *readOnly {
		opts = append(opts, dashboard.WithReadOnly())
	}

	fsys, err := webapp.Page(page, "web", *ui)
	if err != nil {
		return err
	}

	opts = append(opts, dashboard.WithUI(fsys))

	nc, err := natsconn.Connect(natsconn.Config{URL: server, CredsFile: *creds})
	if err != nil {
		return err
	}
	defer nc.Close()

	return webapp.Serve(ctx, name, *addr, dashboard.New(nc, opts...).Handler(), stdout)
}
