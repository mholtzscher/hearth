package mcpapi //nolint:testpackage // Pin independently owned MCP argument documents.

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// Contract fixtures are explicit JSON, independent of the schema-building type.
// Switching builders must not change published defaults, bounds, or required fields.
func TestFunctionInputDocuments(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, nil, &stubFunctions{})
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(
		`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`,
	))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	request.Header.Set("Mcp-Protocol-Version", "2025-11-25")
	response := httptest.NewRecorder()
	server.HTTPHandler().ServeHTTP(response, request)
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string          `json:"name"`
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	for line := range strings.SplitSeq(response.Body.String(), "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok {
			if err := json.Unmarshal([]byte(data), &envelope); err != nil {
				t.Fatal(err)
			}
		}
	}
	documents := make(map[string]json.RawMessage)
	for _, tool := range envelope.Result.Tools {
		documents[tool.Name] = tool.InputSchema
	}
	for _, test := range []struct {
		name string
		want string
	}{
		{
			"list_adapter_functions",
			`{"type":"object","additionalProperties":{"type":"null","not":{"type":"null"}},"properties":{"adapter_id":{"type":"string"},"cursor":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}},"required":["adapter_id"]}`,
		},
		{
			"get_adapter_function",
			`{"type":"object","additionalProperties":{"type":"null","not":{"type":"null"}},"properties":{"adapter_id":{"type":"string"},"function_key":{"type":"string"}},"required":["adapter_id","function_key"]}`,
		},
		{
			"invoke_adapter_function",
			`{"type":"object","additionalProperties":{"type":"null","not":{"type":"null"}},"properties":{"adapter_id":{"type":"string"},"function_key":{"type":"string"},"catalog_revision":{"type":"string"},"arguments":{"type":"object"}},"required":["adapter_id","function_key","catalog_revision","arguments"]}`,
		},
		{
			"list_adapter_function_invocations",
			`{"type":"object","additionalProperties":{"type":"null","not":{"type":"null"}},"properties":{"adapter_id":{"type":"string"},"cursor":{"type":"string"},"function_key":{"type":"string"},"limit":{"type":"integer","minimum":1,"maximum":200,"default":50}},"required":["adapter_id"]}`,
		},
		{
			"get_adapter_function_invocation",
			`{"type":"object","additionalProperties":{"type":"null","not":{"type":"null"}},"properties":{"invocation_id":{"type":"string"}},"required":["invocation_id"]}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, ok := documents[test.name]
			if !ok {
				t.Fatalf("tool %s missing from %s", test.name, response.Body.String())
			}
			var got, want any
			if err := json.Unmarshal(raw, &got); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal([]byte(test.want), &want); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("input document = %s, want %s", raw, test.want)
			}
		})
	}
}
