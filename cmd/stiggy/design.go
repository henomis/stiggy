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
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/henomis/stiggy/internal/design"
	"github.com/henomis/stiggy/registry"
)

const (
	cmdNameDesign     = "design"
	defaultDesignAddr = "127.0.0.1:7357"
	envDesignUI       = "STIGGY_UI"
	designReadTimeout = 10 * time.Second
	designStopTimeout = 5 * time.Second
)

// cmdDesign serves the fleet designer until ctx ends.
func cmdDesign(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	var addr, ui, activities, output string

	fs := newFlagSet(cmdNameDesign, stderr)
	fs.StringVar(&addr, "addr", defaultDesignAddr, "address to listen on")
	fs.StringVar(&ui, "ui", os.Getenv(envDesignUI), "directory of the built designer page (ui/dist)")
	fs.StringVar(&activities, "activities", "", "comma-separated activity names the program registers")
	fs.StringVar(&output, "o", "", "the fleet file to generate (default: the project path with .yaml)")

	files, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(files) > 1 {
		fs.Usage()

		return errUsage
	}

	opts := []design.Option{design.WithActivities(splitList(activities)...)}

	if len(files) == 1 {
		if output == "" {
			output = fleetPath(files[0])
		}

		opts = append(opts, design.WithProject(files[0], output))
	}

	if ui != "" {
		if fi, serr := os.Stat(ui); serr != nil || !fi.IsDir() {
			return fmt.Errorf("-ui %s: not a directory", ui)
		}

		opts = append(opts, design.WithUI(ui))
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("-addr: %w", err)
	}

	srv := &http.Server{
		Handler:           design.New(registry.New(), opts...).Handler(),
		ReadHeaderTimeout: designReadTimeout,
	}

	_, _ = fmt.Fprintf(stdout, "stiggy design: http://%s\n", ln.Addr())

	if ui == "" {
		_, _ = fmt.Fprintf(stderr, "stiggy design: no -ui directory (or $%s): serving the API only\n", envDesignUI)
	}

	go func() {
		<-ctx.Done()

		sctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), designStopTimeout)
		defer cancel()

		_ = srv.Shutdown(sctx)
	}()

	if err = srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("design: %w", err)
	}

	return nil
}

// fleetPath is the default fleet file of a project: news.stiggy.json gives
// news.yaml.
func fleetPath(project string) string {
	base := strings.TrimSuffix(project, filepath.Ext(project))
	base = strings.TrimSuffix(base, ".stiggy")

	return base + ".yaml"
}
