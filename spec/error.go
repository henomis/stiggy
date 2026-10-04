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

package spec

import "errors"

// ErrInvalid matches every [*Error] with errors.Is.
var ErrInvalid = errors.New("invalid fleet")

// Error is one problem found in a fleet. Path locates it with dotted keys,
// using node ids for flow nodes: "agents.writer.model",
// "flows.article.nodes.write.routes". The YAML loader maps paths back to
// lines.
type Error struct {
	Path string
	Msg  string
}

func (e *Error) Error() string {
	if e.Path == "" {
		return e.Msg
	}

	return e.Path + ": " + e.Msg
}

// Unwrap makes errors.Is(err, ErrInvalid) true for every fleet problem.
func (e *Error) Unwrap() error { return ErrInvalid }
