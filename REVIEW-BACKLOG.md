- [x] 2026-10-04 `config/env.go` — `${VAR}` in `crews:`/`patterns:` is now left literal (like `flows:`), silently. A clear compile error for a `${` in a crew or pattern would be friendlier; not done, as it changes behaviour beyond the fix.
      Resolved 2026-10-04: ${VAR} in crews/patterns is now a load error (TestEnvRejectedInCrewsAndPatterns).
- [ ] 2026-10-04 `agentrun/hitl.go` — a review interrupt now carries the run's memory turn (incl. tool results) in its payload,
      so a turn the human rejects stays in the execution event log although it never reaches `_mem_<agent>`.
      Same kind of data, same log; revisit if interrupt payloads are ever shown to less-trusted viewers.
