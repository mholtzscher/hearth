//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

type handlerValidatedInput struct {
	Label    string         `json:"label"`
	Document map[string]any `json:"document"`
}

// TestHandlerValidatedPropertyIsAdvertisedButOwnedByTheHandler proves a
// handler-validated property keeps its canonical schema in tools/list, while
// the SDK checks only that it is an object and every other argument as usual.
func TestHandlerValidatedPropertyIsAdvertisedButOwnedByTheHandler(t *testing.T) {
	t.Parallel()
	canonical := json.RawMessage(
		`{"type":"object","required":["kind"],"additionalProperties":false,"properties":{"kind":{"const":"lamp"}}}`,
	)
	schema, err := mcpapi.InputSchemaWithProperty[handlerValidatedInput]("document", canonical)
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[handlerValidatedInput, greetOutput]{
		Name:             "save",
		InputSchema:      schema,
		HandlerValidated: []string{"document"},
		Handler: func(_ context.Context, request *mcp.CallToolRequest, input handlerValidatedInput) (greetOutput, error) {
			calls.Add(1)
			if !strings.Contains(string(request.Params.Arguments), `"document"`) || input.Label != "office" {
				t.Errorf("handler input = %+v, arguments = %s", input, request.Params.Arguments)
			}
			return greetOutput{}, &mcpapi.ToolError{Code: "document_invalid", Message: "document rejected by its owner"}
		},
	})
	session := connectSession(t, server.HTTPHandler())

	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	advertised, err := json.Marshal(listed.Tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(advertised), `"const":"lamp"`) {
		t.Fatalf("advertised schema lost the canonical document: %s", advertised)
	}

	owned, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name:      "save",
		Arguments: map[string]any{"label": "office", "document": map[string]any{"kind": "fan", "extra": true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	structured, _ := owned.StructuredContent.(map[string]any)
	if !owned.IsError || structured["failure_code"] != "document_invalid" || calls.Load() != 1 {
		t.Fatalf("handler did not own the canonical failure: %+v (calls %d)", owned, calls.Load())
	}

	for name, arguments := range map[string]map[string]any{
		"non-object property": {"label": "office", "document": "lamp"},
		"missing property":    {"label": "office"},
		"other argument type": {"label": 7, "document": map[string]any{}},
		"unknown argument":    {"label": "office", "document": map[string]any{}, "extra": true},
	} {
		result, callErr := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "save", Arguments: arguments})
		if callErr != nil {
			t.Fatalf("%s: %v", name, callErr)
		}
		if !result.IsError {
			t.Fatalf("%s: accepted invalid arguments", name)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("handler calls = %d, want only the call with valid envelope arguments", calls.Load())
	}
}
