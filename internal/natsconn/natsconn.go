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

// Package natsconn opens the NATS connection stiggy hands to phero and
// packtrail. It is the only place in stiggy that dials NATS; everything else
// receives the *nats.Conn and passes it on.
package natsconn

import (
	"errors"
	"fmt"

	"github.com/nats-io/nats.go"
)

// DefaultName is the client name reported to the NATS server.
const DefaultName = "stiggy"

// DefaultURL is the server used when none is configured.
const DefaultURL = nats.DefaultURL

// ErrEmptyURL is returned when no server URL is given.
var ErrEmptyURL = errors.New("natsconn: empty server url")

// Config describes how to reach the NATS server.
type Config struct {
	// URL is one or more comma-separated server URLs.
	URL string
	// Name is the client name; DefaultName when empty.
	Name string
	// CredsFile is an optional NATS credentials (.creds) file.
	CredsFile string
}

// Connect dials NATS. The connection reconnects forever: phero and packtrail
// are built to ride out server restarts, so giving up would only turn a
// transient outage into a crash.
func Connect(cfg Config) (*nats.Conn, error) {
	if cfg.URL == "" {
		return nil, ErrEmptyURL
	}

	name := cfg.Name
	if name == "" {
		name = DefaultName
	}

	opts := []nats.Option{nats.Name(name), nats.MaxReconnects(-1)}
	if cfg.CredsFile != "" {
		opts = append(opts, nats.UserCredentials(cfg.CredsFile))
	}

	nc, err := nats.Connect(cfg.URL, opts...)
	if err != nil {
		return nil, fmt.Errorf("natsconn: connect %s: %w", cfg.URL, err)
	}

	return nc, nil
}
