# hitl

Human in the loop. The planner can ask a human a question (`ask_human`), and
its plan is reviewed by a human before the step completes (`human_input`).
The execution pauses durably each time and continues with `Client.Resume`.
Here a simulated human answers, so the example runs unattended.

## What you need

- NATS with JetStream, at `$NATS_URL` (default `nats://127.0.0.1:4222`):
  `nats-server -js`, or `docker run -p 4222:4222 nats -js`.
- Ollama on localhost, with access to `gemma4:cloud` (cloud models need
  `ollama signin`).

## Run

From the repository root:

```bash
nats-server -js &
go run ./examples/hitl
```

It prints each pause and the human's reply, then the final plan:

```
paused for a question: Where to?
human: Rome
paused for a review: ...
human: map[feedback:Make it shorter, two lines at most.]
...
final plan: ...
```

## Test

```bash
go test ./examples/hitl                           # scripted model, output checked exactly
STIGGY_EXAMPLE_LIVE=1 go test -v ./examples/hitl  # against gemma4:cloud
```

Both need the NATS server; without one the test is skipped.
