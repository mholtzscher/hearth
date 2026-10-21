//go:build integration

package api //nolint:testpackage // Shared fixtures exercise public HTTP and MCP registrations.

import (
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

func functionMCP(t *testing.T, service Functions) (*mcp.ClientSession, *mcpapi.Server) {
	t.Helper()
	server := mcpapi.New(mcpapi.Config{Name: "functions-test", Version: "1"})
	RegisterFunctionsMCP(server, service)
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(&mcp.Implementation{Name: "functions-client", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Error(closeErr)
		}
	})
	return session, server
}
