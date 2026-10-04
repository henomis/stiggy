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

package archguard_test

import (
	"strings"
	"testing"

	"github.com/henomis/stiggy/internal/archguard"
)

var rules = archguard.Rules{
	DialPackages: []string{"github.com/henomis/stiggy/internal/natsconn"},
}

// TestNoDirectNATS is the guard itself: the whole module, tests included,
// must reach NATS only through phero and packtrail.
func TestNoDirectNATS(t *testing.T) {
	vs, err := archguard.Check("../..", rules, "./...")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range vs {
		t.Errorf("%s", v)
	}
}

func TestCheckerFlagsViolations(t *testing.T) {
	vs, err := archguard.Check(".", rules, "./testdata/bad")
	if err != nil {
		t.Fatal(err)
	}

	got := make([]string, 0, len(vs))
	for _, v := range vs {
		got = append(got, v.Msg)
	}

	all := strings.Join(got, "\n")

	for _, want := range []string{
		"forbidden import github.com/nats-io/nats.go/jetstream",
		"nats.Connect: only internal/natsconn may dial NATS",
		"nats.Conn.Publish",
		"nats.Conn.Subscribe",
		"nats.Msg",
		"nats.KeyValue",
	} {
		if !strings.Contains(all, want) {
			t.Errorf("missing violation %q in:\n%s", want, all)
		}
	}

	if strings.Contains(all, "Close") || strings.Contains(all, "DefaultURL") {
		t.Errorf("allowed identifiers reported:\n%s", all)
	}
}

func TestCheckerAcceptsAllowedUse(t *testing.T) {
	vs, err := archguard.Check(".", rules, "./testdata/good")
	if err != nil {
		t.Fatal(err)
	}

	for _, v := range vs {
		t.Errorf("unexpected violation: %s", v)
	}
}
