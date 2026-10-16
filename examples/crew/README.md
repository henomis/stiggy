# crew

A crew in YAML: a researcher and a writer work through their tasks, and an
editor checks the writer's draft (a guardrail). The crew is also exposed on
the NATS Agent Protocol, so the example calls it twice: as a flow, with the
packtrail client, and as a single agent, with phero's protocol client.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/crew
```

It prints the brief from the flow (with how many times the writing task
ran), then the answer of the same crew called as the agent
`examples/brief`.

## Test

```bash
go test ./examples/crew                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/crew  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
