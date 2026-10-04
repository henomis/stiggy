- [x] 2026-10-04 `config/env.go` — `${VAR}` in `crews:`/`patterns:` is now left literal (like `flows:`), silently. A clear compile error for a `${` in a crew or pattern would be friendlier; not done, as it changes behaviour beyond the fix.
      Resolved 2026-10-04: ${VAR} in crews/patterns is now a load error (TestEnvRejectedInCrewsAndPatterns).
