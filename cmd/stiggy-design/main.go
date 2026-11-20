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

// Command stiggy-design builds workflows from blocks in the browser. Saving
// writes the project and the fleet it compiles to, checked like `stiggy
// validate` checks it. It never connects to NATS and never runs a fleet.
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
	"path/filepath"
	"strings"
	"syscall"

	"github.com/henomis/stiggy/internal/design"
	"github.com/henomis/stiggy/internal/webapp"
	"github.com/henomis/stiggy/registry"
)

const (
	name        = "stiggy-design"
	defaultAddr = "127.0.0.1:7357"
)

const usage = `usage: stiggy-design [flags] [project.stiggy.json]

Builds workflows from blocks in the browser (http://127.0.0.1:7357). Saving
writes the project and the fleet it compiles to (-o, default the project
path with .yaml), checked like stiggy validate checks it. It never touches
NATS: run the fleet with stiggy run.

flags:
  -addr ADDR        address to listen on (default 127.0.0.1:7357)
  -o FILE           the fleet file to generate
  -activities a,b   activity names a library program registers, so the
                    fleet checks pass
  -ui DIR           serve the page from DIR instead of the built-in one (development)
  -version          print the version
`

// version is set at build time with -ldflags "-X main.version=...".
var version string

// page is the built designer (make design writes it here).
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

	addr := fs.String("addr", defaultAddr, "address to listen on")
	output := fs.String("o", "", "the fleet file to generate (default: the project path with .yaml)")
	activities := fs.String("activities", "", "comma-separated activity names the program registers")
	ui := fs.String("ui", "", "serve the page from this directory instead of the built-in one")
	showVersion := fs.Bool("version", false, "print the version")

	files, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if *showVersion {
		webapp.Version(stdout, name, version)

		return nil
	}

	if len(files) > 1 {
		fs.Usage()

		return errUsage
	}

	opts := []design.Option{design.WithActivities(webapp.SplitList(*activities)...)}

	if len(files) == 1 {
		if *output == "" {
			*output = fleetPath(files[0])
		}

		opts = append(opts, design.WithProject(files[0], *output))
	}

	fsys, err := webapp.Page(page, "web", *ui)
	if err != nil {
		return err
	}

	opts = append(opts, design.WithUI(fsys))

	return webapp.Serve(ctx, name, *addr, design.New(registry.New(), opts...).Handler(), stdout)
}

// parseArgs parses flags placed before or after the project file.
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

// fleetPath is the default fleet file of a project: news.stiggy.json gives
// news.yaml.
func fleetPath(project string) string {
	base := strings.TrimSuffix(project, filepath.Ext(project))
	base = strings.TrimSuffix(base, ".stiggy")

	return base + ".yaml"
}
