//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type automationWireResult struct {
	IsError    bool            `json:"isError"`
	Structured json.RawMessage `json:"structuredContent"`
	Content    []struct {
		Text string `json:"text"`
	} `json:"content"`
}

// Raw HTTP avoids the official client's float-based request inspection and
// result decoding, neither of which defines the server's exact JSON contract.
func callAutomationWireTool(t *testing.T, server *mcptools.Server, name, arguments string) automationWireResult {
	t.Helper()
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + name + `","arguments":` + arguments + `}}`
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	_, data, ok := strings.Cut(response.Body.String(), "data: ")
	if !ok || response.Code != http.StatusOK {
		t.Fatalf("response: %s", response.Body.String())
	}
	var wire struct {
		Result automationWireResult `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire.Result.Content) != 1 || wire.Result.Content[0].Text != string(wire.Result.Structured) {
		t.Fatalf("wire text and structured content differ: %s", data)
	}
	return wire.Result
}
