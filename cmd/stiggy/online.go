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
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"slices"
	"strings"
	"text/tabwriter"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy"
	"github.com/henomis/stiggy/internal/natsconn"
	"github.com/henomis/stiggy/spec"
)

// tablePadding separates the columns of tabular output.
const tablePadding = 2

// errExecution means the execution ended without completing.
var errExecution = errors.New("execution did not complete")

type connFlags struct {
	server string
	creds  string
}

func (c *connFlags) register(fs *flag.FlagSet) {
	def := os.Getenv("NATS_URL")
	if def == "" {
		def = natsconn.DefaultURL
	}

	fs.StringVar(&c.server, "server", def, "NATS server URL(s)")
	fs.StringVar(&c.creds, "creds", "", "NATS credentials file")
}

func (c *connFlags) connect() (*nats.Conn, error) {
	return natsconn.Connect(natsconn.Config{URL: c.server, CredsFile: c.creds})
}

func cmdRun(ctx context.Context, args []string, stderr io.Writer) error {
	var cf connFlags

	fs := newFlagSet(cmdNameRun, stderr)
	cf.register(fs)
	role := fs.String("role", string(stiggy.RoleAll), "all, engine or workers")
	only := fs.String("only", "", "comma-separated agents, activities and remotes this process runs")
	httpAddr := fs.String("http", "", "serve /healthz and /readyz on this address")

	files, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(files) != 1 {
		fs.Usage()

		return errUsage
	}

	fleet, src, err := loadFleet(files[0], nil, stderr)
	if err != nil {
		return err
	}

	nc, err := cf.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	logger := slog.New(slog.NewTextHandler(stderr, nil))

	opts := []stiggy.Option{stiggy.WithFleet(fleet), stiggy.WithRole(stiggy.Role(*role)), stiggy.WithLogger(logger)}
	if names := splitList(*only); len(names) > 0 {
		opts = append(opts, stiggy.WithOnly(names...))
	}

	app, err := stiggy.New(nc, opts...)
	if err != nil {
		if errors.Is(err, spec.ErrInvalid) {
			report(src, err, stderr)

			return errReported
		}

		return err
	}

	go func() {
		select {
		case <-app.Ready():
			plan := app.Plan()
			logger.Info("stiggy: serving", "namespace", plan.Namespace, "role", *role,
				"flows", len(plan.Flows), "workers", len(plan.Workers))
		case <-ctx.Done():
		}
	}()

	if *httpAddr != "" {
		stop, herr := serveHealth(*httpAddr, app, logger)
		if herr != nil {
			return herr
		}
		defer stop()
	}

	err = app.Run(ctx)

	// Let in-flight publishes reach the server before closing.
	_ = nc.Drain()

	return err
}

func cmdStart(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var cf connFlags

	fs := newFlagSet(cmdNameStart, stderr)
	cf.register(fs)
	ns := fs.String("ns", spec.DefaultNamespace, "packtrail namespace of the fleet")
	input := fs.String("input", "{}", "execution input, a JSON object")
	id := fs.String("id", "", "execution id (makes the start idempotent)")
	wait := fs.Bool("wait", false, "wait for the execution to end and print its state")

	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(pos) != 1 {
		fs.Usage()

		return errUsage
	}

	var in json.RawMessage
	if err = json.Unmarshal([]byte(*input), &in); err != nil {
		return fmt.Errorf("-input: %w", err)
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

	var opts []packtrail.StartOption
	if *id != "" {
		opts = append(opts, packtrail.WithExecutionID(*id))
	}

	execID, err := client.Start(ctx, pos[0], in, opts...)
	if err != nil {
		return err
	}

	if !*wait {
		_, _ = fmt.Fprintln(stdout, execID)

		return nil
	}

	st, err := client.Wait(ctx, execID)
	if err != nil {
		return err
	}

	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	if err = enc.Encode(map[string]any{
		"exec_id": execID, "status": st.Status, "output": st.Output, "error": st.Error,
		"counters": st.Counters,
	}); err != nil {
		return err
	}

	if st.Status != packtrail.StatusCompleted {
		return fmt.Errorf("%w: %s", errExecution, st.Status)
	}

	return nil
}

func cmdAgents(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var cf connFlags

	fs := newFlagSet(cmdNameAgents, stderr)
	cf.register(fs)
	owner := fs.String("owner", "", "only agents of this owner")
	framework := fs.String("agent", "", "only agents of this framework (stiggy, phero, ...)")

	pos, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(pos) != 0 {
		fs.Usage()

		return errUsage
	}

	nc, err := cf.connect()
	if err != nil {
		return err
	}
	defer nc.Close()

	var opts []natsagent.DiscoverOption
	if *owner != "" {
		opts = append(opts, natsagent.FilterByOwner(*owner))
	}

	if *framework != "" {
		opts = append(opts, natsagent.FilterByAgent(*framework))
	}

	handles, err := natsagent.NewClient(nc).Discover(ctx, opts...)
	if err != nil && !errors.Is(err, natsagent.ErrNoAgentsFound) {
		return err
	}

	slices.SortFunc(handles, func(a, b *natsagent.AgentHandle) int {
		return strings.Compare(a.Owner+"/"+a.Name+"/"+a.InstanceID, b.Owner+"/"+b.Name+"/"+b.InstanceID)
	})

	tw := tabwriter.NewWriter(stdout, 0, 0, tablePadding, ' ', 0)
	_, _ = fmt.Fprintln(tw, "OWNER\tNAME\tAGENT\tSESSION\tPROTOCOL\tINSTANCE")

	for _, h := range handles {
		_, _ = fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			h.Owner, h.Name, h.Agent, h.Session, h.ProtocolVersion, h.InstanceID)
	}

	return tw.Flush()
}
