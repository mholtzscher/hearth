package mcpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestHTTPHandlerRetainsSDKRequestBodyLimit(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "body-limit", Version: "1"})
	calls := 0
	mcpapi.Register(server, mcpapi.Tool[noInput, exactOutput]{
		Name: "bounded",
		Handler: func(context.Context, noInput) (exactOutput, error) {
			calls++
			return exactOutput{}, nil
		},
	})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"bounded","arguments":{` +
		strings.Repeat(" ", mcp.DefaultMaxRequestBodyBytes) + `}}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	if response.Code != http.StatusRequestEntityTooLarge || calls != 0 {
		t.Fatalf("status=%d calls=%d, want 413 without handler entry", response.Code, calls)
	}
}
