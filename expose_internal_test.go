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
	"sync"
	"testing"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/henomis/packtrail"
	"github.com/henomis/packtrail/flow"
	"github.com/henomis/packtrail/packtrailtest"
	"github.com/henomis/phero/v2/llm"
	natsagent "github.com/henomis/phero/v2/nats"

	"github.com/henomis/stiggy/spec"
)

// TestExposedFlowShutdownKeepsNamedExecution stops an exposed flow's server
// past its drain budget while a prompt that named its execution id is in
// flight: the caller can retry with that id and join it, so the execution
// must go on rather than be cancelled.
func TestExposedFlowShutdownKeepsNamedExecution(t *testing.T) {
	nc := packtrailtest.Start(t).Connect(t)

	entered, release := make(chan struct{}), make(chan struct{})

	var once sync.Once

	slow := llm.Func(func(context.Context, []llm.Message, ...llm.CallOption) (*llm.Result, error) {
		once.Do(func() { close(entered) })
		<-release

		msg := llm.AssistantMessage([]llm.ContentPart{llm.Text("done")})

		return &llm.Result{Message: &msg}, nil
	})

	step, err := AgentTask("a", "w", spec.Step{})
	if err != nil {
		t.Fatal(err)
	}

	app, err := New(nc, WithNamespace("keep"), WithModel("m", slow),
		WithAgent("w", spec.Agent{Model: "m"}), WithFlow(&flow.Flow{Name: "write", Nodes: []flow.Node{step}}))
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runCtx, stopApp := context.WithCancel(ctx)
	appDone := make(chan error, 1)

	go func() { appDone <- app.Run(runCtx) }()

	defer func() {
		stopApp()
		<-appDone
	}()

	select {
	case <-app.Ready():
	case <-ctx.Done():
		t.Fatal("app not ready")
	}

	srv, err := natsagent.New(nc, &flowAgent{client: app.client, flow: "write"}, "o", "write",
		natsagent.WithDrainTimeout(100*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}

	srvCtx, stopSrv := context.WithCancel(ctx)
	srvDone := make(chan struct{})

	go func() {
		defer close(srvDone)

		_ = srv.Start(srvCtx)
	}()

	<-srv.Ready()

	handles, err := natsagent.NewClient(nc).Discover(ctx, natsagent.FilterByOwner("o"))
	if err != nil || len(handles) != 1 {
		t.Fatalf("discover = %v, %v", handles, err)
	}

	go func() {
		s, serr := handles[0].Send(ctx, &natsagent.Request{
			Prompt: "go", Header: nats.Header{HeaderExecutionID: []string{"job-1"}},
		})
		if serr == nil {
			defer s.Close()

			_, _ = s.Text(ctx)
		}
	}()

	<-entered
	stopSrv()
	<-srvDone
	close(release)

	st, err := app.client.Wait(ctx, "job-1")
	if err != nil {
		t.Fatal(err)
	}

	if st.Status != packtrail.StatusCompleted {
		t.Fatalf("status = %s (%s), want completed", st.Status, st.Reason)
	}
}
