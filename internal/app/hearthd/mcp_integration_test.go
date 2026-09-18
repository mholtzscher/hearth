package hearthd //nolint:testpackage // Reuses package-private assembly stubs.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesapi "github.com/mholtzscher/hearth/internal/modules/devices/api"
)

// mcpCatalogToolNames is the full MCP Tool catalog §4 of specs/mcp.md requires:
// the 15 Devices tools and the 8 Automations tools, each mirroring its Huma
// operationId.
func mcpCatalogToolNames() []string {
	return []string{
		"list_entities",
		"get_entity",
		"update_entity",
		"execute_entity_command",
		"list_entity_commands",
		"list_devices",
		"get_device",
		"get_command",
		"list_commands",
		"list_adapters",
		"get_adapter",
		"list_adapter_health_history",
		"list_entity_availability_history",
		"list_entity_state_history",
		"list_entity_events",
		"create_automation",
		"list_automations",
		"get_automation",
		"replace_automation",
		"delete_automation",
		"start_automation_run",
		"list_automation_history",
		"get_automation_history_entry",
	}
}

// connectAssembledMCP serves the assembled Core handler and connects the
// official MCP client to its /mcp endpoint.
func connectAssembledMCP(t *testing.T, handler http.Handler) *mcp.ClientSession {
	t.Helper()
	httpServer := httptest.NewServer(handler)
	t.Cleanup(httpServer.Close)
	session, err := mcp.NewClient(
		&mcp.Implementation{Name: "hearth-hearthd-test", Version: "1.0.0"},
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
	return session
}

// mcpCallTool calls one tool and fails the test on a protocol error.
func mcpCallTool(t *testing.T, session *mcp.ClientSession, name string, arguments map[string]any) *mcp.CallToolResult {
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

// mcpStructuredFields decodes one result's structured content into a generic
// object, the shape a branching agent reads.
func mcpStructuredFields(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	fields := make(map[string]any)
	if unmarshalErr := json.Unmarshal(raw, &fields); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	return fields
}

// mcpErrorText returns the text of one isError result.
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

// decodeMCPLogRecord decodes the single JSON log record in raw.
func decodeMCPLogRecord(t *testing.T, raw []byte) map[string]any {
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

// TestMCPInternalFailureLogsToTheInjectedLogger proves assembly routes MCP
// diagnostics to the application logger it was given: the injected logger
// receives one safe record, the global default receives nothing, and the raw
// cause never reaches either.
//
// This test mutates the global logger, so it deliberately does not run in
// parallel: sequential tests never overlap the package's parallel tests.
//
//nolint:paralleltest // Mutates the process-wide default logger to prove isolation.
func TestMCPInternalFailureLogsToTheInjectedLogger(t *testing.T) {
	const sentinel = "SENTINEL-mcp-secret-DEADBEEF"
	var injected, global bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&global, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	stub := &stubDevices{executeCommand: func(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error) {
		return devices.CommandResult{}, errors.New("upstream rejected: " + sentinel)
	}}
	handler, _, _ := newHTTPHandlerWithMCP(
		stub, &stubAutomations{}, &testReadiness{}, stub, &stubAutomations{},
		slog.New(slog.NewJSONHandler(&injected, nil)),
	)
	session := connectAssembledMCP(t, handler)

	result := mcpCallTool(t, session, "execute_entity_command", map[string]any{
		"entity_id": string(testHTTPEntityID), "operation": "set", "parameters": map[string]any{"value": true},
	})
	if text := mcpErrorText(t, result); text != "internal_error: internal error" {
		t.Fatalf("error text = %q, want the generic internal failure", text)
	}
	if global.Len() != 0 {
		t.Fatalf("global default logger received %s, want none", global.Bytes())
	}
	if strings.Contains(injected.String(), sentinel) || strings.Contains(global.String(), sentinel) {
		t.Fatalf("logs leaked the cause: injected=%s global=%s", injected.Bytes(), global.Bytes())
	}
	record := decodeMCPLogRecord(t, injected.Bytes())
	if record["level"] != "ERROR" || record["event"] != "mcp.tool_internal_failure" ||
		record["tool"] != "execute_entity_command" || record["error_code"] != "internal_error" {
		t.Fatalf("log record = %#v", record)
	}
	if errorType, _ := record["error_type"].(string); errorType == "" {
		t.Fatalf("log error_type = %#v, want a non-empty Go error type name", record["error_type"])
	}
	if _, ok := record["error"]; ok {
		t.Fatalf("log record = %#v, want no raw error field", record)
	}
}

// TestMCPEndpointExposesTheFullCatalog proves the assembled Core HTTP handler
// mounts /mcp on the same listener and registers the whole 23-tool catalog, so
// assembly cannot silently drop a module's tools.
func TestMCPEndpointExposesTheFullCatalog(t *testing.T) {
	t.Parallel()
	handler, _, _ := NewHTTPHandlerWithMCP(
		&stubDevices{}, &stubAutomations{}, &testReadiness{}, &stubDevices{}, &stubAutomations{},
	)
	session := connectAssembledMCP(t, handler)

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != len(mcpCatalogToolNames()) {
		t.Fatalf("tools = %d, want %d", len(listed.Tools), len(mcpCatalogToolNames()))
	}
	registered := make(map[string]*mcp.Tool, len(listed.Tools))
	for _, tool := range listed.Tools {
		registered[tool.Name] = tool
	}
	for _, name := range mcpCatalogToolNames() {
		tool, ok := registered[name]
		if !ok {
			t.Errorf("catalog is missing tool %q", name)
			continue
		}
		if tool.Description == "" {
			t.Errorf("tool %q has no description", name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %q has no input schema", name)
		}
	}
}

// TestMCPEndpointReturnsTypedStructuredOutput proves a successful tool call over
// the assembled endpoint still returns the typed body as structured content.
func TestMCPEndpointReturnsTypedStructuredOutput(t *testing.T) {
	t.Parallel()
	stub := &stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
		return devices.EntityWithState{Entity: devices.Entity{
			ID: testHTTPEntityID, DeviceID: testHTTPDeviceID, AdapterID: "simulator", Name: "Power",
			TypeID: devices.EntityTypePowerV1, Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
		}}, nil
	}}
	handler, _, _ := NewHTTPHandlerWithMCP(
		stub, &stubAutomations{}, &testReadiness{}, stub, &stubAutomations{},
	)
	session := connectAssembledMCP(t, handler)

	result := mcpCallTool(t, session, "get_entity", map[string]any{"entity_id": string(testHTTPEntityID)})
	if result.IsError {
		t.Fatalf("result = %#v, want a successful tool result", result)
	}
	var body devicesapi.EntityBody
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if unmarshalErr := json.Unmarshal(raw, &body); unmarshalErr != nil {
		t.Fatalf("unmarshal structured content %s: %v", raw, unmarshalErr)
	}
	if body.ID != string(testHTTPEntityID) || body.Name != "Power" {
		t.Fatalf("entity body = %#v, want the typed device body", body)
	}
}

// TestMCPEndpointMapsToolErrorToStructuredFailure proves a domain ToolError
// crosses the assembled endpoint as an isError result carrying both the stable
// human text and the machine-readable failure_code and Command status.
func TestMCPEndpointMapsToolErrorToStructuredFailure(t *testing.T) {
	t.Parallel()
	const commandID = devices.CommandID("cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	stub := &stubDevices{executeCommand: func(
		context.Context,
		devices.CommandInput,
	) (devices.CommandResult, error) {
		return devices.CommandResult{}, &devices.CommandExecutionError{
			CommandID: commandID, Err: devices.ErrEntityDisabled,
		}
	}}
	handler, _, _ := NewHTTPHandlerWithMCP(
		stub, &stubAutomations{}, &testReadiness{}, stub, &stubAutomations{},
	)
	session := connectAssembledMCP(t, handler)

	result := mcpCallTool(t, session, "execute_entity_command", map[string]any{
		"entity_id": string(testHTTPEntityID), "operation": "set", "parameters": map[string]any{"value": true},
	})
	text := mcpErrorText(t, result)
	if text != "entity_disabled: entity is disabled" {
		t.Fatalf("error text = %q, want the stable failure text", text)
	}
	fields := mcpStructuredFields(t, result)
	if fields["failure_code"] != "entity_disabled" {
		t.Fatalf("failure_code = %#v, want entity_disabled", fields["failure_code"])
	}
	if fields["status"] != string(devices.CommandStatusEntityDisabled) {
		t.Fatalf("status = %#v, want the durable Command status", fields["status"])
	}
	if fields["command_id"] != string(commandID) {
		t.Fatalf("command_id = %#v, want the durable Command ID", fields["command_id"])
	}
}

// TestMCPEndpointRejectsInvalidInputBeforeTheHandler proves the SDK still
// validates arguments against the derived schema and never reaches the service,
// and that automatic rejection publishes no structured error payload.
func TestMCPEndpointRejectsInvalidInputBeforeTheHandler(t *testing.T) {
	t.Parallel()
	// stubDevices panics on any ExecuteCommand call, so a handler that ran for
	// invalid input fails the test instead of reaching the service.
	handler, _, _ := NewHTTPHandlerWithMCP(
		&stubDevices{}, &stubAutomations{}, &testReadiness{}, &stubDevices{}, &stubAutomations{},
	)
	session := connectAssembledMCP(t, handler)

	result := mcpCallTool(t, session, "execute_entity_command", map[string]any{
		"entity_id": string(testHTTPEntityID), "parameters": map[string]any{"value": true},
	})
	if text := mcpErrorText(t, result); text == "" {
		t.Fatal("invalid input error text is empty")
	}
	if result.StructuredContent != nil {
		t.Fatalf("structured content = %#v, want none for invalid input", result.StructuredContent)
	}
}
