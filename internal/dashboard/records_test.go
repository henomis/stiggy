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
	"encoding/json"
	"errors"
	"testing"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/worker"
)

// TestRecordOf checks the lookup by (key, generation, attempt), and that
// packtrail's marker of a record over its cap is passed on as TooLarge.
func TestRecordOf(t *testing.T) {
	marker, err := json.Marshal(worker.RecordTruncated{Truncated: true, Size: 9000, Head: `{"version":1,`})
	if err != nil {
		t.Fatal(err)
	}

	hist := []packtrail.AttemptRecord{
		{Key: "s", Generation: 1, Attempt: 1, Outcome: packtrail.OutcomeFailed, Record: json.RawMessage(`{"version":1,"trace_id":"abc","truncated":true}`)},
		{Key: "s", Generation: 2, Attempt: 1, Outcome: packtrail.OutcomeCompleted, Record: marker},
	}

	a, err := recordOf(hist, "s", 1, 1, "http://jaeger/trace/{trace_id}")
	if err != nil || a.TooLarge != nil || a.TraceURL != "http://jaeger/trace/abc" ||
		string(a.Record) != `{"version":1,"trace_id":"abc","truncated":true}` {
		t.Fatalf("own truncated record = %+v, %v", a, err)
	}

	b, err := recordOf(hist, "s", 2, 1, "")
	if err != nil || b.Record != nil || b.TooLarge == nil || b.TooLarge.Size != 9000 || b.TooLarge.Head != `{"version":1,` {
		t.Fatalf("packtrail marker = %+v, %v", b, err)
	}

	if _, err = recordOf(hist, "s", 1, 2, ""); !errors.Is(err, ErrNoRecord) {
		t.Fatalf("missing attempt: %v", err)
	}
}
