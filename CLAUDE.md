# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What stiggy is

stiggy deploys multi-agent architectures on NATS by gluing two libraries:

- **phero** (`github.com/henomis/phero/v2`, released v2.0.0): agents, LLMs, tools, memory, the NATS Agent Protocol.
- **packtrail** (`github.com/henomis/packtrail`, released v1.0.0): durable, event-sourced workflow engine on JetStream.

A fleet is defined once, as YAML (`stiggy` binary) or in Go (library API); both produce the same model. Flows are packtrail flows plus a few agent fields (`agent`, `activity`, `prompt`, `writes`, `routes`); agents run in-process inside packtrail workers. The goal is durable crews, supervisors, swarms, human review and replay on NATS. The implementation plan (milestones M0–M7) lives at `~/.claude/plans/you-have-phero-as-composed-treehouse.md`.

## Layout

| Path | Role |
|---|---|
| `spec/` | The fleet model (`Fleet`, `Model`, `Tool`, `Agent`, `Step`), `ApplyStep` (step → node kind, dynamic edges, `meta.stiggy`), path-located `Error` |
| `config/` | YAML loader: strict decoding, `${ENV}` expansion (never inside `flows`), step desugaring, `Source` maps error paths to lines |
| `compile/` | Pure `Compile(fleet, catalog) → Plan`; reports every problem; golden tests in `testdata/` (`go test ./compile -update`) |
| `registry/` | Name → constructor for LLM providers, tool types (incl. MCP), memory backends, embedders, vector stores and activities, all with strict options; implements `compile.Catalog` |
| `agentrun/` | Worker handler running a fresh phero agent per job: prompt, steering tools (`route_to_*`, `ask_human`), structured output, human-in-the-loop via interrupt payloads, execution memory (`_mem_<agent>` channels), long-term memory sessions, delegates, streaming, usage on every outcome |
| `stiggy.go`, `options.go` | `App`: `New` (validate + compile, no I/O), `Run` (engine, then workers; drain workers before the engine), roles |
| `expose.go` | Exposed agents and flows (phero `nats.Server`), flow-as-agent handler, readiness through discovery |
| `assembly.go` | Builds models, tools, knowledge (RAG), agents and memories once per `Run`; knowledge ingestion in the engine role; cleanups |
| `patterns/` | Crews and patterns as macros: `Crew`, `Supervisor`, `Swarm`, `EvaluatorOptimizer`, `Debate`, `PlanExecute` → plain flows; `compile` expands `crews:`/`patterns:` and maps errors back to them |
| `examples/` | Runnable examples (`run(ctx, nc, out)` + `example_test.go`); scripted models by default, real ones with `STIGGY_EXAMPLE_LIVE=1 OLLAMA_MODEL=gemma4:cloud` (`make examples-live`) |
| `deploy/` | Dockerfile (build from the repo root), docker-compose, example fleet |
| `stiggytest/` | Embedded NATS + app + fake LLMs (`Reply`, `Replies`, `Echo`) |
| `internal/stepio/` | Step meta, job context as template data, `writes` extraction (shared by agents and activities) |
| `cmd/stiggy/` | CLI: `validate`, `compile`, `run` (roles, `-only`, `-http` health), `start`, `agents`, and execution ops (`get`, `history`, `progress`, `signal`, `resume`, `update`, `cancel`, `fork`, `rerun`) |
| `internal/natsconn/` | The only place that dials NATS |
| `internal/archguard/` | The no-direct-NATS guard |

Worker kinds: `agent-<name>`, `act-<name>` and `remote-<name>` are reserved for stiggy steps; any other kind is left to external packtrail workers.

## Commands

```bash
make test    # all tests with -race
make guard   # only the no-direct-NATS architecture guard
make lint    # golangci-lint (config copied from phero)
make check   # test + examples + lint + vet
make examples-live   # examples against a real model (gemma4:cloud via Ollama)
```

Go 1.26.4 (the toolchain is fetched automatically). `go.mod` pins the released phero v2.0.0 and packtrail v1.0.0 (no `replace` directives).

## Rule 1: no direct NATS access

NATS is the only transport, and stiggy's own state lives in NATS (packtrail). Agent storage the user configures may be any backend phero offers (memory: simple, jsonfile, nats, psql, rag; vector stores: qdrant, psql, weaviate). stiggy reaches NATS **only through the public Go APIs of phero and packtrail**.

stiggy code may:
- dial NATS, but **only in `internal/natsconn`**;
- pass the `*nats.Conn` to phero and packtrail constructors (`packtrail.New`, `worker.New`, phero `nats.New` / `nats.NewClient`, `memory/nats.Open`, ...);
- use connection lifecycle methods (`Close`, `Drain`, `Status`, ...) and `nats.Header`;
- run an embedded server in tests through `packtrailtest`.

stiggy code must not:
- import `nats.go/jetstream`, `nats.go/micro` or `nats-server`;
- publish, subscribe or request on any subject, or create streams, consumers or buckets;
- read or write phero's or packtrail's subjects, buckets or wire formats, even documented ones.

`internal/archguard` enforces this with a type-based check over the whole module, tests included (`TestNoDirectNATS`). If you need another `nats.go` identifier, the allowlist in `archguard.go` is the place, and it should only grow for connection setup, never for messaging.

## Rule 2: phero and packtrail are read-only

**Never modify phero or packtrail (including any local checkouts).** When a feature needs something they don't expose:
- leave the feature out, or make the compiler reject it with a clear error;
- only build a stiggy-side workaround when it is architecturally simple: small, local, no raw NATS, and easy to delete.

## Conventions (follow phero and packtrail)

- Apache 2.0 license header on every Go file (`make license`).
- Constructors are `New(...)` with `With...` options; no globals, no `init()`.
- Sentinel errors in each package; wrap with `%w`.
- Unit tests live next to the code. End-to-end tests run against an embedded JetStream server (`packtrailtest.Start`) with a scripted fake LLM, using only stiggy's public API.
