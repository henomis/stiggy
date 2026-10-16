# router

Agent-chosen routing, built with the Go API. A triage agent reads a support
ticket and sends it to the billing or the tech agent through the
`route_to_billing` and `route_to_tech` tools. The choice is stored in the
execution's event log.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/router
```

It runs two tickets and prints, for each, the agent it was routed to and
its answer:

```
ticket "My invoice charges me twice." → billing
...
ticket "The dashboard shows a 500 error." → tech
...
```

## Test

```bash
go test ./examples/router                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/router  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
