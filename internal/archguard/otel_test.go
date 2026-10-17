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

package archguard

import (
	"os/exec"
	"strings"
	"testing"
)

// TestLibraryImportsOnlyOTelAPI checks that the packages a program imports
// reach only the OpenTelemetry API: the SDK, exporters and Prometheus are
// the program's choice (the stiggy command makes its own, in
// internal/telemetry).
func TestLibraryImportsOnlyOTelAPI(t *testing.T) {
	out, err := exec.Command("go", "list", "-deps", "-f", "{{if not .Standard}}{{.ImportPath}}{{end}}",
		"github.com/henomis/stiggy", "github.com/henomis/stiggy/agentrun", "github.com/henomis/stiggy/stiggytest",
		"github.com/henomis/stiggy/registry", "github.com/henomis/stiggy/config").Output()
	if err != nil {
		t.Fatalf("go list: %v", err)
	}

	pkgs := strings.Fields(string(out))
	if len(pkgs) == 0 {
		t.Fatal("go list found no packages")
	}

	for _, pkg := range pkgs {
		for _, f := range []string{
			"go.opentelemetry.io/otel/sdk", "go.opentelemetry.io/otel/exporters", "github.com/prometheus/",
		} {
			if strings.HasPrefix(pkg, f) {
				t.Errorf("library package graph reaches %s", pkg)
			}
		}
	}
}
