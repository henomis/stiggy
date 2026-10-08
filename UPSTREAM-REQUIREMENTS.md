# Upstream requirements

stiggy relies only on phero and packtrail (CLAUDE.md, Rules 1–2). When stiggy needs something they
don't provide, it goes here as a requirement for the upstream project instead of being rebuilt in stiggy.
Each entry says what upstream does today (with the source), what stiggy needs, and what stiggy does until then.

Versions checked: phero v2.0.0, packtrail v1.0.1-rc.1.

## phero

### P1. (withdrawn) jsonfile memory with several instances on one file

phero's jsonfile memory reads its file once and rewrites it whole on every `Save`, so two instances of one file
overwrite each other. stiggy no longer offers the `jsonfile` memory type (2026-10-07). It is meant for tests, and
nats, psql and rag are safe with several instances. Reopen this only if stiggy offers jsonfile again.

### P2. Agent: let the caller decide whether a run's turn is saved to memory

- **Today:** `Agent.finishRun` always saves the session (`agent.go:351`, with `context.WithoutCancel`). That includes
  runs that end in an interrupt, an error or a retryable failure.
- **Needed:** an agent option or hook that decides whether to save from the run's outcome, for example
  `WithSaveSession(func(err error) bool)`. Another way: hand the turn back in `Result`, so the caller saves it when the
  work is committed.
- **Why stiggy hits it:** a draft rejected in human review, or a run that packtrail retries, stays in long-term memory
  (simple, jsonfile, nats, psql), and a retry duplicates the prompt. stiggy's execution memory commits only on
  approval; long-term memory can't do the same.
- **Acceptance:** a run that returns an interrupt or an error leaves the memory unchanged when the hook says no.
- **stiggy until then:** a known limitation (REVIEW-BACKLOG 2026-10-07, `agentrun/agentrun.go`).

### P3. nats.Server: tell "the caller went away" from "the server is draining"

- **Today:** a handler's context is the server's `promptCtx` (`nats/server.go:206-214`). Only the drain cancels it,
  once its budget is spent. Nothing on the wire tells the server that a caller abandoned its request, and a cancelled
  handler can't tell why.
- **Needed:** cancel a request's handler context when its caller cancels or stops waiting (a cancel message, or the
  caller's heartbeat lapsing). Set a cause a handler can read with `context.Cause`, for example
  `ErrCallerGone` vs `ErrDraining`.
- **Why stiggy hits it:** an exposed flow cancels its execution when the answer would reach nobody (`expose.go`,
  `flowAgent.Run`). Today that branch only runs at shutdown, so an abandoned prompt keeps spending until the flow
  ends, and a restart past the drain budget cancels executions whose callers are still there.
- **Acceptance:** a client that cancels `Send` makes the handler's context end with cause `ErrCallerGone`, and a
  drain timeout ends it with `ErrDraining`.
- **stiggy until then:** an execution is cancelled only when it is unnamed and its context ends. A named one
  (`Stiggy-Execution-Id`) is never cancelled.

### P4. An empty assistant answer saved to memory breaks the next call (OpenAI-compatible providers)

- **Today:** when a model ends a run with an empty answer, the agent saves an assistant message with no parts and no tool
  calls (`finishRun`, see P2). On the next run that message is replayed; `llm/openai` maps it to a
  `ChatCompletionMessage` with an empty `Content`, which go-openai omits, so the request carries an assistant message with
  no content. Ollama answers `400 invalid message content type: <nil>`, on every retry, so the step fails.
- **Needed:** don't save an assistant message with no content and no tool calls, or send it with `"content": ""`.
- **Why stiggy hits it:** stiggy retries an empty answer (`ErrEmptyAnswer`). With a long-term memory, the failed run's
  empty message is replayed on the retry and the step can never succeed. Seen live with `examples/socialsim` and
  gemma4:cloud on 2026-10-08, before its prompt asked for a non-empty answer. Diagnosis from the error and phero's code;
  the request body was not captured.
- **Acceptance:** a run that ended with an empty answer leaves memory replayable: the next run's request is accepted.
- **stiggy until then:** `agentrun.replayable` wraps every agent memory and drops such messages on save and on retrieve
  (`TestEmptyAnswerIsNotReplayed`). Delete it once phero is fixed. Confirmed live: without it the retry hit the 400;
  with it the retry runs (gemma4:cloud may still answer empty, which the prompt has to discourage).

## packtrail

### K1. (done in packtrail v1.0.1-rc.1) Start with an existing id: report it, and refuse another flow

packtrail v1.0.0 joined any existing execution on `Start(..., WithExecutionID(id))`, whatever its flow, so an exposed
flow could return another flow's output to a caller that sent that flow's `Stiggy-Execution-Id`. Since v1.0.1-rc.1,
`Start` fails with `ErrExecutionConflict` for an id of another flow's execution (live or archived), and
`WithJoinExisting(false)` reports a join as `ErrExecutionExists`. stiggy answers the conflict with a 409
`execution_conflict` (`expose.go`, `TestExposedFlowExecutionIDConflict`). stiggy still joins an execution of the same
flow on purpose: a retry with the same id must find its execution.
