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
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/henomis/packtrail/worker"
	"github.com/henomis/phero/v2/llm"
	"github.com/henomis/phero/v2/memory"

	"github.com/henomis/stiggy/spec"
)

// jsonNull is an absent channel value.
const jsonNull = "null"

// maxCachedSessions bounds the long-term memories a worker keeps open.
const maxCachedSessions = 1024

// ErrClearUnsupported is returned by Clear on execution memory: the
// conversation is part of the execution's history, which never changes.
var ErrClearUnsupported = errors.New("agentrun: execution memory cannot be cleared")

// MemoryFactory builds the long-term memory of a session.
type MemoryFactory func(ctx context.Context, session string) (memory.Memory, error)

// chanMem is execution memory: the agent's conversation in a hidden channel
// of the execution (spec.MemoryChannel). A step reads the conversation as of
// its own state and appends its turn with the step's writes, so forks,
// reruns and time travel see exactly the history of their point in time.
type chanMem struct {
	prior []llm.Message

	mu    sync.Mutex
	added []llm.Message
}

func newChanMem(job *worker.Job, agentName string) (*chanMem, error) {
	m := &chanMem{}

	raw := job.Context.Channels[spec.MemoryChannel(agentName)]
	if len(raw) == 0 || string(raw) == jsonNull {
		return m, nil
	}

	if err := json.Unmarshal(raw, &m.prior); err != nil {
		return nil, worker.Permanent(fmt.Errorf("agentrun: execution memory: %w", err))
	}

	return m, nil
}

// Save implements memory.Memory: phero saves each run's new messages once.
func (m *chanMem) Save(_ context.Context, messages []llm.Message) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.added = append(m.added, messages...)

	return nil
}

// Retrieve implements memory.Memory.
func (m *chanMem) Retrieve(context.Context, string) ([]llm.Message, error) {
	return slices.Clone(m.prior), nil
}

// Clear implements memory.Memory.
func (m *chanMem) Clear(context.Context) error { return ErrClearUnsupported }

func (m *chanMem) turn() []llm.Message {
	if m == nil {
		return nil
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	return slices.Clone(m.added)
}

// sessionCache keeps the long-term memories of a worker by session, so an
// in-process memory ("simple") lives across jobs. It evicts the oldest
// session past maxCachedSessions.
type sessionCache struct {
	build MemoryFactory

	mu    sync.Mutex
	mems  map[string]memory.Memory
	order []string
}

func (c *sessionCache) get(ctx context.Context, session string) (memory.Memory, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if m, ok := c.mems[session]; ok {
		return m, nil
	}

	m, err := c.build(ctx, session)
	if err != nil {
		return nil, err
	}

	if c.mems == nil {
		c.mems = map[string]memory.Memory{}
	}

	if len(c.order) >= maxCachedSessions {
		delete(c.mems, c.order[0])
		c.order = c.order[1:]
	}

	c.mems[session] = m
	c.order = append(c.order, session)

	return m, nil
}

// SessionOf returns the memory session of an agent's job.
func SessionOf(agentName string, m spec.Memory, execID string) string {
	s := m.Session
	if s == "" {
		s = agentName
	}

	if m.PerExecution {
		s += "-" + execID
	}

	return s
}

// memoryFor returns the memory of one job: execution memory, a long-term
// session, or none.
func (r *runner) memoryFor(ctx context.Context, job *worker.Job) (memory.Memory, *chanMem, error) {
	m := r.a.Spec.Memory

	switch {
	case m.TypeOrDefault() == spec.MemoryExecution:
		cm, err := newChanMem(job, r.a.Name)

		return cm, cm, err
	case m.IsLongTerm() && r.sessions != nil:
		mem, err := r.sessions.get(ctx, SessionOf(r.a.Name, m, job.ExecID))
		if err != nil {
			return nil, nil, fmt.Errorf("agentrun: memory: %w", err)
		}

		return mem, nil, nil
	default:
		return nil, nil, nil
	}
}
