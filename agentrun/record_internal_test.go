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

package agentrun

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// TestRecordFit checks a record over its cap fits after fit, the cut texts
// marked, also when the parts texts cannot shrink (a large output schema,
// a long tool list) are what overflows, and a long error is cut too.
func TestRecordFit(t *testing.T) {
	long := strings.Repeat("x", 20000)

	big := func() *Record {
		props := map[string]any{}
		for i := range 300 {
			props["a_field_of_the_answer_"+strconv.Itoa(i)] = map[string]any{"type": "string"}
		}

		tools := make([]string, 200)
		for i := range tools {
			tools[i] = "tool_number_" + strconv.Itoa(i)
		}

		return &Record{
			Version: RecordVersion, Level: "full", OutputSchema: map[string]any{"properties": props},
			RecordRun: RecordRun{
				Agent: "a", System: long, Output: long, Tools: tools, Error: long,
				Calls: []RecordCall{{
					Messages: []RecordMessage{{Role: "user", Text: long}}, Response: long, Error: long,
					ToolCalls: []RecordToolCall{{Tool: "t", Args: long, Result: long, Error: long}},
				}},
			},
		}
	}

	for _, limit := range []int{1 << 10, 4 << 10, 64 << 10} {
		r := big()
		r.fit(limit)

		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}

		if len(b) > limit || !r.Truncated {
			t.Fatalf("limit %d: size %d, truncated %v", limit, len(b), r.Truncated)
		}

		if len(r.Error) == len(long) {
			t.Errorf("limit %d: the error was not cut", limit)
		}

		if limit == 64<<10 {
			// Room for the structure: the texts are cut, not the calls.
			c := r.Calls[0]
			if len(r.Tools) != 200 || r.OutputSchema == nil || !r.SystemTruncated || !c.ResponseTruncated ||
				!c.Messages[0].Truncated || !c.ToolCalls[0].Truncated {
				t.Errorf("limit %d: record = %+v", limit, r.RecordRun)
			}
		} else if r.Calls != nil || r.OutputSchema != nil {
			t.Errorf("limit %d: calls or schema kept in a record that cannot hold them", limit)
		}
	}

	// A record under its cap is left alone.
	small := &Record{Version: RecordVersion, RecordRun: RecordRun{Agent: "a", System: "hi"}}
	small.fit(1 << 10)

	if small.Truncated || small.System != "hi" {
		t.Errorf("small record = %+v", small)
	}
}
