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

package jobctx_test

import (
	"errors"
	"testing"

	"github.com/henomis/stiggy/internal/jobctx"
)

func TestJobContext(t *testing.T) {
	if _, ok := jobctx.From(t.Context()); ok {
		t.Fatal("a bare context carries a job")
	}

	ctx := jobctx.With(t.Context(), jobctx.Job{Node: "n", Input: []byte(`{"user":"ann"}`)})

	j, ok := jobctx.From(ctx)
	if !ok || j.Node != "n" {
		t.Fatalf("job = %+v, %v", j, ok)
	}

	var in struct{ User string }
	if err := j.DecodeInput(&in); err != nil || in.User != "ann" {
		t.Fatalf("input = %+v, %v", in, err)
	}

	if err := j.DecodeItem(&in); !errors.Is(err, jobctx.ErrNoValue) {
		t.Fatalf("absent item: err = %v", err)
	}
}
