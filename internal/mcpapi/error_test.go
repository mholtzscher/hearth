package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// sensitiveSentinel stands in for a secret or rejected value an unknown error
// string could embed; every diagnostic test proves it never reaches the log.
const sensitiveSentinel = "SENTINEL-secret-value-DEADBEEF"

// TestUnmodelledFailureIsLoggedWithSafeMetadata proves a handler failure the
// wrapper does not model keeps its fixed failure code, Go error type, and the
// invoked tool name in one structured server record, while neither the client
// nor the log sees the raw cause.
func TestUnmodelledFailureIsLoggedWithSafeMetadata(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: logger})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, errors.New("SQLite unavailable: " + sensitiveSentinel)
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if text := firstText(t, result); text != "internal error" {
		t.Fatalf("error text = %q, want the generic message", text)
	}

	record := decodeLogRecord(t, logs.Bytes())
	if record["level"] != "ERROR" {
		t.Fatalf("log level = %#v, want ERROR", record["level"])
	}
	if record["event"] != "mcp.tool_internal_failure" {
		t.Fatalf("log event = %#v, want mcp.tool_internal_failure", record["event"])
	}
	if record["tool"] != "greet" {
		t.Fatalf("log tool = %#v, want the invoked tool name", record["tool"])
	}
	if record["error_code"] != "unexpected_error" {
		t.Fatalf("log error_code = %#v, want the fixed unexpected_error code", record["error_code"])
	}
	assertSafeErrorType(t, record)
	assertCauseAbsent(t, logs.Bytes())
}

// TestDomainFailureIsNotLoggedAsInternal proves an expected domain failure stays
// silent in the diagnostic log: the client branches on it, so it is not a
// server fault.
func TestDomainFailureIsNotLoggedAsInternal(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: logger})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, &mcpapi.ToolError{Code: "entity_disabled", Message: "entity is disabled"}
		},
	})

	session := connectSession(t, server.HTTPHandler())
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	}); err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if logs.Len() != 0 {
		t.Fatalf("logs = %s, want none for a domain failure", logs.Bytes())
	}
}

// TestInternalToolFailureIsLoggedWithSafeMetadata proves an internal failure
// whose handler retains its cause records only the fixed failure code, Go error
// type, and tool name while the client sees only the generic message and
// machine-readable failure fields. The retained cause stays reachable to the
// server but is never logged as text.
func TestInternalToolFailureIsLoggedWithSafeMetadata(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: logger})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, (&mcpapi.ToolError{
				Code: mcpapi.CodeInternalError, Message: "internal error",
			}).WithCause(errors.New("SQLite unavailable: " + sensitiveSentinel))
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if text := firstText(t, result); text != "internal_error: internal error" {
		t.Fatalf("error text = %q, want the generic message", text)
	}
	assertInternalFailureContentHidesCause(t, result)

	record := decodeLogRecord(t, logs.Bytes())
	if record["level"] != "ERROR" {
		t.Fatalf("log level = %#v, want ERROR", record["level"])
	}
	if record["event"] != "mcp.tool_internal_failure" {
		t.Fatalf("log event = %#v, want mcp.tool_internal_failure", record["event"])
	}
	if record["tool"] != "greet" {
		t.Fatalf("log tool = %#v, want the invoked tool name", record["tool"])
	}
	if record["error_code"] != "internal_error" {
		t.Fatalf("log error_code = %#v, want the fixed internal_error code", record["error_code"])
	}
	assertSafeErrorType(t, record)
	assertCauseAbsent(t, logs.Bytes())
}

// TestInternalToolFailureWithoutCauseIsNotLogged proves an internal failure
// with no retained cause stays silent in the diagnostic log: there is no
// server-side detail to report, and inventing one is not the wrapper's job.
func TestInternalToolFailureWithoutCauseIsNotLogged(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&logs, nil))
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: logger})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(context.Context, greetInput) (greetOutput, error) {
			return greetOutput{}, &mcpapi.ToolError{Code: mcpapi.CodeInternalError, Message: "internal error"}
		},
	})

	session := connectSession(t, server.HTTPHandler())
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "greet",
		Arguments: map[string]any{"name": "world"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if text := firstText(t, result); text != "internal_error: internal error" {
		t.Fatalf("error text = %q, want the generic message", text)
	}
	if logs.Len() != 0 {
		t.Fatalf("logs = %s, want none without a retained cause", logs.Bytes())
	}
}

// assertInternalFailureContentHidesCause proves the generic internal failure
// still publishes its machine-readable fields while leaking no server detail.
func assertInternalFailureContentHidesCause(t *testing.T, result *mcp.CallToolResult) {
	t.Helper()
	if result.StructuredContent == nil {
		t.Fatal("structured content = nil, want the failure fields")
	}
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if strings.Contains(string(raw), sensitiveSentinel) ||
		strings.Contains(firstText(t, result), sensitiveSentinel) {
		t.Fatalf("result leaked server detail: %s", raw)
	}
	var fields map[string]any
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if fields["failure_code"] != "internal_error" {
		t.Fatalf("failure_code = %#v, want internal_error", fields["failure_code"])
	}
}

// assertSafeErrorType proves one record carries a non-empty Go error type name
// instead of the raw cause.
func assertSafeErrorType(t *testing.T, record map[string]any) {
	t.Helper()
	errorType, ok := record["error_type"].(string)
	if !ok || errorType == "" {
		t.Fatalf("log error_type = %#v, want a non-empty Go error type name", record["error_type"])
	}
}

// assertCauseAbsent proves no log byte carries the sensitive sentinel and no
// raw error field was serialized at all.
func assertCauseAbsent(t *testing.T, raw []byte) {
	t.Helper()
	if strings.Contains(string(raw), sensitiveSentinel) {
		t.Fatalf("logs leaked the cause: %s", raw)
	}
	record := decodeLogRecord(t, raw)
	if _, ok := record["error"]; ok {
		t.Fatalf("log record = %#v, want no raw error field", record)
	}
}

// decodeLogRecord decodes the single JSON log record in raw.
func decodeLogRecord(t *testing.T, raw []byte) map[string]any {
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
