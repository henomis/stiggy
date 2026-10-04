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
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/henomis/packtrail"
	"github.com/henomis/phero/v2/agent"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy/agentrun"
	"github.com/henomis/stiggy/spec"
)

// HeaderExecutionID is the request header a caller of an exposed flow sets
// to choose the execution id: retrying the same prompt with the same id
// joins the execution instead of starting another.
const HeaderExecutionID = "Stiggy-Execution-Id"

// AgentID is the protocol's framework name of everything stiggy serves.
const AgentID = "stiggy"

// cancelGrace bounds cancelling an execution whose caller went away.
const cancelGrace = 5 * time.Second

// flowAgent serves a flow on the NATS Agent Protocol: each prompt starts an
// execution and the answer is its output. Like any v0.3 agent it answers
// once, at the end; callers that need progress use the packtrail client.
type flowAgent struct {
	client *packtrail.Client
	flow   string
}

// Run implements natsagent.Handler.
func (f *flowAgent) Run(ctx context.Context, parts ...llm.ContentPart) (*agent.Result, error) {
	var opts []packtrail.StartOption

	if req, ok := natsagent.RequestFrom(ctx); ok {
		if id := req.Header.Get(HeaderExecutionID); id != "" {
			opts = append(opts, packtrail.WithExecutionID(id))
		}
	}

	id, err := f.client.Start(ctx, f.flow, flowInput(llm.TextContent(parts...)), opts...)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", f.flow, err)
	}

	st, err := f.client.Wait(ctx, id)
	if err != nil {
		// The caller is gone (or the server is shutting down): the answer
		// would reach nobody, so stop spending on it.
		if ctx.Err() != nil {
			cctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cancelGrace)
			defer cancel()

			_ = f.client.Cancel(cctx, id, "the protocol caller went away")
		}

		return nil, fmt.Errorf("wait %s: %w", id, err)
	}

	if st.Status != packtrail.StatusCompleted {
		return nil, &natsagent.CodedError{
			Code: http.StatusInternalServerError, ErrCode: "execution_" + string(st.Status),
			Message: fmt.Sprintf("execution %s %s: %s", id, st.Status, st.Error),
		}
	}

	return &agent.Result{Parts: []llm.ContentPart{llm.Text(answerText(st.Output))}}, nil
}

// flowInput is the execution input for a prompt: the prompt itself when it is
// a JSON object, {"prompt": text} otherwise.
func flowInput(text string) any {
	var obj map[string]any
	if json.Unmarshal([]byte(text), &obj) == nil && obj != nil {
		return obj
	}

	return map[string]any{"prompt": text}
}

// answerText renders an execution output as an answer: the text of a plain
// agent output, the JSON otherwise.
func answerText(out json.RawMessage) string {
	var m map[string]any
	if json.Unmarshal(out, &m) == nil && len(m) == 1 {
		if t, ok := m[spec.OutputText].(string); ok {
			return t
		}
	}

	return string(out)
}

// startServers serves the exposed agents and flows, and returns once every
// server is ready (registered with the broker and heartbeating).
func (a *App) startServers(ctx context.Context, wg *sync.WaitGroup, errc chan error, asm *assembly) error {
	e := a.fleet.Expose
	if e == nil || (len(e.Agents) == 0 && len(e.Flows) == 0) {
		return nil
	}

	owner := a.fleet.OwnerOrDefault()
	handlers := map[string]natsagent.Handler{}

	for _, name := range e.Agents {
		ag, err := asm.agent(ctx, name)
		if err != nil {
			return fmt.Errorf("stiggy: expose %s: %w", name, err)
		}

		handlers[name] = agentrun.Exposed(ag)
	}

	for _, name := range e.Flows {
		handlers[name] = &flowAgent{client: a.client, flow: name}
	}

	servers := make([]*natsagent.Server, 0, len(handlers))

	for _, name := range sortedKeys(handlers) {
		srv, err := natsagent.New(a.nc, handlers[name], owner, name,
			natsagent.WithAgentID(AgentID), natsagent.WithLogger(a.logger))
		if err != nil {
			return fmt.Errorf("stiggy: expose %s: %w", name, err)
		}

		wg.Go(func() {
			if serr := srv.Start(ctx); serr != nil && ctx.Err() == nil {
				errc <- fmt.Errorf("stiggy: expose %s: %w", name, serr)
			}
		})

		servers = append(servers, srv)
	}

	for _, srv := range servers {
		select {
		case <-srv.Ready():
		case err := <-errc:
			return err
		case <-ctx.Done():
			return ErrNotReady
		}
	}

	return nil
}
