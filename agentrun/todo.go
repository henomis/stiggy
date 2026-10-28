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
	"slices"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/tool/todo"
)

// TodoToolName is the name of the tool that keeps an agent's task list.
const TodoToolName = "todo_write"

// runTools returns the tools a gets afresh for each run, since they keep
// state: its task list (spec.Agent.Todo). onTodo, when not nil, gets each
// update of the list.
func (a Agent) runTools(onTodo func([]todo.Item)) ([]*llm.Tool, error) {
	if !a.Spec.Todo {
		return nil, nil
	}

	var opts []todo.Option
	if onTodo != nil {
		opts = append(opts, todo.WithOnChange(onTodo))
	}

	t, err := todo.New(opts...).Tool()
	if err != nil {
		return nil, err
	}

	return []*llm.Tool{t}, nil
}

// todoProgress returns the callback that publishes a streaming agent's task
// list as progress; nil when the agent does not stream.
func (a Agent) todoProgress(job *worker.Job) func([]todo.Item) {
	if !a.Spec.Stream {
		return nil
	}

	p := &progress{job: job, agent: a.Name}

	return func(items []todo.Item) {
		p.send(Progress{Type: ProgressTodo, Todos: slices.Clone(items)})
	}
}
