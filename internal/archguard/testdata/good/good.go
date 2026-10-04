// Package good uses only what stiggy is allowed to: it receives a connection,
// inspects it and passes it on.
package good

import "github.com/nats-io/nats.go"

type sink func(*nats.Conn, nats.Header)

func Run(nc *nats.Conn, pass sink) {
	h := nats.Header{}
	h.Set("Stiggy-Exec", "e1")

	if nc.IsConnected() && nc.Status() == nats.CONNECTED {
		pass(nc, h)
	}

	_ = nc.Drain()
}
