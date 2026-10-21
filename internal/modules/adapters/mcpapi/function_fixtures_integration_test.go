//go:build integration

package mcpapi //nolint:testpackage // Independent fixtures exercise this protocol’s public registration.

import (
	"net/http/httptest"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	mcptools "github.com/mholtzscher/hearth/internal/mcpapi"
)

func functionMCP(t *testing.T, service Functions) (*mcp.ClientSession, *mcptools.Server) {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "functions-test", Version: "1"})
	Register(server, nil, service)
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
