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

package stepio

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/henomis/packtrail/worker"

	"github.com/henomis/stiggy/spec"
)

func TestWrites(t *testing.T) {
	s := spec.Step{Writes: map[string]string{"all": "output", "v": "output.review.verdict"}}

	type review struct {
		Verdict string `json:"verdict"`
	}

	for name, out := range map[string]any{
		"map":         map[string]any{"review": map[string]any{"verdict": "ok"}},
		"json string": `{"review":{"verdict":"ok"}}`,
		"struct": struct {
			Review review `json:"review"`
		}{Review: review{Verdict: "ok"}},
	} {
		got, err := Writes(s, out)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}

		if got["v"] != "ok" || !reflect.DeepEqual(got["all"], out) {
			t.Errorf("%s: writes = %v", name, got)
		}
	}

	_, err := Writes(s, map[string]any{"review": "flat"})
	if !errors.Is(err, ErrMissingField) || worker.IsPermanent(err) {
		t.Errorf("missing field: err = %v (it must be retryable)", err)
	}

	if _, err = Writes(s, "not json"); !errors.Is(err, ErrMissingField) {
		t.Errorf("non-JSON text: err = %v", err)
	}

	if w, werr := Writes(spec.Step{}, "x"); w != nil || werr != nil {
		t.Errorf("no writes: %v %v", w, werr)
	}
}

func TestStep(t *testing.T) {
	ok := &worker.Job{Meta: json.RawMessage(`{"stiggy":{"agent":"a"},"other":1}`)}
	if s, err := Step(ok); err != nil || s.Agent != "a" {
		t.Fatalf("got %+v, %v", s, err)
	}

	for name, meta := range map[string]string{
		"none":    `{"other":1}`,
		"unknown": `{"stiggy":{"agnet":"a"}}`,
		"broken":  `[1]`,
	} {
		if _, err := Step(&worker.Job{Meta: json.RawMessage(meta)}); err == nil || !worker.IsPermanent(err) {
			t.Errorf("%s: err = %v, want permanent", name, err)
		}
	}
}

func TestData(t *testing.T) {
	idx := 2

	d, err := Data(&worker.Job{Context: worker.Context{
		Input:   json.RawMessage(`{"a":1}`),
		Results: map[string]json.RawMessage{"r": json.RawMessage(`{"text":"t"}`)},
		Index:   &idx,
	}})
	if err != nil {
		t.Fatal(err)
	}

	if d["index"] != 2 || d["input"].(map[string]any)["a"] != float64(1) || d["item"] != nil {
		t.Fatalf("data = %v", d)
	}

	if d["results"].(map[string]any)["r"].(map[string]any)["text"] != "t" {
		t.Fatalf("results = %v", d["results"])
	}
}
