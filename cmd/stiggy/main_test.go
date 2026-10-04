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
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/henomis/packtrail/flow"
)

const fixture = "../../compile/testdata/sequential.yaml"

func runCLI(t *testing.T, args ...string) (stdout, stderr string, err error) {
	t.Helper()

	var out, errOut bytes.Buffer

	err = run(context.Background(), args, &out, &errOut)

	return out.String(), errOut.String(), err
}

func TestValidate(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "sk")

	out, _, err := runCLI(t, "validate", "-activities", "publish", fixture)
	if err != nil || !strings.Contains(out, "ok (1 flows, 3 workers, 1 schedules)") {
		t.Fatalf("err=%v out=%q", err, out)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.yaml")
	doc := "agents:\n  a: {model: nope}\nflows:\n  f:\n    nodes:\n      - {id: s, type: task, activity: publish}\n"

	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	_, stderr, err := runCLI(t, "validate", path)
	if !errors.Is(err, errReported) {
		t.Fatalf("err = %v", err)
	}

	for _, want := range []string{
		path + `:2: agents.a.model: unknown model "nope"`,
		path + `:6: flows.f.nodes.s.activity: unknown activity "publish"`,
	} {
		if !strings.Contains(stderr, want) {
			t.Errorf("missing %q in:\n%s", want, stderr)
		}
	}
}

func TestValidateWithoutEnv(t *testing.T) {
	if _, _, err := runCLI(t, "validate", "-activities", "publish", "-no-env", fixture); err != nil {
		t.Fatal(err)
	}
}

// TestCompileYAMLIsPacktrailFlow checks the yaml output is made of documents
// packtrail itself accepts.
func TestCompileYAMLIsPacktrailFlow(t *testing.T) {
	out, _, err := runCLI(t, "compile", "-activities", "publish", "-no-env", fixture)
	if err != nil {
		t.Fatal(err)
	}

	docs := strings.Split(out, "---\n")
	if len(docs) != 2 {
		t.Fatalf("want a header and one flow, got %d parts:\n%s", len(docs), out)
	}

	f, err := flow.Parse([]byte(docs[1]))
	if err != nil {
		t.Fatalf("packtrail rejects the output: %v\n%s", err, docs[1])
	}

	if f.Node("research").Kind != "agent-researcher" {
		t.Errorf("kind = %q", f.Node("research").Kind)
	}
}

func TestCompileJSON(t *testing.T) {
	out, _, err := runCLI(t, "compile", "-format", "json", "-activities", "publish", "-no-env", fixture)
	if err != nil {
		t.Fatal(err)
	}

	var plan struct {
		Namespace string
		Workers   []struct{ Kind string }
	}

	if err = json.Unmarshal([]byte(out), &plan); err != nil || plan.Namespace != "newsroom" || len(plan.Workers) != 3 {
		t.Fatalf("err=%v plan=%+v", err, plan)
	}
}

func TestUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}, {"compile"}, {"compile", "-format", "toml", fixture}} {
		if _, _, err := runCLI(t, args...); !errors.Is(err, errUsage) {
			t.Errorf("%v: err = %v", args, err)
		}
	}

	if out, _, err := runCLI(t, "help"); err != nil || !strings.Contains(out, "usage: stiggy") {
		t.Errorf("help: %v", err)
	}
}

// TestDeployFleetValidates keeps the deployment example valid.
func TestDeployFleetValidates(t *testing.T) {
	if out, stderr, err := runCLI(t, "validate", "-no-env", "../../deploy/fleet.yaml"); err != nil {
		t.Fatalf("%v\n%s%s", err, out, stderr)
	}
}

// TestReadmeFleetValidates keeps the README's quick-start fleet valid.
func TestReadmeFleetValidates(t *testing.T) {
	readme, err := os.ReadFile("../../README.md")
	if err != nil {
		t.Fatal(err)
	}

	_, rest, ok := strings.Cut(string(readme), "```yaml\n# fleet.yaml\n")
	if !ok {
		t.Fatal("no fleet.yaml block in README.md")
	}

	doc, _, _ := strings.Cut(rest, "```")
	path := filepath.Join(t.TempDir(), "fleet.yaml")

	if err = os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}

	if out, stderr, verr := runCLI(t, "validate", "-no-env", path); verr != nil {
		t.Fatalf("%v\n%s%s", verr, out, stderr)
	}
}
