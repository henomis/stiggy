# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What stiggy is

stiggy deploys multi-agent architectures on NATS by gluing two libraries:

- **phero** (`github.com/henomis/phero/v2`, v2.0.0): agents, LLMs, tools, memory, the NATS Agent Protocol.
- **packtrail** (`github.com/henomis/packtrail`, v1.0.1): a durable, event-sourced workflow engine on JetStream.

A fleet (models, tools, knowledge, agents, flows, crews, patterns) is described once, as YAML run by the `stiggy` binary or in Go through `stiggy.New(nc, stiggy.With...)`. Both front ends build the same `spec.Fleet`. `go.mod` pins the released versions, with no `replace` directives.

## Commands

```bash
make test            # every package except examples and e2e, with -race
make check           # test + examples + lint + vet (what CI runs, roughly)
make lint            # golangci-lint v2 (keep its version in sync with .github/workflows/checks.yml)
make fmt             # go fmt + gofumpt + gci
make guard           # only the no-direct-NATS architecture guard
make examples        # examples as tests, scripted models, embedded NATS
make examples-live   # examples against a real model (OLLAMA_MODEL=llama3.2-eco by default)
make e2e             # live scenarios: starts Qdrant (docker), real model via Ollama
                     # (STIGGY_E2E_MODEL, default gemma4:cloud); E2E_RUN=TestName for one
make build           # bin/stiggy, version stamped from git describe
make license         # add the Apache header to new Go files

go test -race -run TestName ./compile/      # one test
go test ./compile -update                   # rewrite compile/testdata/*.golden.json
```

Go 1.26.4 (the toolchain is fetched automatically). `e2e/` tests skip unless `STIGGY_E2E=1`; examples use scripted models unless `STIGGY_EXAMPLE_LIVE=1`.

## Architecture

The pipeline from description to running fleet:

1. **Front ends → `spec.Fleet`.** `config/` loads YAML strictly (unknown fields are errors), expands `${ENV}` (never inside `flows`), desugars steps, and keeps a `Source` that maps error paths to file lines. The Go options in `options.go` fill the same struct.
2. **`compile.Compile(fleet, catalog) → Plan`** is pure (no I/O). It first expands `crews:` and `patterns:` (macros in `patterns/`: crew, supervisor, swarm, evaluator-optimizer, debate, plan-execute) into plain packtrail flows, mapping errors back to the macro. Then it validates everything and reports every problem at once, as path-located `spec.Error`s. The `Catalog` (implemented by `registry/`) says which providers, tool types, memories, vector stores and activities exist. The `Plan` holds packtrail `flow.Flow`s, the worker kinds and the schedules. Golden tests in `compile/testdata/` pin the output.
3. **Steps.** A flow step with `agent`/`activity`/`remote` plus `prompt`, `writes`, `routes` becomes a packtrail task node. `spec.ApplyStep` stores the step in the node's `meta.stiggy`, so it is versioned with the flow and running executions keep the configuration they started with. Agent-chosen routes become dynamic edges.
4. **`App.New`** validates and compiles only. **`App.Run`** builds models, tools, knowledge and memories once (`assembly.go`), starts the packtrail engine and one worker per kind according to the role (`all`/`engine`/`workers`, `-only`), and exposes agents and flows over the NATS Agent Protocol (`expose.go`). On shutdown, workers drain before the engine stops.
5. **Per job, `agentrun/`** builds a fresh phero agent and renders the prompt over the job context (`internal/stepio`). It adds the steering tools (`route_to_*`, `ask_human`), delegates and structured output, and returns output, channel writes and usage. Routing decisions, human questions (interrupt payloads) and execution memory (hidden `_mem_<agent>` channels) live in packtrail state, so they stay correct under fork, rerun and time travel. Tools can read their calling job through `stiggy.JobFromContext`.

Worker kinds `agent-<name>`, `act-<name>` and `remote-<name>` are reserved for stiggy steps. Any other kind is left to external packtrail workers.

`cmd/stiggy` is a thin CLI: offline (`validate`, `compile`), online (`run`, `start`, `agents`), execution ops on a packtrail client (`get`, `history`, `progress`, `signal`, `resume`, `update`, `cancel`, `fork`, `rerun`), and `version`.

`stiggytest/` gives tests an embedded NATS, an app and fake LLMs (`Reply`, `Replies`, `Echo`). The user docs are `README.md` and `web/docs.html` (published to GitHub Pages from `web/`); keep them in step with behavior changes.

## Rule 1: no direct NATS access

NATS is the only transport, and stiggy's own state lives in NATS through packtrail. Agent storage the user configures may be any backend phero offers. stiggy reaches NATS **only through the public Go APIs of phero and packtrail**.

stiggy code may:
- dial NATS, but **only in `internal/natsconn`**;
- pass the `*nats.Conn` to phero and packtrail constructors (`packtrail.New`, `worker.New`, phero `nats.New` / `nats.NewClient`, `memory/nats.Open`, ...);
- use connection lifecycle methods (`Close`, `Drain`, `Status`, ...) and `nats.Header`;
- run an embedded server in tests through `packtrailtest`.

stiggy code must not:
- import `nats.go/jetstream`, `nats.go/micro` or `nats-server`;
- publish, subscribe or request on any subject, or create streams, consumers or buckets;
- read or write phero's or packtrail's subjects, buckets or wire formats, even documented ones.

`internal/archguard` enforces this with a type-based check over the whole module, tests included (`TestNoDirectNATS`). If you need another `nats.go` identifier, add it to the allowlist in `archguard.go`. The allowlist should only grow for connection setup, never for messaging.

## Rule 2: phero and packtrail are read-only

**Never modify phero or packtrail, including local checkouts.** When a feature needs something they don't expose:
- leave the feature out, or make the compiler reject it with a clear error;
- build a stiggy-side workaround only when it is architecturally simple: small, local, no raw NATS, and easy to delete.

## Conventions (shared with phero and packtrail)

- Apache 2.0 license header on every Go file.
- Constructors are `New(...)` with `With...` options; no globals, no `init()`.
- Sentinel errors in each package, wrapped with `%w`.
- Unit tests live next to the code. End-to-end tests use only the public API, against an embedded JetStream server (`packtrailtest.Start`) with a scripted fake LLM.
- Live tests (`e2e/`, `examples-live`) assert structure and planted facts, never exact model wording.
- In docs and comments, describe stiggy on its own terms; never compare it with other agent frameworks.
