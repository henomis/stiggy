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

package registry_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	gomcp "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/henomis/stiggy/registry"
	"github.com/henomis/stiggy/spec"
)

type echoArgs struct {
	Text string `json:"text"`
}

// TestMCPTools connects to an MCP server over streamable HTTP, lists its
// tools through the allow filter, calls one, and closes the session on
// cleanup.
func TestMCPTools(t *testing.T) {
	srv := gomcp.NewServer(&gomcp.Implementation{Name: "test", Version: "v1"}, nil)

	gomcp.AddTool(srv, &gomcp.Tool{Name: "echo", Description: "Echo text."},
		func(_ context.Context, _ *gomcp.CallToolRequest, in echoArgs) (*gomcp.CallToolResult, any, error) {
			return &gomcp.CallToolResult{Content: []gomcp.Content{&gomcp.TextContent{Text: "echo: " + in.Text}}}, nil, nil
		})
	gomcp.AddTool(srv, &gomcp.Tool{Name: "secret", Description: "Not allowed."},
		func(context.Context, *gomcp.CallToolRequest, echoArgs) (*gomcp.CallToolResult, any, error) {
			return nil, nil, errors.New("must not be listed")
		})

	hs := httptest.NewServer(gomcp.NewStreamableHTTPHandler(func(*http.Request) *gomcp.Server { return srv }, nil))
	defer hs.Close()

	var cleanups []func() error

	env := registry.Env{Defer: func(f func() error) { cleanups = append(cleanups, f) }}
	tool := spec.Tool{Type: "mcp", Options: map[string]any{"url": hs.URL, "allow": []any{"echo"}}}

	r := registry.New()

	if err := r.ValidateTool(tool); err != nil {
		t.Fatal(err)
	}

	tools, err := r.BuildTool(context.Background(), "srv", tool, env)
	if err != nil {
		t.Fatal(err)
	}

	if len(tools) != 1 || tools[0].Name() != "echo" {
		t.Fatalf("tools = %v, want only echo", tools)
	}

	out, err := tools[0].Handle(context.Background(), `{"text":"hi"}`)
	if err != nil || !strings.Contains(out.(string), "echo: hi") {
		t.Fatalf("call = %v, %v", out, err)
	}

	if len(cleanups) != 1 {
		t.Fatalf("cleanups = %d, want the session close", len(cleanups))
	}

	if err = cleanups[0](); err != nil {
		t.Fatal(err)
	}

	bad := spec.Tool{Type: "mcp", Options: map[string]any{"url": hs.URL, "command": "x"}}
	if _, err = r.BuildTool(context.Background(), "srv", bad, env); !errors.Is(err, registry.ErrOptions) {
		t.Fatalf("command and url: err = %v", err)
	}
}
