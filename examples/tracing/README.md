# tracing

A small newsroom fleet whose OpenTelemetry spans go to Jaeger. A researcher
collects facts and asks a fact checker (a delegate), a writer drafts, and an
editor may send the draft back once (a route). The program starts the
execution under one root span, so the whole execution is one trace:

```
article
├── stiggy.step research        stop_reason=final llm_calls=3
│   └── invoke_agent researcher
│       ├── chat gemma4
│       ├── execute_tool ask_checker
│       │   └── invoke_agent checker
│       │       └── chat gemma4
│       └── chat gemma4
├── stiggy.step write
│   └── invoke_agent writer
│       └── chat gemma4
└── stiggy.step review          route_by=default
    └── invoke_agent editor
        └── chat gemma4
```

Every span carries the step (`stiggy.exec`, `stiggy.node`,
`stiggy.generation`, ...). Model calls follow the GenAI semantic conventions
(`gen_ai.request.model`, `gen_ai.usage.*`). This fleet captures prompts and
answers too (`content: {capture: true}`); leave that off where they are
private.

## What you need

- Docker, for NATS and Jaeger:
  `docker compose -f examples/tracing/docker-compose.yaml up -d`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
docker compose -f examples/tracing/docker-compose.yaml up -d
go run ./examples/tracing
```

It prints the execution, the article, the usage and the trace id (a real
model's wording and counts vary):

```
execution 1gsRc1TUo3G4Ux6SELslyW: completed
NATS JetStream adds a persistence layer to NATS ...
4 agent runs, 5 model calls, 1 tool calls, 878 tokens in, 378 tokens out
trace 2f487c97e02b6df6372880f4b7634d59
open http://localhost:16686/search?service=example-tracing
```

Open Jaeger at http://localhost:16686 and search for the trace id, or for
the service `example-tracing`. Spans go to `$OTEL_EXPORTER_OTLP_ENDPOINT`
when it is set; `JAEGER_UI` changes the printed link.

Stop the services with
`docker compose -f examples/tracing/docker-compose.yaml down`.

## With the CLI

`fleet.yaml` is a complete fleet: its `tracing:` section exports to the same
Jaeger, with no Go code.

```bash
stiggy run examples/tracing/fleet.yaml &
stiggy start -ns example-tracing -input '{"topic": "NATS KV"}' -wait article
```

`stiggy start` sends no traceparent, so each step is its own trace. Find them
all in Jaeger with the tag `stiggy.exec=<execution id>`, or by the
`trace_id` on each step's log line. To get one trace per execution, start it
with `packtrail.WithTraceparent`, as this program does.

## Test

```bash
go test ./examples/tracing                           # scripted model, in-memory spans, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/tracing  # against gemma4:cloud
```

Both need NATS (from the compose file, or `nats-server -js`); without it the
test is skipped. The scripted test doesn't need Jaeger: it checks the
exported spans in memory.
