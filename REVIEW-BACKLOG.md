- [x] 2026-10-04 `config/env.go` — `${VAR}` in `crews:`/`patterns:` is now left literal (like `flows:`), silently. A clear compile error for a `${` in a crew or pattern would be friendlier; not done, as it changes behaviour beyond the fix.
      Resolved 2026-10-04: ${VAR} in crews/patterns is now a load error (TestEnvRejectedInCrewsAndPatterns).
- [ ] 2026-10-04 `agentrun/hitl.go` — a review interrupt now carries the run's memory turn (incl. tool results) in its payload,
      so a turn the human rejects stays in the execution event log although it never reaches `_mem_<agent>`.
      Same kind of data, same log; revisit if interrupt payloads are ever shown to less-trusted viewers.
- [ ] 2026-10-07 `agentrun/agentrun.go` — long-term memory (simple/nats/psql) is written by phero's `finishRun` on every run,
      including runs that end in a review interrupt or a retryable failure. A rejected draft and a duplicated prompt stay in the
      session, unlike execution memory, which commits only on approval. Upstream: UPSTREAM-REQUIREMENTS.md P2.
- [ ] 2026-10-07 `registry/builtin*.go`, `registry/mcp.go` — `Validate*` checks only unknown keys/types; required and enum options
      (mcp command|url, qdrant distance, kv bucket, psql dsn, weaviate host/class, skill root) fail only at `Run`, not at validate/compile.
- [ ] 2026-10-07 `compile/expand.go` `sourcePath` — every node a crew generates maps to `crews.<c>.tasks.<id>`; errors on manager,
      guardrail (`<t>_guard`) and async `_parallel`/`_join` nodes print a nonexistent path on the `tasks:` line.
- [ ] 2026-10-07 `patterns/patterns.go` `builder.agent` — `ApplyStep` errors (e.g. an empty debater or swarm agent name) are plain
      errors without a `patterns.<p>` path or line.
- [x] 2026-10-07 `patterns/*.go` — crews and patterns return their first problem (early returns), so a crew with two broken tasks reports one;
      messages also repeat the path (`crews.c.tasks.t1.agent: tasks.t1.agent is required`). Partial miss of "compile reports every problem".
      Accepted 2026-10-07: documented in docs/reference.md, compile.Compile and CLAUDE.md. The repeated path in messages is still open.
- [ ] 2026-10-07 `cmd/stiggy/online.go:139` — `nc.Drain()` is asynchronous; the deferred `nc.Close()` cuts it short. Low impact: Run has stopped every component by then.
- [ ] 2026-10-07 `internal/archguard/testdata/{bad,good}/*.go` — fixtures lack the Apache header.
- [ ] 2026-10-07 `internal/archguard/archguard.go:174` — only objects of the root `nats.go` package are checked: a stiggy-declared interface
      `*nats.Conn` satisfies (`type pub interface{ Publish(string, []byte) error }`) bypasses the guard, and so would a third-party
      package taking `*nats.Conn` (jsm.go, orbit.go). Confirmed with a copy of the checker.
- [ ] 2026-10-07 `stiggy.go:96` — `New` registers `WithActivity` handlers into the caller's `WithRegistry` registry; a second `New` with the
      same registry and activities fails with `ErrDuplicate` (engine + workers apps in one program, retry after a failed `New`).
- [ ] 2026-10-07 `cmd/stiggy/offline.go:198` — `stiggy compile` prints `# worker remote-x runs agent ` (empty name) for remote workers.
- [ ] 2026-10-07 `agentrun/agentrun.go:217` — tool-name collisions (two `kv` or `file_read` tools, a delegate named `human` next to
      `ask_human`, route targets equal in their first 64 chars) and names over 64 chars (`route_to_` + a long node id) are only found
      when a job runs, and fail the step permanently. Compile could reject them.
- [ ] 2026-10-07 `internal/stepio/stepio.go:181` — `ErrMissingField` is retryable: a `writes` path that can never resolve (e.g. `output.summary`
      on a step without `output_schema`) re-runs the whole agent, side-effecting tools included, until retries run out.
- [ ] 2026-10-07 `agentrun/memory.go` — execution memory keeps `route_to_*` tool_use/tool_result turns; a later step of the same agent
      without tools sends them with no `tools` defined, which Anthropic rejects (400 → permanent). Plausible, provider-dependent.
- [ ] 2026-10-07 `patterns/teams.go:135`, `patterns/crew.go:189` — on an evaluator-optimizer or guardrail retry the generator sees the
      feedback but not its own rejected draft (context is `[evaluate]` / prev + guard); with `memory: none` it rewrites from scratch.
- [ ] 2026-10-07 `patterns/crew.go:201`, `patterns/teams.go:307` — `expected_output` is pasted into the guard and hierarchical manager
      prompts, which are templates: `expected_output: "like {{name}}"` compiles on a plain task, fails once a guardrail is added.
- [ ] 2026-10-07 `compile/flows.go:66` — packtrail validation is skipped for a flow that already has a stiggy error, so `agent: nope`
      plus a broken `when:` reports only the agent. Partial miss of "compile reports every problem", outside the accepted crews/patterns case.
- [ ] 2026-10-07 `config/config.go:107`, `config/env.go` — with `-no-env`/`WithoutEnv` the `${` check on crews/patterns does not run
      (validate passes, run fails); `${ X }` or `${a-b}` does not match and stays literal silently (e.g. `api_key: "${ OPENAI_API_KEY }"`).
- [ ] 2026-10-07 `config/env.go:84` — env errors inside lists use the index (`crews.c.tasks.0.description`) while `Source` keys tasks by
      id: the line printed is the `tasks:` line, and the path differs from compile's.
- [ ] 2026-10-07 `compile/compile.go:236` — a schedule's `input` is not checked to be an object; `input: hello` passes validate and fails at `Run`.
- [ ] 2026-10-07 `compile/`, `config/` — a crew that fails to expand also yields `unknown flow "c"` from `expose.flows`/`schedules`; `config.Parse`
      walks maps in random order, so `stiggy validate` prints its errors in a different order run to run.
