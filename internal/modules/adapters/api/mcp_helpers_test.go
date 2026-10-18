package api //nolint:testpackage // Shared MCP transport fixtures.

import (
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

func mcpSession(t *testing.T, service Adapters) *mcp.ClientSession {
	t.Helper()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	RegisterMCP(server, service)
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "hearth-adapters-api-test", Version: "1.0.0"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("close MCP session: %v", closeErr)
		}
	})
	return session
}

func mcpCallTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	return result
}

func mcpBody(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("MCP error = %#v", result)
	}
	encoded, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatal(err)
	}
	if decodeErr := json.Unmarshal(encoded, target); decodeErr != nil {
		t.Fatal(decodeErr)
	}
}
