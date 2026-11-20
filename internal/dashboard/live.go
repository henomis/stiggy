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

package dashboard

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// heartbeat keeps idle event streams open through proxies.
const heartbeat = 15 * time.Second

// Ended is an execution that just ended, as /api/live streams it.
type Ended struct {
	Namespace string `json:"namespace"`
	ExecID    string `json:"exec_id"`
	Status    string `json:"status"`
}

// Step is one streamed event of an agent step, as the progress stream sends
// it.
type Step struct {
	Progress

	Key     string    `json:"key"`
	Attempt int       `json:"attempt"`
	Time    time.Time `json:"time"`
}

// sse starts an event stream; send writes one event, and false means the
// client is gone.
func sse(w http.ResponseWriter) (func(name string, v any) bool, bool) {
	fl, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming is not supported", http.StatusInternalServerError)

		return nil, false
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	fl.Flush()

	return func(name string, v any) bool {
		var err error

		if name == "" {
			_, err = fmt.Fprint(w, ": ping\n\n")
		} else {
			b, _ := json.Marshal(v) //nolint:errchkjson // the streamed types always encode.
			_, err = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", name, b)
		}

		fl.Flush()

		return err == nil
	}, true
}

// live streams the executions ending in every shown namespace.
func (s *Server) live(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	known, err := s.namespaces(ctx)
	if err != nil {
		httpError(w, err)

		return
	}

	merged := make(chan Ended)

	for _, ns := range known {
		c, cerr := s.client(ctx, ns)
		if cerr != nil {
			continue
		}

		ch, werr := c.WatchTerminal(ctx, 0)
		if werr != nil {
			continue
		}

		go func() {
			for e := range ch {
				select {
				case merged <- Ended{Namespace: ns, ExecID: e.ExecID, Status: string(e.Status)}:
				case <-ctx.Done():
					return
				}
			}
		}()
	}

	send, ok := sse(w)
	if !ok {
		return
	}

	stream(ctx, send, merged, "ended")
}

// progress streams what the agents of an execution do, as it happens.
func (s *Server) progress(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	c, err := s.client(ctx, r.PathValue("ns"))
	if err != nil {
		httpError(w, err)

		return
	}

	ch, err := c.Progress(ctx, r.PathValue("id"))
	if err != nil {
		httpError(w, err)

		return
	}

	steps := make(chan Step)

	go func() {
		defer close(steps)

		for p := range ch {
			st := Step{Key: p.Key, Attempt: p.Attempt, Time: p.Time}
			if json.Unmarshal(p.Data, &st.Progress) != nil {
				continue
			}

			if st.Node == "" {
				st.Node = p.Node
			}

			select {
			case steps <- st:
			case <-ctx.Done():
				return
			}
		}
	}()

	send, ok := sse(w)
	if !ok {
		return
	}

	if stream(ctx, send, steps, "step") {
		send("end", map[string]bool{"ended": true})
	}
}

// stream sends what ch carries until ctx ends or ch closes; true means ch
// closed.
func stream[T any](ctx context.Context, send func(string, any) bool, ch <-chan T, name string) bool {
	tick := time.NewTicker(heartbeat)
	defer tick.Stop()

	for {
		select {
		case <-ctx.Done():
			return false
		case <-tick.C:
			if !send("", nil) {
				return false
			}
		case v, ok := <-ch:
			if !ok {
				return true
			}

			if !send(name, v) {
				return false
			}
		}
	}
}
