package mcpapi //nolint:testpackage // Exercise operation inputs through the registered catalog.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestInputSchemasPreserveOptionalIDsAndPageArguments(t *testing.T) {
	t.Parallel()
	inputs := listedDeviceInputSchemas(t)
	for _, test := range []struct {
		tool, field, description string
	}{
		{"list_entities", "device_id", "canonical Hearth Device ID; omit to list every Device"},
		{"list_commands", "entity_id", "canonical Hearth Entity ID; omit to list every Entity's Commands"},
	} {
		t.Run(test.tool, func(t *testing.T) {
			t.Parallel()
			var schema struct {
				Properties map[string]map[string]any `json:"properties"`
			}
			if decodeErr := json.Unmarshal(inputs[test.tool], &schema); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			wantID := map[string]any{
				"description": test.description,
				"anyOf":       []any{map[string]any{"type": "null"}, map[string]any{"type": "string"}},
			}
			if !reflect.DeepEqual(schema.Properties[test.field], wantID) {
				t.Fatalf("%s = %#v, want %#v", test.field, schema.Properties[test.field], wantID)
			}
			assertPageArgumentSchemas(t, schema.Properties)
		})
	}
}

func listedDeviceInputSchemas(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, &stubDevices{})
	request := httptest.NewRequest(
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	_, data, found := strings.Cut(response.Body.String(), "data: ")
	if response.Code != http.StatusOK || !found {
		t.Fatalf("response = %d: %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Result.Tools) != 12 {
		t.Fatalf("catalog has %d tools", len(envelope.Result.Tools))
	}
	inputs := make(map[string]json.RawMessage)
	for _, tool := range envelope.Result.Tools {
		inputs[tool.Name] = tool.InputSchema
	}
	return inputs
}

func assertPageArgumentSchemas(t *testing.T, properties map[string]map[string]any) {
	t.Helper()
	limit := properties["limit"]
	if limit["type"] != "integer" || limit["description"] != "page size from 1 to 200; defaults to 50" {
		t.Fatalf("limit = %#v", limit)
	}
	for _, constraint := range []string{"minimum", "maximum", "default"} {
		if _, present := limit[constraint]; present {
			t.Fatalf("limit unexpectedly advertises %s", constraint)
		}
	}
	if properties["cursor"]["type"] != "string" ||
		properties["cursor"]["description"] != "opaque cursor from a previous page's next_cursor" {
		t.Fatalf("cursor = %#v", properties["cursor"])
	}
}
