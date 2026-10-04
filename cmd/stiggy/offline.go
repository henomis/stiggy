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
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/henomis/stiggy/compile"
	"github.com/henomis/stiggy/config"
	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

type commonFlags struct {
	activities string
	noEnv      bool
}

func (c *commonFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&c.activities, "activities", "", "comma-separated activity names the program registers")
	fs.BoolVar(&c.noEnv, "no-env", false, "do not expand ${VAR} references")
}

func (c *commonFlags) configOptions() []config.Option {
	if c.noEnv {
		return []config.Option{config.WithoutEnv()}
	}

	return nil
}

// offlineCatalog is the binary's registry plus activity names declared on the
// command line: they are only checked, never run.
type offlineCatalog struct {
	*registry.Registry

	extra []string
}

func (c offlineCatalog) HasActivity(name string) bool {
	return slices.Contains(c.extra, name) || c.Registry.HasActivity(name)
}

func (c *commonFlags) catalog() compile.Catalog {
	cat := offlineCatalog{Registry: registry.New()}

	for a := range strings.SplitSeq(c.activities, ",") {
		if a = strings.TrimSpace(a); a != "" {
			cat.extra = append(cat.extra, a)
		}
	}

	return cat
}

// loadFleet parses a fleet file, printing every problem to stderr.
func loadFleet(path string, opts []config.Option, stderr io.Writer) (*spec.Fleet, *config.Source, error) {
	fleet, src, err := config.LoadFile(path, opts...)
	if err != nil {
		report(src, err, stderr)

		return nil, nil, errReported
	}

	return fleet, src, nil
}

// report prints err, one line per problem, located when possible.
func report(src *config.Source, err error, stderr io.Writer) {
	if src == nil {
		_, _ = fmt.Fprintln(stderr, err)

		return
	}

	for _, line := range src.Describe(err) {
		_, _ = fmt.Fprintln(stderr, line)
	}
}

// load parses and compiles one fleet file, printing every problem to stderr.
func load(path string, cf *commonFlags, stderr io.Writer) (*compile.Plan, error) {
	fleet, src, err := loadFleet(path, cf.configOptions(), stderr)
	if err != nil {
		return nil, err
	}

	plan, err := compile.Compile(fleet, cf.catalog())
	if err != nil {
		report(src, err, stderr)

		return nil, errReported
	}

	return plan, nil
}

func cmdValidate(args []string, stdout, stderr io.Writer) error {
	var cf commonFlags

	fs := newFlagSet(cmdNameValidate, stderr)
	cf.register(fs)

	files, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(files) == 0 {
		fs.Usage()

		return errUsage
	}

	failed := false

	for _, path := range files {
		plan, lerr := load(path, &cf, stderr)
		if lerr != nil {
			failed = true

			continue
		}

		_, _ = fmt.Fprintf(stdout, "%s: ok (%d flows, %d workers, %d schedules)\n",
			path, len(plan.Flows), len(plan.Workers), len(plan.Schedules))
	}

	if failed {
		return errReported
	}

	return nil
}

func cmdCompile(args []string, stdout, stderr io.Writer) error {
	var cf commonFlags

	fs := newFlagSet(cmdNameCompile, stderr)
	cf.register(fs)
	format := fs.String("format", formatYAML, "output format: yaml or json")

	files, err := parseArgs(fs, args)
	if err != nil {
		return err
	}

	if len(files) != 1 || (*format != formatYAML && *format != formatJSON) {
		fs.Usage()

		return errUsage
	}

	plan, err := load(files[0], &cf, stderr)
	if err != nil {
		return err
	}

	if *format == formatJSON {
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")

		return enc.Encode(plan)
	}

	return writeYAML(stdout, plan)
}

// writeYAML prints each flow as a packtrail flow document (what
// `packtrail validate` accepts), after a comment header listing the workers
// and schedules.
func writeYAML(w io.Writer, plan *compile.Plan) error {
	var buf bytes.Buffer

	_, _ = fmt.Fprintf(&buf, "# namespace: %s\n", plan.Namespace)

	for _, wk := range plan.Workers {
		target := "agent " + wk.Agent
		if wk.Activity != "" {
			target = "activity " + wk.Activity
		}

		_, _ = fmt.Fprintf(&buf, "# worker %s runs %s\n", wk.Kind, target)
	}

	for _, s := range plan.Schedules {
		_, _ = fmt.Fprintf(&buf, "# schedule %s starts %s on %q\n", s.Name, s.Flow, s.Cron)
	}

	for _, f := range plan.Flows {
		b, err := yaml.Marshal(f)
		if err != nil {
			return fmt.Errorf("flow %s: %w", f.Name, err)
		}

		buf.WriteString("---\n")
		buf.Write(b)
	}

	_, err := w.Write(buf.Bytes())

	return err
}
