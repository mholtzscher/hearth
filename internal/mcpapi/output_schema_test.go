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
// name, so a test can validate a result against the contract the server
// publishes.
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
// drop tools whose output schema root has no object type; before the pin all 23
// production tools were dropped for exactly this reason.
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

// TestOutputSchemaAcceptsSuccessAndStructuredFailure proves the advertised
// schema is the union of the success body and the structured failure object, so
// both a success result and a ToolError result validate against it.
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

// An untyped recursive output must obey the advertised success contract while
// retaining the wrapper's failure contract through every registration path.
func TestOutputSchemaOverrideValidatesRecursiveOutputs(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name           string
		request, exact bool
	}{
		{name: "typed"},
		{name: "request", request: true},
		{name: "exact", exact: true},
		{name: "exact-request", request: true, exact: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			server := mcpapi.New(mcpapi.Config{Name: "override", Version: "1", Logger: discardLogger()})
			schema := recursiveOutputSchema(t)
			// Permit null in the supplied success contract; the wrapper must still
			// enforce the object root it advertises for every MCP result.
			schema["type"] = []string{"object", "null"}
			schema["$defs"].(map[string]any)["node"].(map[string]any)["type"] = []string{"object", "null"}
			handler := func(_ context.Context, input greetInput) (any, error) {
				switch input.Name {
				case "fail":
					return nil, &mcpapi.ToolError{Code: "refused", Message: "refused"}
				case "invalid":
					return json.RawMessage(`{"value":1,"children":[{"value":"bad"}]}`), nil
				case "failure-shaped":
					return json.RawMessage(`{"failure_code":"refused","message":"refused"}`), nil
				case "nil":
					return nil, nil //nolint:nilnil // Deliberately invalid success must fail output validation.
				default:
					return json.RawMessage(`{"value":7,"children":[{"value":null}]}`), nil
				}
			}
			if test.request {
				mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[greetInput, any]{
					Name: "tree", OutputSchema: schema, ExactOutput: test.exact,
					Handler: func(ctx context.Context, _ *mcp.CallToolRequest, input greetInput) (any, error) {
						return handler(ctx, input)
					},
				})
			} else {
				mcpapi.Register(server, mcpapi.Tool[greetInput, any]{
					Name: "tree", OutputSchema: schema, ExactOutput: test.exact, Handler: handler,
				})
			}
			session := connectSession(t, server.HTTPHandler())
			advertised := advertisedOutputSchema(t, session, "tree")
			assertRecursiveOutputContract(t, session, advertised)
			// Registration must not relocate references in caller-owned schema data.
			if schema["$ref"] != "#/$defs/node" {
				t.Fatalf("registration changed caller schema: %#v", schema)
			}
		})
	}
}

func assertRecursiveOutputContract(t *testing.T, session *mcp.ClientSession, advertised *jsonschema.Schema) {
	t.Helper()
	for _, input := range []string{"ok", "fail", "invalid", "failure-shaped", "nil"} {
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "tree", Arguments: map[string]any{"name": input},
		})
		if err != nil {
			t.Fatalf("call %s: %v", input, err)
		}
		if result.IsError != (input != "ok") {
			t.Fatalf("call %s returned unexpected result: %+v", input, result)
		}
		if err = validateStructured(t, advertised, result); err != nil {
			t.Fatalf("call %s violates published contract: %v", input, err)
		}
	}
}

func recursiveOutputSchema(t *testing.T) map[string]any {
	t.Helper()
	var schema map[string]any
	if err := json.Unmarshal([]byte(`{
		"type":"object", "$ref":"#/$defs/node",
		"$defs":{"node":{
			"type":"object", "additionalProperties":false, "required":["value"],
			"properties":{
				"value":{"type":["integer","null"]},
				"children":{"type":"array","items":{"$ref":"#/$defs/node"}}
			}
		}}
	}`), &schema); err != nil {
		t.Fatal(err)
	}
	return schema
}

func TestOutputSchemaOverrideRejectsInvalidSchemaAtRegistration(t *testing.T) {
	t.Parallel()
	for _, exact := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "exact"}[exact], func(t *testing.T) {
			t.Parallel()
			defer func() {
				if recover() == nil {
					t.Fatal("registered an output schema with an unresolved local reference")
				}
			}()
			server := mcpapi.New(mcpapi.Config{Name: "invalid-schema", Version: "1"})
			mcpapi.Register(server, mcpapi.Tool[noInput, any]{
				Name: "invalid", ExactOutput: exact,
				OutputSchema: map[string]any{"type": "object", "$ref": "#/$defs/missing"},
				Handler:      func(context.Context, noInput) (any, error) { return struct{}{}, nil },
			})
		})
	}
}
