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

package natsconn_test

import (
	"errors"
	"testing"

	"github.com/henomis/packtrail/packtrailtest"

	"github.com/henomis/stiggy/internal/natsconn"
)

func TestConnect(t *testing.T) {
	srv := packtrailtest.Start(t)

	nc, err := natsconn.Connect(natsconn.Config{URL: srv.URL()})
	if err != nil {
		t.Fatal(err)
	}
	defer nc.Close()

	if !nc.IsConnected() {
		t.Fatalf("status = %v, want connected", nc.Status())
	}
}

func TestConnectEmptyURL(t *testing.T) {
	if _, err := natsconn.Connect(natsconn.Config{}); !errors.Is(err, natsconn.ErrEmptyURL) {
		t.Fatalf("err = %v, want ErrEmptyURL", err)
	}
}

func TestConnectUnreachable(t *testing.T) {
	if _, err := natsconn.Connect(natsconn.Config{URL: "nats://127.0.0.1:1"}); err == nil {
		t.Fatal("expected an error for an unreachable server")
	}
}
