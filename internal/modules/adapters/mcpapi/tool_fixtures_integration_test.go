//go:build integration

package mcpapi //nolint:testpackage // Shared MCP transport fixtures.

import (
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
)

func mcpSession(t *testing.T, service Adapters) *mcp.ClientSession {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "hearth", Version: "1.0.0"})
	Register(server, service, nil)
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
