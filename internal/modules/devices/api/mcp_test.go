package api //nolint:testpackage // Tests exercise package-private transport mappings and fixtures.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
	"github.com/mholtzscher/hearth/internal/modules/devices"
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
	if text := mcpErrorText(t, result); text != "internal_error: internal error" {
		t.Fatalf("%s error text = %q, want the generic message", tool, text)
	}
	if result.StructuredContent == nil {
		t.Fatalf("%s structured content = nil, want the failure fields", tool)
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), cause) {
		t.Fatalf("%s structured content leaked the cause: %s", tool, raw)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if fields["failure_code"] != "internal_error" {
		t.Fatalf("%s failure_code = %#v, want internal_error", tool, fields["failure_code"])
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
	err := mcpCommandFailure(&devices.CommandExecutionError{
		CommandID: apiCommandID, Err: devices.ErrEntityDisabled,
	})
	var toolError *mcpapi.ToolError
	if !errors.As(err, &toolError) {
		t.Fatalf("mcpCommandFailure = %#v, want a ToolError", err)
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

// TestMCPReadInternalProblemRendersTheGenericHumaProblem proves the cause a read
// retains never changes the REST response: the shared read handlers still return
// the same generic 500 problem document, content type, and schema link, and the
// cause never appears in it.
func TestMCPReadInternalProblemRendersTheGenericHumaProblem(t *testing.T) {
	t.Parallel()
	const cause = "SQLite unavailable: /var/lib/hearth/hearth.db"
	router, _ := testAPI(t, &stubDevices{getEntity: func(
		context.Context,
		devices.EntityID,
	) (devices.EntityWithState, error) {
		return devices.EntityWithState{}, errors.New(cause)
	}})

	response := performRequest(router, "/v1/entities/"+string(apiEntityID))
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	if contentType := response.Header().Get("Content-Type"); contentType != "application/problem+json" {
		t.Fatalf("content type = %q, want application/problem+json", contentType)
	}
	if link := response.Header().Get("Link"); !strings.Contains(link, "ErrorModel.json") {
		t.Fatalf("Link = %q, want the generic problem schema link", link)
	}
	body := response.Body.String()
	for _, want := range []string{
		`"$schema"`, `"title":"Internal Server Error"`, `"status":500`, `"detail":"internal error"`,
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("body = %s, want %s", body, want)
		}
	}
	if strings.Contains(body, "hearth.db") || strings.Contains(body, "SQLite") {
		t.Fatalf("body leaked the cause: %s", body)
	}
}
