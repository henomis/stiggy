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

package registry

import (
	"context"
	"fmt"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/henomis/phero/v2/llm"
	pheromcp "github.com/henomis/phero/v2/mcp"
)

// mcpClientName identifies stiggy to MCP servers.
const mcpClientName = "stiggy"

// mcpOptions configures an MCP server whose tools an agent can call: a local
// process over stdio (command) or a remote server over streamable HTTP (url).
type mcpOptions struct {
	Command string            `json:"command"`
	Args    []string          `json:"args"`
	Env     map[string]string `json:"env"`
	URL     string            `json:"url"`
	Allow   []string          `json:"allow"`
	Deny    []string          `json:"deny"`
}

// newMCPTools connects to the server and lists its tools. The session stays
// open until the app stops.
func newMCPTools(ctx context.Context, o mcpOptions, env Env) ([]*llm.Tool, error) {
	var transport gomcp.Transport

	switch {
	case (o.Command == "") == (o.URL == ""):
		return nil, fmt.Errorf("%w: mcp needs exactly one of command or url", ErrOptions)
	case o.URL != "":
		transport = &gomcp.StreamableClientTransport{Endpoint: o.URL}
	default:
		// phero forwards only a minimal environment to the server process;
		// anything else it needs must be listed in env.
		transport = pheromcp.StdioTransport(o.Command, o.Args, o.Env)
	}

	client := gomcp.NewClient(&gomcp.Implementation{Name: mcpClientName, Version: "v1"}, nil)

	session, err := client.Connect(ctx, transport, nil)
	if err != nil {
		return nil, fmt.Errorf("mcp: connect: %w", err)
	}

	env.deferCleanup(session.Close)

	tools, err := pheromcp.New(session).AsTools(ctx, pheromcp.AllowDeny(o.Allow, o.Deny))
	if err != nil {
		return nil, fmt.Errorf("mcp: list tools: %w", err)
	}

	return tools, nil
}
