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

package e2e_test

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

// writeFiles writes files (relative path → content) under dir.
func writeFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()

	for name, content := range files {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o750); err != nil {
			t.Fatal(err)
		}

		if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// qdrant returns the address of the Qdrant to use, and skips the test when
// none is reachable.
func qdrant(t *testing.T) (string, int) {
	t.Helper()
	live(t)

	addr := envOr("STIGGY_E2E_QDRANT", "localhost:6334")

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("STIGGY_E2E_QDRANT: %v", err)
	}

	p, err := strconv.Atoi(port)
	if err != nil {
		t.Fatalf("STIGGY_E2E_QDRANT port: %v", err)
	}

	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		t.Skipf("no Qdrant at %s (start it with make e2e-up): %v", addr, err)
	}

	_ = conn.Close()

	return host, p
}

// policies are a fictional company's: the facts exist nowhere else, so an
// answer that has them came through knowledge.
var policies = map[string]string{
	"refunds.md": `# Refunds

Customers on the Zephyr Fiber plan can request a full refund within 37 days
of purchase. After 37 days only a pro-rata credit is offered. Refund requests
need the order number and are processed in 6 business days.
`,
	"devices.md": `# Devices

To factory-reset the Zephyr Z9 router, hold the rear pinhole button for 23
seconds, until the status light blinks violet. Do not unplug the router while
the light blinks.
`,
	"billing.md": `# Billing

Invoices are issued on the 3rd of each month. A late payment fee of 4.50 euros
applies after 15 days.
`,
}

const supportFleet = `
namespace: e2e-support
embedders:
  nomic:
    provider: ollama
    model: "${STIGGY_E2E_EMBED_MODEL:-nomic-embed-text}"
    base_url: "${STIGGY_E2E_OLLAMA_URL:-http://localhost:11434/v1}"
vectorstores:
  policies: {type: qdrant, host: %q, port: %d, collection: %q, vector_size: %d, distance: cosine}
knowledge:
  policies:
    embedder: nomic
    vectorstore: policies
    description: Zephyr Telecom's customer policies (refunds, billing, devices).
    top_k: 3
    sources:
      - {path: %q, chunk_size: 400, chunk_overlap: 40}
agents:
  triage:
    model: main
    role: Triage
    goal: "Send each ticket to the right team: billing for refunds, invoices and payments; tech for devices, routers and outages."
  billing:
    model: main
    role: Billing support
    goal: Answer billing tickets from the company policies only. Always search the policies before answering.
    knowledge: [policies]
  tech:
    model: main
    role: Tech support
    goal: Answer technical tickets from the company policies only. Always search the policies before answering.
    knowledge: [policies]
flows:
  support:
    start: triage
    nodes:
      - id: triage
        type: task
        agent: triage
        prompt: "Ticket: {{.input.ticket}}\n\nRoute it to billing or tech."
        routes: [billing, tech]
      - id: billing
        type: task
        agent: billing
        prompt: "Answer this ticket in at most three sentences: {{.input.ticket}}"
      - id: tech
        type: task
        agent: tech
        prompt: "Answer this ticket in at most three sentences: {{.input.ticket}}"
`

// TestSupportDeskRAG routes tickets with a triage agent to specialists who
// answer from knowledge: policies ingested by the engine at startup into a
// real Qdrant, with real embeddings, and searched through tools.
func TestSupportDeskRAG(t *testing.T) {
	host, port := qdrant(t)

	dir := t.TempDir()
	writeFiles(t, dir, policies)

	size, err := strconv.Atoi(envOr("STIGGY_E2E_EMBED_SIZE", "768"))
	if err != nil {
		t.Fatalf("STIGGY_E2E_EMBED_SIZE: %v", err)
	}

	h := start(t, fmt.Sprintf(supportFleet, host, port, fleetName("stiggy_e2e_policies"), size,
		filepath.Join(dir, "*.md")))

	for _, tc := range []struct {
		ticket, team, other string
		facts               []string
	}{
		{
			ticket: "I bought the Zephyr Fiber plan 20 days ago and want my money back. How many days do I have to ask for a refund?",
			team:   "billing", other: "tech", facts: []string{"37"},
		},
		{
			ticket: "How do I factory-reset my Zephyr Z9 router?",
			team:   "tech", other: "billing", facts: []string{"23", "violet"},
		},
	} {
		t.Run(tc.team, func(t *testing.T) {
			st := run(t, h.Client, "support", map[string]any{"ticket": tc.ticket})

			if st.Visits[tc.team] != 1 || st.Visits[tc.other] != 0 {
				t.Fatalf("visits %v: want the ticket routed to %s only", st.Visits, tc.team)
			}

			mustContain(t, tc.team+" answer", result(t, st, tc.team), tc.facts...)
		})
	}
}

// archive is a project archive: the launch code and owner are only in the
// files, among decoys.
var archive = map[string]string{
	"projects/kestrel.md": "# Project Kestrel\n\nOwner: Dana Ruiz\nLaunch code: KX-5521-ALPHA\nBudget: 48200 EUR\n",
	"projects/osprey.md":  "# Project Osprey\n\nOwner: Lee Park\nLaunch code: OS-1180-BRAVO\nBudget: 12900 EUR\n",
	"notes/minutes.txt":   "Weekly sync: Kestrel and Osprey are on track. Codes are in the project files.\n",
}

const archiveFleet = `
namespace: e2e-archive
tools:
  read: {type: file_read, working_dir: "${E2E_ARCHIVE}"}
  find: {type: glob, working_dir: "${E2E_ARCHIVE}"}
  search: {type: grep, working_dir: "${E2E_ARCHIVE}"}
agents:
  boss:
    model: main
    role: Operations manager
    goal: >-
      Answer questions about company projects. You cannot read the archive:
      route to the archivist to look facts up. Once it has reported, answer
      with the exact values it found.
  archivist:
    model: main
    role: Archivist
    description: Looks up facts in the project archive files.
    goal: >-
      Find facts in the project archive. Use your tools (glob to list files,
      read to read them) and report the exact values written in the files.
      Never repeat values from other steps without checking them in the files.
    tools: [read, find, search]
  analyst:
    model: main
    role: Analyst
    description: Does arithmetic and comparisons on figures it is given.
    goal: Compute and compare figures precisely.
patterns:
  desk:
    type: supervisor
    supervisor: boss
    workers: [archivist, analyst]
    prompt: "{{.input.question}}"
    max_steps: 12
    retry: {max_attempts: 3, delay: 2s}
`

// TestSupervisorWithFileTools has a supervisor with no tools answer a
// question only its archivist can: the archivist finds the facts with file
// tools, reports back, and the supervisor answers with them.
func TestSupervisorWithFileTools(t *testing.T) {
	dir := t.TempDir()
	writeFiles(t, dir, archive)
	t.Setenv("E2E_ARCHIVE", dir)

	h := start(t, archiveFleet)

	st := run(t, h.Client, "desk", map[string]any{
		"question": "What is the launch code of Project Kestrel, and who owns it?",
	})

	if st.Visits["archivist"] == 0 {
		t.Fatalf("visits %v: the supervisor never asked the archivist", st.Visits)
	}

	mustContain(t, "answer", text(st.Output), "KX-5521-ALPHA", "Dana Ruiz")
}
