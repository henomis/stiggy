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
		if !strings.Contains(out, "→ billing") || !strings.Contains(out, "→ tech") {
			t.Fatalf("output:\n%s", out)
		}
	})
}
