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

// Package report carries what a step's handler learns about one attempt
// (stop reason, latencies, route, review) to the step's end line, which is
// written by the wrapper around the handler.
package report

import (
	"context"
	"log/slog"
	"sync"
)

// Report collects the attributes of one attempt. Its methods are safe for
// concurrent use, and a nil *Report drops what it is given.
type Report struct {
	mu    sync.Mutex
	attrs []slog.Attr
}

type key struct{}

// With returns ctx carrying a new Report.
func With(ctx context.Context) (context.Context, *Report) {
	r := &Report{}

	return context.WithValue(ctx, key{}, r), r
}

// From returns the Report ctx carries, nil when none.
func From(ctx context.Context) *Report {
	r, _ := ctx.Value(key{}).(*Report)

	return r
}

// Add records attrs; a key added again replaces the earlier value.
func (r *Report) Add(attrs ...slog.Attr) {
	if r == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

next:
	for _, a := range attrs {
		for i := range r.attrs {
			if r.attrs[i].Key == a.Key {
				r.attrs[i] = a

				continue next
			}
		}

		r.attrs = append(r.attrs, a)
	}
}

// Attrs returns the recorded attributes in the order first added.
func (r *Report) Attrs() []slog.Attr {
	if r == nil {
		return nil
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	return append([]slog.Attr(nil), r.attrs...)
}
