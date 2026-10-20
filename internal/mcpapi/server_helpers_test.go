package mcpapi_test

import (
	"encoding/json"
	"log/slog"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// discardLogger returns a logger that swallows records, so a test does not print
// the diagnostic it asserts elsewhere.
func discardLogger() *slog.Logger {
	return slog.New(slog.DiscardHandler)
}

// decodeStructured round-trips a result's structured content into target, the
// way a typed client recovers its output value.
func decodeStructured(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if unmarshalErr := json.Unmarshal(raw, target); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
}

// firstText returns the text of the first text content block in result.
func firstText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("result has no text content: %#v", result.Content)
	return ""
}
