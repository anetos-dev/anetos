// SPDX-License-Identifier: Apache-2.0

package main

import (
	"context"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// mcpSession connects a client to anetos mcp's server for dir, in
// memory.
func mcpSession(t *testing.T, dir string) *mcp.ClientSession {
	t.Helper()
	ct, st := mcp.NewInMemoryTransports()
	server, cleanup := newMCPServer(dir)
	t.Cleanup(cleanup)
	if _, err := server.Connect(t.Context(), st, nil); err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).Connect(t.Context(), ct, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cs.Close() })
	return cs
}

// callTool calls a tool, returning its text and whether it's an error.
func callTool(t *testing.T, cs *mcp.ClientSession, name string, args map[string]any) (string, bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), mcpTestTimeout)
	defer cancel()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("%s: %v", name, err)
	}
	var b strings.Builder
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return b.String(), res.IsError
}

// The server lists its tools, all read and check only, and answers from
// the docs and the code.
func TestMCPServer(t *testing.T) {
	cs := mcpSession(t, "../..") // the cli module: a Go module with models in its fixtures
	tools, err := cs.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range tools.Tools {
		names = append(names, tool.Name)
		if tool.Description == "" || tool.InputSchema == nil {
			t.Errorf("%s: no description or input schema", tool.Name)
		}
	}
	want := []string{"app_about", "check", "command_list", "db_schema", "docs_list", "docs_read", "docs_search", "doctor", "migrate_status", "model_list", "route_list"}
	if slices.Sort(names); !slices.Equal(names, want) {
		t.Errorf("tools = %v", names)
	}
	if out, isErr := callTool(t, cs, "docs_search", map[string]any{"query": "retry a failed job", "limit": 2}); isErr || !strings.Contains(out, "docs_read guides/queues") {
		t.Errorf("docs_search: %v\n%s", isErr, out)
	}
	if out, isErr := callTool(t, cs, "docs_read", map[string]any{"page": "../guides/queues.md"}); isErr || !strings.HasPrefix(out, "# Queues") {
		t.Errorf("docs_read: %v\n%.200s", isErr, out)
	}
	if out, isErr := callTool(t, cs, "docs_read", map[string]any{"page": "testing"}); !isErr || !strings.Contains(out, "guides/testing") {
		t.Errorf("docs_read of an ambiguous page: %v\n%s", isErr, out)
	}
	if out, isErr := callTool(t, cs, "docs_list", nil); isErr || !strings.Contains(out, "guides/queues\tQueues") {
		t.Errorf("docs_list: %v\n%.200s", isErr, out)
	}
	if out, isErr := callTool(t, cs, "model_list", map[string]any{"packages": []string{"./internal/modelgen/internal/fixture"}}); isErr || !strings.Contains(out, `"table": "post_comments"`) {
		t.Errorf("model_list: %v\n%.300s", isErr, out)
	}
	if _, isErr := callTool(t, cs, "docs_search", map[string]any{"query": " "}); !isErr {
		t.Error("docs_search without words isn't an error")
	}
	// Outside a project, the tools that need one say so.
	cs = mcpSession(t, t.TempDir())
	if out, isErr := callTool(t, cs, "model_list", nil); !isErr || !strings.Contains(out, "no Anetos project here") {
		t.Errorf("outside a project: %v %s", isErr, out)
	}
}

const mcpTestTimeout = 5 * time.Minute
