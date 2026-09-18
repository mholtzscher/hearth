package mcpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// advertisedOutputSchema returns the compiled output schema the client sees for
// name, so a test can validate a result's structured content against the
// contract the server publishes.
func advertisedOutputSchema(t *testing.T, session *mcp.ClientSession, name string) *jsonschema.Schema {
	t.Helper()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name != name {
			continue
		}
		raw, marshalErr := json.Marshal(tool.OutputSchema)
		if marshalErr != nil {
			t.Fatalf("marshal output schema: %v", marshalErr)
		}
		document, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if decodeErr != nil {
			t.Fatalf("decode output schema %s: %v", raw, decodeErr)
		}
		compiler := jsonschema.NewCompiler()
		if addErr := compiler.AddResource("urn:hearth:mcp:tool-output", document); addErr != nil {
			t.Fatalf("add output schema: %v", addErr)
		}
		compiled, compileErr := compiler.Compile("urn:hearth:mcp:tool-output")
		if compileErr != nil {
			t.Fatalf("compile output schema %s: %v", raw, compileErr)
		}
		return compiled
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}

// validateStructured checks one result's structured content against schema.
func validateStructured(t *testing.T, schema *jsonschema.Schema, result *mcp.CallToolResult) error {
	t.Helper()
	raw, err := json.Marshal(result.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatalf("decode structured content %s: %v", raw, err)
	}
	return schema.Validate(value)
}

// TestOutputSchemaRootIsObject proves every advertised output schema carries a
// top-level type of object. Strict clients (the Inspector, the Pi MCP gateway)
// drop tools whose output schema root has no object type; before the pin all
// 23 production tools were dropped for exactly this reason.
func TestOutputSchemaRootIsObject(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: discardLogger()})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(_ context.Context, input greetInput) (greetOutput, error) {
			return greetOutput{Greeting: "hello " + input.Name}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) == 0 {
		t.Fatal("no tools listed")
	}
	for _, tool := range listed.Tools {
		raw, marshalErr := json.Marshal(tool.OutputSchema)
		if marshalErr != nil {
			t.Fatalf("marshal output schema for %q: %v", tool.Name, marshalErr)
		}
		var document map[string]any
		if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
			t.Fatalf("decode output schema for %q: %v", tool.Name, decodeErr)
		}
		if document["type"] != "object" {
			t.Errorf("tool %q output schema root type = %v, want %q", tool.Name, document["type"], "object")
		}
	}
}

// TestOutputSchemaAcceptsSuccessAndStructuredFailure proves the schema a tool
// advertises is the union of the success body and the structured failure
// object, so both a success result and a ToolError result validate against it.
func TestOutputSchemaAcceptsSuccessAndStructuredFailure(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: discardLogger()})
	mcpapi.Register(server, mcpapi.Tool[greetInput, greetOutput]{
		Name:        "greet",
		Description: "Greet one person",
		Handler: func(_ context.Context, input greetInput) (greetOutput, error) {
			if input.Name == "fail" {
				return greetOutput{}, &mcpapi.ToolError{
					Code:    "entity_disabled",
					Message: "entity is disabled",
					Details: map[string]any{"status": "entity_disabled", "command_id": "cmd_1"},
				}
			}
			return greetOutput{Greeting: "hello " + input.Name}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	schema := advertisedOutputSchema(t, session, "greet")

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "greet", Arguments: map[string]any{"name": "world"},
		})
		if err != nil {
			t.Fatalf("call tool: %v", err)
		}
		if validationErr := validateStructured(t, schema, result); validationErr != nil {
			t.Fatalf("success structured content does not match the advertised schema: %v", validationErr)
		}
	})

	t.Run("structured failure", func(t *testing.T) {
		t.Parallel()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "greet", Arguments: map[string]any{"name": "fail"},
		})
		if err != nil {
			t.Fatalf("call tool: %v", err)
		}
		if !result.IsError {
			t.Fatalf("result = %#v, want an isError result", result)
		}
		if validationErr := validateStructured(t, schema, result); validationErr != nil {
			t.Fatalf("failure structured content does not match the advertised schema: %v", validationErr)
		}
	})

	t.Run("failure without the required code is rejected", func(t *testing.T) {
		t.Parallel()
		// A negative control: the union must still constrain the failure shape,
		// so the validator is discriminating rather than permissive.
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader([]byte(`{"message":"missing the code"}`)))
		if err != nil {
			t.Fatalf("decode value: %v", err)
		}
		if validationErr := schema.Validate(value); validationErr == nil {
			t.Fatal("schema accepted a failure without failure_code")
		}
	})
}
