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

// Package bad breaks every rule archguard enforces; archguard_test expects
// each marked line to be reported.
package bad

import (
	"context"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream" // want: forbidden import
)

func Run(nc *nats.Conn) {
	_, _ = nats.Connect(nats.DefaultURL)         // want: nats.Connect outside natsconn
	_ = nc.Publish("agents.prompt", nil)         // want: nats.Conn.Publish
	_, _ = nc.Subscribe("x", func(*nats.Msg) {}) // want: nats.Conn.Subscribe and nats.Msg
	js, _ := jetstream.New(nc)
	_, _ = js.Stream(context.Background(), "s")
	var kv nats.KeyValue // want: nats.KeyValue
	_ = kv
	nc.Close() // allowed
}
