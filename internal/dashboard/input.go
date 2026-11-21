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
	"strconv"
	"unicode/utf8"
)

// roleUser is the role of a record message from the user's side.
const roleUser = "user"

// inputMax bounds the text of an input preview, in bytes.
const inputMax = 600

// Why an input preview has no text (InputPreview.Missing).
const (
	// MissingTooLarge is a record over packtrail's cap: only its start was
	// kept, not the prompt.
	MissingTooLarge = "too_large"
	// MissingNotKept is a record cut to its record_max_bytes so far that
	// the prompt is gone.
	MissingNotKept = "not_kept"
)

// InputPreview is the start of the prompt a step attempt put to its agent,
// read from the attempt's record, so the story shows each step's input
// without fetching every record.
type InputPreview struct {
	// Text is the step prompt, cut to inputMax bytes on a rune boundary.
	Text string `json:"text"`
	// Size is the byte length of the prompt as the record keeps it.
	Size int `json:"size"`
	// Cut is set when Text is shorter than the prompt: cut here, or by the
	// record's record_max_bytes.
	Cut bool `json:"cut,omitempty"`
	// Memory counts the messages before the prompt in the first call:
	// memory and a review's transcript.
	Memory int `json:"memory,omitempty"`
	// Missing says why there is no text (MissingTooLarge, MissingNotKept).
	Missing string `json:"missing,omitempty"`
}

// previewer gives the input preview of an attempt's record, nil for none.
type previewer func(key string, gen, attempt int, record json.RawMessage) *InputPreview

// previewOf is a previewer without a cache.
func previewOf(_ string, _, _ int, record json.RawMessage) *InputPreview {
	return InputPreviewOf(record)
}

// InputPreviewOf reads the input preview of a record (an agentrun.Record,
// or packtrail's marker of one over its cap); nil for no record.
func InputPreviewOf(record json.RawMessage) *InputPreview {
	if len(record) == 0 {
		return nil
	}

	// Only what the preview needs; packtrail's marker has no version.
	var probe struct {
		Version   int  `json:"version"`
		Truncated bool `json:"truncated"`
		Size      int  `json:"size"`
		Calls     []struct {
			Messages []struct {
				Role      string `json:"role"`
				Text      string `json:"text"`
				Truncated bool   `json:"truncated"`
			} `json:"messages"`
		} `json:"calls"`
	}

	if json.Unmarshal(record, &probe) != nil {
		return nil
	}

	if probe.Version == 0 && probe.Truncated && probe.Size > 0 {
		return &InputPreview{Missing: MissingTooLarge}
	}

	if len(probe.Calls) == 0 {
		return &InputPreview{Missing: MissingNotKept}
	}

	msgs := probe.Calls[0].Messages

	// The step prompt is the last user message: memory and a transcript
	// come before it.
	at := -1

	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == roleUser {
			at = i

			break
		}
	}

	if at < 0 {
		return &InputPreview{Missing: MissingNotKept, Memory: len(msgs)}
	}

	m := msgs[at]
	if m.Truncated && m.Text == "" {
		return &InputPreview{Missing: MissingNotKept, Memory: at}
	}

	text := cutRunes(m.Text, inputMax)

	return &InputPreview{Text: text, Size: len(m.Text), Cut: m.Truncated || len(text) < len(m.Text), Memory: at}
}

// cutRunes returns the first n bytes of s, cut back to a rune boundary.
func cutRunes(s string, n int) string {
	if len(s) <= n {
		return s
	}

	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}

	return s[:n]
}

// previewCacheMax bounds the previews kept: past it, the cache starts over.
const previewCacheMax = 4096

// previews returns a previewer of the execution exec in ns that keeps the
// previews it reads: records never change.
func (s *Server) previews(ns, exec string) previewer {
	return func(key string, gen, attempt int, record json.RawMessage) *InputPreview {
		id := ns + "\x00" + exec + "\x00" + key + "\x00" + strconv.Itoa(gen) + "\x00" + strconv.Itoa(attempt)
		if p, ok := s.preview.get(id); ok {
			return p
		}

		p := InputPreviewOf(record)
		s.preview.put(id, p)

		return p
	}
}
