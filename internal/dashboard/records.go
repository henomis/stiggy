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
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/henomis/packtrail"
)

// ErrNoRecord is returned for an attempt that stored no record.
var ErrNoRecord = errors.New("dashboard: the attempt has no record")

// AttemptRecord is the record of one attempt, as the records endpoint
// answers it.
type AttemptRecord struct {
	Key        string    `json:"key"`
	Generation int       `json:"generation"`
	Attempt    int       `json:"attempt"`
	Outcome    string    `json:"outcome"`
	Seq        int       `json:"seq"`
	Time       time.Time `json:"time"`
	// Record is what the worker recorded, as it stored it; nil when it was
	// too large for packtrail's cap (then TooLarge is set).
	Record json.RawMessage `json:"record,omitempty"`
	// TooLarge is packtrail's marker of a record over its cap.
	TooLarge *TooLarge `json:"too_large,omitempty"`
	// TraceURL links to the attempt's trace, when the dashboard has a
	// tracing link (WithTraceURL) and the record a trace id.
	TraceURL string `json:"trace_url,omitempty"`
}

// TooLarge is a record cut by packtrail (worker.RecordTruncated): its size
// and the start of its JSON encoding.
type TooLarge struct {
	Size int    `json:"size"`
	Head string `json:"head"`
}

// recordOf finds the record of the attempt (key, gen, attempt) in hist,
// linked to its trace by traceURL (see WithTraceURL).
func recordOf(hist []packtrail.AttemptRecord, key string, gen, attempt int, traceURL string) (*AttemptRecord, error) {
	for _, a := range hist {
		if a.Key != key || a.Generation != gen || a.Attempt != attempt {
			continue
		}

		out := &AttemptRecord{
			Key: a.Key, Generation: a.Generation, Attempt: a.Attempt, Outcome: a.Outcome, Seq: a.Seq, Time: a.Time,
			Record: a.Record,
		}

		// packtrail's marker has no version; a stiggy record always has one.
		var probe struct {
			Version   int    `json:"version"`
			Truncated bool   `json:"truncated"`
			Size      int    `json:"size"`
			Head      string `json:"head"`
			TraceID   string `json:"trace_id"`
		}

		if json.Unmarshal(a.Record, &probe) == nil && probe.Version == 0 && probe.Truncated && probe.Size > 0 {
			out.Record, out.TooLarge = nil, &TooLarge{Size: probe.Size, Head: probe.Head}
		}

		if traceURL != "" && probe.TraceID != "" {
			out.TraceURL = strings.ReplaceAll(traceURL, "{trace_id}", url.PathEscape(probe.TraceID))
		}

		return out, nil
	}

	return nil, ErrNoRecord
}

// records answers the record of one attempt of a node:
// ?node=&key=&generation=&attempt=. An empty key means the node.
func (s *Server) records(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), requestTTL)
	defer cancel()

	q := r.URL.Query()
	node, key := q.Get("node"), q.Get("key")

	if key == "" {
		key = node
	}

	gen, gerr := strconv.Atoi(q.Get("generation"))
	attempt, aerr := strconv.Atoi(q.Get("attempt"))

	if node == "" || gerr != nil || aerr != nil {
		writeMessage(w, http.StatusBadRequest, "node, generation and attempt are required")

		return
	}

	c, err := s.client(ctx, r.PathValue("ns"))
	if err != nil {
		httpError(w, err)

		return
	}

	hist, err := c.RecordHistory(ctx, r.PathValue("id"), node)
	if err != nil {
		httpError(w, err)

		return
	}

	rec, err := recordOf(hist, key, gen, attempt, s.traceURL)
	if err != nil {
		writeMessage(w, http.StatusNotFound, fmt.Sprintf("%v: %s/%d/%d", err, key, gen, attempt))

		return
	}

	writeJSON(w, rec)
}

// writeMessage answers an error message, as httpError does.
func writeMessage(w http.ResponseWriter, code int, msg string) {
	writeStatus(w, code, map[string]string{"error": msg})
}
