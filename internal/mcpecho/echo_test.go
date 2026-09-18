package mcpecho_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v5"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/mcpecho"
)

// tenantKey scopes the test context value to this package.
type tenantKey struct{}

const tenantID = "tenant-1"

type tenantInput struct{}

type tenantOutput struct {
	Tenant string `json:"tenant"`
}

// TestMountServesToolsAndPropagatesEchoContext proves Echo middleware that
// derives a request context reaches MCP tool handlers, and that tools/list and
// tools/call work through the mounted route.
func TestMountServesToolsAndPropagatesEchoContext(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[tenantInput, tenantOutput]{
		Name:        "current_tenant",
		Description: "Report the request tenant",
		Handler: func(ctx context.Context, _ tenantInput) (tenantOutput, error) {
			tenant, _ := ctx.Value(tenantKey{}).(string)
			return tenantOutput{Tenant: tenant}, nil
		},
	})

	router := echo.New()
	mcpecho.Mount(router, "/mcp", server, withTenant)

	httpServer := httptest.NewServer(router)
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "hearth-mcpecho-test", Version: "1.0.0"},
		nil,
	).Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL + "/mcp"}, nil)
	if err != nil {
		t.Fatalf("connect MCP client: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := session.Close(); closeErr != nil {
			t.Errorf("close MCP session: %v", closeErr)
		}
	})

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 1 || tools.Tools[0].Name != "current_tenant" {
		t.Fatalf("tools = %#v, want current_tenant", tools.Tools)
	}

	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "current_tenant",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var output tenantOutput
	if unmarshalErr := json.Unmarshal(raw, &output); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if output.Tenant != tenantID {
		t.Fatalf("tenant = %q, want %q", output.Tenant, tenantID)
	}
}

// withTenant is Echo middleware that injects the request tenant into the
// standard request context, the mechanism both HTTP transports share.
func withTenant(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c *echo.Context) error {
		ctx := context.WithValue(c.Request().Context(), tenantKey{}, tenantID)
		c.SetRequest(c.Request().WithContext(ctx))
		return next(c)
	}
}
