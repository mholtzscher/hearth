//go:build integration

package api //nolint:testpackage // Shared MCP transport fixtures.

import (
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
