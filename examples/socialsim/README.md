# socialsim

A small social simulation. A director casts people from a news item, they
post on a tiny social network for two rounds, and an analyst explains how
opinion moved. Then the run is forked right after round 1 with breaking news,
and round 2 and the report run again on the fork.

It shows templated system prompts and memory sessions (one persona agent
plays every cast member), Go tools that know their caller
(`stiggy.JobFromContext`), packtrail's long-term store, a loop, and a fork
with writes.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/socialsim
```

It prints the cast, the timeline of posts by round and the report, then the
same for the fork. With a real model it takes a few minutes.

## Test

```bash
go test ./examples/socialsim                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/socialsim  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
