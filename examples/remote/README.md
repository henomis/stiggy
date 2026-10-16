# remote

An agent another team serves on the NATS Agent Protocol, used by a fleet.
The "legal team" is a plain phero agent behind phero's NATS server, started
by the same program for convenience. The fleet names it under `remotes:`,
runs it as a flow step (`remote: legal`) and lets its writer delegate to it
as a tool.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/remote
```

It prints what the legal team sees (the step that prompted it, from the
request headers), then the draft and the legal review.

## Test

```bash
go test ./examples/remote                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/remote  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
