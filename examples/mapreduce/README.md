# mapreduce

A parallel map-reduce in YAML. A planner returns three topics as structured
output (`output_schema`), a researcher runs once per topic in parallel (a
`map` node), an `append` channel gathers the notes, and a writer summarizes
them.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/mapreduce
```

It prints the execution's status, the notes gathered in parallel and the
summary.

## Test

```bash
go test ./examples/mapreduce                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/mapreduce  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
