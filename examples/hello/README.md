# hello

The smallest fleet: one agent and one flow, defined in YAML and run
in-process. It parses the fleet, starts the app, starts an execution and
reads its output.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/hello
```

It prints the execution's ID and status, then the greeting (a real model's
wording varies):

```
execution 5f0R...: completed
Hello, Ada! Welcome aboard.
```

## Test

```bash
go test ./examples/hello                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/hello  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
