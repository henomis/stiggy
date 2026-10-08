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
	"strings"
	"testing"

	"github.com/henomis/stiggy/examples/internal/extest"
)

func TestExample(t *testing.T) {
	extest.Run(t, run, func(out string) {
		for _, w := range []string{
			"timeline (6 posts):",
			"[round 1] Ada: Ada's take, round 1.",
			"[round 1] Bo: Bo's take, round 1.",
			"[round 2] Cy: Cy's take, round 2.",
			"report: Ada and Bo held their ground",
		} {
			if !strings.Contains(out, w) {
				t.Fatalf("missing %q in output:\n%s", w, out)
			}
		}
	})
}
