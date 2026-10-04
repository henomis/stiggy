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
