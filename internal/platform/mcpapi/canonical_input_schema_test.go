package mcpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type canonicalInput struct {
	Definition json.RawMessage `json:"definition"`
}

func TestCanonicalInputSchemaOwnsExactValuesBeforeRegistration(t *testing.T) {
	t.Parallel()
	raw := json.RawMessage(
		`{"type":"object","properties":{"integer":{"const":9007199254740993},"decimal":{"const":1.0000000000000001},"nullable":{"const":null}},"required":["integer","decimal","nullable"],"additionalProperties":false}`,
	)
	schema, schemaErr := mcpapi.InputSchemaWithProperty[canonicalInput]("definition", raw)
	if schemaErr != nil {
		t.Fatal(schemaErr)
	}
	for index := range raw {
		raw[index] = ' '
	}
	server := mcpapi.New(mcpapi.Config{Name: "canonical", Version: "1"})
	mcpapi.Register(server, mcpapi.Tool[canonicalInput, greetOutput]{
		Name: "canonical", InputSchema: schema,
		Handler: func(context.Context, canonicalInput) (greetOutput, error) {
			return greetOutput{Greeting: "unused"}, nil
		},
	})
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
				InputSchema json.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(data)), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Result.Tools) != 1 {
		t.Fatalf("tools = %d", len(envelope.Result.Tools))
	}
	node := envelope.Result.Tools[0].InputSchema
	for _, key := range []string{"properties", "definition", "properties"} {
		var object map[string]json.RawMessage
		if err := json.Unmarshal(node, &object); err != nil {
			t.Fatal(err)
		}
		node = object[key]
	}
	var properties map[string]struct {
		Const json.RawMessage `json:"const"`
	}
	if err := json.Unmarshal(node, &properties); err != nil {
		t.Fatal(err)
	}
	for name, want := range map[string]string{"integer": "9007199254740993", "decimal": "1.0000000000000001", "nullable": "null"} {
		if got := string(properties[name].Const); got != want {
			t.Errorf("%s const = %s, want %s", name, got, want)
		}
	}
}
