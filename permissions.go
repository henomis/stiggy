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
	"fmt"

	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/tool/permission"

	"github.com/henomis/stiggy/spec"
)

// toolGuard returns what applies an agent's permissions to its tools. A
// fleet's tools are shared by every agent that names them, so each agent
// gets copies with its policy installed. Without permissions the tools pass
// as they are.
func toolGuard(p *spec.Permissions) (func([]*llm.Tool) ([]*llm.Tool, error), error) {
	if p == nil {
		return func(ts []*llm.Tool) ([]*llm.Tool, error) { return ts, nil }, nil
	}

	def := permission.Allow
	if p.Default == spec.PermissionDeny {
		def = permission.Deny
	}

	opts := []permission.Option{
		permission.WithAllow(p.Allow...), permission.WithDeny(p.Deny...), permission.WithDefault(def),
	}

	if p.WorkingDir != "" {
		opts = append(opts, permission.WithWorkingDir(p.WorkingDir))
	}

	policy, err := permission.New(opts...)
	if err != nil {
		return nil, fmt.Errorf("permissions: %w", err)
	}

	return func(ts []*llm.Tool) ([]*llm.Tool, error) {
		guarded := make([]*llm.Tool, 0, len(ts))

		for _, t := range ts {
			c, cerr := copyTool(t)
			if cerr != nil {
				return nil, cerr
			}

			policy.Apply(c)
			guarded = append(guarded, c)
		}

		return guarded, nil
	}, nil
}

// copyTool returns a tool that calls t, with t's name, description, schema
// and modes, whose middlewares are its own.
func copyTool(t *llm.Tool) (*llm.Tool, error) {
	c, err := llm.NewRawTool(t.Name(), t.Description(), t.Schema(false), t.Handle)
	if err != nil {
		return nil, fmt.Errorf("tool %q: %w", t.Name(), err)
	}

	return c.SetStrict(t.Strict()).SetExclusive(t.Exclusive()), nil
}
