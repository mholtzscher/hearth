package mcpapi //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func mcpLogRecord(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	line, _, _ := bytes.Cut(bytes.TrimSpace(raw), []byte("\n"))
	if len(line) == 0 {
		t.Fatal("no log record was written")
	}
	var record map[string]any
	if err := json.Unmarshal(line, &record); err != nil {
		t.Fatalf("decode log record %s: %v", line, err)
	}
	return record
}

func mcpCallTool(
	t *testing.T,
	session *mcp.ClientSession,
	name string,
	arguments map[string]any,
) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatalf("call %s: %v", name, err)
	}
	if result == nil {
		t.Fatalf("call %s returned no result", name)
	}
	return result
}

func mcpBody(t *testing.T, result *mcp.CallToolResult, target any) {
	t.Helper()
	if result.IsError {
		t.Fatalf("result = %#v, want a successful tool result", result)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if unmarshalErr := json.Unmarshal(raw, target); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
}

func mcpErrorText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if !result.IsError {
		t.Fatalf("result = %#v, want an isError tool result", result)
	}
	for _, content := range result.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	t.Fatalf("error result has no text content: %#v", result.Content)
	return ""
}

func mcpFailureFields(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	text := mcpErrorText(t, result)
	if len(result.Content) != 1 {
		t.Fatalf("error content = %#v, want one text item", result.Content)
	}
	var fields map[string]any
	if err := json.Unmarshal([]byte(text), &fields); err != nil || fields == nil {
		t.Fatalf("error text is not a JSON object: %q (%v)", text, err)
	}
	if !reflect.DeepEqual(fields, result.StructuredContent) {
		t.Fatalf("error text %q differs from structured content %#v", text, result.StructuredContent)
	}
	return fields
}

// mcpAssertInternalFailureLogged proves one tool call failed the way an
// unpublished internal failure must: the client receives only the generic
// internal_error result with no server detail, and the server records exactly one
// structured diagnostic with the fixed code and Go error type but never the
// retained cause text.
func mcpAssertInternalFailureLogged(
	t *testing.T,
	result *mcp.CallToolResult,
	logs *bytes.Buffer,
	tool string,
	cause string,
) {
	t.Helper()
	fields := mcpFailureFields(t, result)
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), cause) {
		t.Fatalf("%s structured content leaked the cause: %s", tool, raw)
	}
	if len(fields) != 2 || fields["failure_code"] != "internal_error" || fields["message"] != "internal error" {
		t.Fatalf("%s failure = %#v, want generic internal error", tool, fields)
	}

	record := mcpLogRecord(t, logs.Bytes())
	if record["level"] != "ERROR" || record["event"] != "mcp.tool_internal_failure" ||
		record["tool"] != tool {
		t.Fatalf("%s log record = %#v", tool, record)
	}
	if record["error_code"] != "internal_error" {
		t.Fatalf("%s log error_code = %#v, want internal_error", tool, record["error_code"])
	}
	if errorType, _ := record["error_type"].(string); errorType == "" {
		t.Fatalf("%s log error_type = %#v, want a non-empty Go error type name", tool, record["error_type"])
	}
	if _, ok := record["error"]; ok {
		t.Fatalf("%s log record = %#v, want no raw error field", tool, record)
	}
	if strings.Contains(logs.String(), cause) {
		t.Fatalf("%s logs leaked the cause: %s", tool, logs.Bytes())
	}
}

// TestMCPCommandFailureCarriesStructuredCodeAndStatus proves the tool error for a
// refused Command preserves the durable failure code, Command status, and
// Command ID that clients receive as structured content.
func TestMCPCommandFailureCarriesStructuredCodeAndStatus(t *testing.T) {
	t.Parallel()
	err := commandFailure(&devices.CommandExecutionError{
		CommandID: apiCommandID, Err: devices.ErrEntityDisabled,
	})
	var toolError *mcptools.ToolError
	if !errors.As(err, &toolError) {
		t.Fatalf("commandFailure = %#v, want a ToolError", err)
	}
	if toolError.Code != "entity_disabled" || toolError.Message != "entity is disabled" {
		t.Fatalf("tool error = %#v", toolError)
	}
	if toolError.Details["status"] != string(devices.CommandStatusEntityDisabled) {
		t.Fatalf("tool error status = %#v", toolError.Details)
	}
	if toolError.Details["command_id"] != string(apiCommandID) {
		t.Fatalf("tool error command_id = %#v", toolError.Details)
	}
}
