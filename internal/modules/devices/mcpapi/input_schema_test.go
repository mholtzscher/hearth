package mcpapi //nolint:testpackage // Exercise operation inputs through the registered catalog.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

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

func TestHistoryToolPublishedSchemaRejectsNullBucket(t *testing.T) {
	t.Parallel()
	doc := listedDeviceInputSchemas(t)["query_entity_state_history"]
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(string(doc)))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://hearth.invalid/tool", value))
	schema, err := compiler.Compile("https://hearth.invalid/tool")
	require.NoError(t, err)
	base := `{"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"select_field":"","result":{"kind":"summary","metrics":{"min":{"operation":"min"}}}}`
	for _, tc := range []struct {
		body  string
		valid bool
	}{
		{base, true},
		{strings.Replace(base, `"metrics":{`, `"bucket":null,"metrics":{`, 1), false},
		{strings.Replace(base, `"metrics":{`, `"nulls":null,"metrics":{`, 1), false},
		{strings.Replace(base, `"operation":"min"`, `"Operation":"min"`, 1), false},
		{strings.Replace(base, `"metrics":{"min":{"operation":"min"}}`, `"metrics":{}`, 1), false},
	} {
		instance, decodeErr := jsonschema.UnmarshalJSON(strings.NewReader(tc.body))
		require.NoError(t, decodeErr)
		require.Equal(t, tc.valid, schema.Validate(instance) == nil, tc.body)
	}
}

func TestPresentationToolsPublishUnitsArgument(t *testing.T) {
	t.Parallel()
	inputs := listedDeviceInputSchemas(t)
	for _, name := range []string{"get_entity", "get_device", "list_entities", "query_entity_state_history"} {
		var schema struct {
			Properties map[string]map[string]any `json:"properties"`
			Required   []string                  `json:"required"`
		}
		if err := json.Unmarshal(inputs[name], &schema); err != nil {
			t.Fatal(err)
		}
		units := schema.Properties["units"]
		if units["type"] != "string" || !reflect.DeepEqual(units["enum"], []any{"", "canonical"}) {
			t.Fatalf("%s units = %#v", name, units)
		}
		for _, field := range schema.Required {
			if field == "units" {
				t.Fatalf("%s requires units", name)
			}
		}
	}
}

func listedDeviceInputSchemas(t *testing.T) map[string]json.RawMessage {
	t.Helper()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, &stubDevices{}, testPresenter())
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
	if len(envelope.Result.Tools) != 13 {
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
