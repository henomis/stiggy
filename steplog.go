// Copyright 2026 Simone Vellei
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package stiggy

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy/agentrun"
)

// Step outcomes, as logged.
const (
	outcomeCompleted   = "completed"
	outcomeInterrupted = "interrupted"
	outcomeRetry       = "retryable_failed"
	outcomeFailed      = "failed"
)

// logged wraps the handler of a stiggy step so every attempt logs a start
// and an end line through the job's logger (exec, flow, node, key, attempt),
// with role naming what runs the step: agent, remote or activity.
// The end line carries the outcome, the duration, the usage and the route.
func logged(h worker.Handler, role, name string) worker.Handler {
	return func(ctx context.Context, job *worker.Job) (*worker.Result, error) {
		log := job.Logger().With(role, name)
		log.InfoContext(ctx, "stiggy: step started")

		began := time.Now()
		res, err := h(ctx, job)

		outcome := stepOutcome(err)
		attrs := []any{"outcome", outcome, "duration", time.Since(began)}

		usage := usageOf(res, err)
		for _, k := range []string{agentrun.CounterTokensIn, agentrun.CounterTokensOut, agentrun.CounterCostUSD} {
			if v, ok := usage[k]; ok {
				attrs = append(attrs, k, v)
			}
		}

		if res != nil && res.Next != "" {
			attrs = append(attrs, "route", res.Next)
		}

		level := slog.LevelInfo
		if outcome == outcomeFailed || outcome == outcomeRetry {
			level = slog.LevelWarn

			attrs = append(attrs, "err", err)
		}

		log.Log(ctx, level, "stiggy: step ended", attrs...)

		return res, err
	}
}

func stepOutcome(err error) string {
	var intr *worker.InterruptError

	switch {
	case err == nil:
		return outcomeCompleted
	case errors.As(err, &intr):
		return outcomeInterrupted
	case worker.IsPermanent(err):
		return outcomeFailed
	default:
		return outcomeRetry
	}
}

// usageOf is what an attempt reported spending: on its result, or on its
// error (worker.WithUsage).
func usageOf(res *worker.Result, err error) map[string]float64 {
	var ue *worker.UsageError
	if errors.As(err, &ue) {
		return ue.Usage
	}

	if res != nil {
		return res.Usage
	}

	return nil
}
