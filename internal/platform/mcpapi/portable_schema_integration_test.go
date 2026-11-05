//go:build integration

package mcpapi_test

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

// TestRegisteredToolSchemasArePortable proves every schema the wrapper registers
// reaches a client free of the two shapes strict MCP clients reject: a boolean
// subschema node and an array-valued `type`.
//
// It inspects the schemas tools/list actually publishes and registers one tool
// for each supported authoring convention: ordinary derived fields, a type
// schema hook, and a concrete open-object type.
func TestRegisteredToolSchemasArePortable(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[portabilityDerivedInput, portabilityDerivedOutput]{
		Name:        "derived",
		Description: "Derived schemas",
		Handler: func(context.Context, portabilityDerivedInput) (portabilityDerivedOutput, error) {
			return portabilityDerivedOutput{}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[contractInput[portabilityContract], contractOutput[openObjectContract]]{
		Name:        "hooked",
		Description: "Explicit argument schema",
		Handler: func(context.Context, contractInput[portabilityContract]) (contractOutput[openObjectContract], error) {
			return contractOutput[openObjectContract]{map[string]any{}}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[contractInput[openObjectContract], contractOutput[openObjectContract]]{
		Name:        "untyped",
		Description: "Accepts any argument object",
		Handler: func(context.Context, contractInput[openObjectContract]) (contractOutput[openObjectContract], error) {
			return contractOutput[openObjectContract]{map[string]any{}}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 3 {
		t.Fatalf("tools = %d, want 3", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		findings := portableSchemaFindings(wireSchemaValue(t, tool.Name+" input", tool.InputSchema))
		if len(findings) != 0 {
			t.Errorf("%s input schema is not portable:\n%s", tool.Name, strings.Join(findings, "\n"))
		}
		if tool.OutputSchema != nil {
			t.Errorf("%s advertises output schema", tool.Name)
		}
	}

	if schema := listedTool(t, session, "untyped").OutputSchema; schema != nil {
		t.Fatalf("untyped tool output schema = %#v, want none", schema)
	}
}

// TestDerivedSchemasBecomePortableShapes proves the shape one derived schema
// takes: an unconstrained member explicitly permits every JSON type rather than
// using boolean `true`, every nullable member becomes an `anyOf` of single-type
// branches, and each branch keeps the constraints that apply to its type.
func TestDerivedSchemasBecomePortableShapes(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[portabilityDerivedInput, portabilityDerivedOutput]{
		Name:        "derived",
		Description: "Derived schemas",
		Handler: func(context.Context, portabilityDerivedInput) (portabilityDerivedOutput, error) {
			return portabilityDerivedOutput{}, nil
		},
	})
	session := connectSession(t, server.HTTPHandler())
	input := decodedObject(
		t, "input schema",
		wireSchemaValue(t, "derived input", listedTool(t, session, "derived").InputSchema),
	)
	properties := decodedObject(t, "input properties", input["properties"])

	free := decodedObject(t, "free schema", properties["free"])
	assertTypeUnion(t, "free", free, []string{
		"null", "boolean", "object", "array", "number", "string",
	})

	note := decodedObject(t, "note schema", properties["note"])
	assertTypeUnion(t, "note", note, []string{"null", "string"})

	tags := decodedObject(t, "tags schema", properties["tags"])
	assertTypeUnion(t, "tags", tags, []string{"null", "array"})
	if _, present := tags["items"]; !present {
		t.Fatalf("tags schema = %#v, want the items constraint at its original path", tags)
	}
	tagsNull := anyOfBranch(t, "tags", tags, "null")
	if len(tagsNull) != 1 {
		t.Fatalf("tags null branch = %#v, want only its type", tagsNull)
	}

	point := decodedObject(t, "point schema", properties["point"])
	assertTypeUnion(t, "point", point, []string{"null", "object"})
	if _, present := point["properties"]; !present {
		t.Fatalf("point schema = %#v, want properties at their original path", point)
	}
	if additional, present := point["additionalProperties"]; !present || additional == true {
		t.Fatalf("point additionalProperties = %#v, want the closed-object constraint", additional)
	}
}

// Success validation preserves unconstrained and nullable members,
// while modeled failures remain independent of the success contract.
func TestOutputRetainsNullableMembersAndFailures(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0", Logger: discardLogger()})
	mcpapi.Register(server, mcpapi.Tool[portabilityDerivedInput, portabilityDerivedOutput]{
		Name:        "derived",
		Description: "Derived schemas",
		Handler: func(_ context.Context, input portabilityDerivedInput) (portabilityDerivedOutput, error) {
			if input.Note != nil && *input.Note == "fail" {
				return portabilityDerivedOutput{}, &mcpapi.ToolError{
					Code:    "entity_disabled",
					Message: "entity is disabled",
					Details: map[string]any{"status": "entity_disabled"},
				}
			}
			return portabilityDerivedOutput{
				Points: []portabilityPoint{{Label: "first", Priority: new(int)}},
				Note:   map[string]any{"arbitrary": []any{1, "two"}},
				Target: &portabilityPoint{Label: "target"},
			}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	if listedTool(t, session, "derived").OutputSchema != nil {
		t.Fatal("output schema advertised")
	}

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "derived", Arguments: map[string]any{"free": map[string]any{"a": 1}},
		})
		if err != nil {
			t.Fatalf("call tool: %v", err)
		}
		if result.IsError {
			t.Fatalf("unexpected tool error: %s", firstText(t, result))
		}
		if got := firstText(
			t,
			result,
		); got != `{"points":[{"label":"first","priority":0}],"note":{"arbitrary":[1,"two"]},"target":{"label":"target"}}` {
			t.Fatalf("success = %s", got)
		}
	})

	t.Run("structured failure", func(t *testing.T) {
		t.Parallel()
		result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
			Name: "derived", Arguments: map[string]any{"note": "fail"},
		})
		if err != nil {
			t.Fatalf("call tool: %v", err)
		}
		if !result.IsError {
			t.Fatalf("result = %#v, want an isError result", result)
		}
		if got := firstText(
			t,
			result,
		); got != `{"failure_code":"entity_disabled","message":"entity is disabled","status":"entity_disabled"}` {
			t.Fatalf("failure = %s", got)
		}
	})
}

// TestPortableInputSchemaStillRejectsBeforeTheHandler proves the rewrite changes
// only the shape clients read: Hearth still validates arguments against the
// normalized derived schema, still accepts a null for a nullable member, and
// still never runs the handler for an invalid argument.
func TestPortableInputSchemaStillRejectsBeforeTheHandler(t *testing.T) {
	t.Parallel()
	t.Run("nullable members accept null", func(t *testing.T) {
		t.Parallel()
		session, handlerInputs := rejectionSession(t)
		result := callDerived(t, session, map[string]any{"note": nil, "tags": nil, "point": nil, "path": nil})
		if result.IsError {
			t.Fatalf("result = %#v, want a null argument to be accepted", result)
		}
		<-handlerInputs
	})

	t.Run("wrong type is rejected before the handler", func(t *testing.T) {
		t.Parallel()
		session, handlerInputs := rejectionSession(t)
		result := callDerived(t, session, map[string]any{"note": 42})
		if !result.IsError {
			t.Fatalf("result = %#v, want an isError tool result", result)
		}
		if text := firstText(t, result); !strings.Contains(text, `"failure_code":"invalid_arguments"`) {
			t.Fatalf("error text = %q, want structured input failure", text)
		}
		assertHandlerSkipped(t, handlerInputs)
	})

	t.Run("missing required member is rejected before the handler", func(t *testing.T) {
		t.Parallel()
		session, handlerInputs := rejectionSession(t)
		result := callDerived(t, session, map[string]any{"point": map[string]any{}})
		if !result.IsError {
			t.Fatalf("result = %#v, want an isError tool result", result)
		}
		assertHandlerSkipped(t, handlerInputs)
	})
}

// rejectionSession serves the derived-schema tool on its own server and reports
// the inputs the handler received.
func rejectionSession(t *testing.T) (*mcp.ClientSession, chan portabilityDerivedInput) {
	t.Helper()
	handlerInputs := make(chan portabilityDerivedInput, 1)
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	mcpapi.Register(server, mcpapi.Tool[portabilityDerivedInput, portabilityDerivedOutput]{
		Name:        "derived",
		Description: "Derived schemas",
		Handler: func(_ context.Context, input portabilityDerivedInput) (portabilityDerivedOutput, error) {
			handlerInputs <- input
			return portabilityDerivedOutput{}, nil
		},
	})
	return connectSession(t, server.HTTPHandler()), handlerInputs
}

func callDerived(t *testing.T, session *mcp.ClientSession, arguments map[string]any) *mcp.CallToolResult {
	t.Helper()
	result, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "derived", Arguments: arguments,
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	return result
}

func assertHandlerSkipped(t *testing.T, handlerInputs chan portabilityDerivedInput) {
	t.Helper()
	select {
	case input := <-handlerInputs:
		t.Fatalf("handler ran for invalid input: %#v", input)
	default:
	}
}

// TestPortableInputSchemaAppliesDefaults proves the rewrite keeps a member's
// default on the member rather than inside a union branch, so the SDK still
// fills a missing nullable argument with it.
func TestPortableInputSchemaAppliesDefaults(t *testing.T) {
	t.Parallel()
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	received := make(chan string, 1)
	mcpapi.Register(server, mcpapi.Tool[subjectDefaultInput, contractOutput[openObjectContract]]{
		Name:        "defaults",
		Description: "Applies a member default",
		Handler: func(_ context.Context, input subjectDefaultInput) (contractOutput[openObjectContract], error) {
			received <- *input.Subject
			return contractOutput[openObjectContract]{map[string]any{}}, nil
		},
	})
	session := connectSession(t, server.HTTPHandler())
	if _, err := session.CallTool(t.Context(), &mcp.CallToolParams{
		Name: "defaults", Arguments: map[string]any{},
	}); err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if got := <-received; got != "fallback" {
		t.Fatalf("subject = %q, want the declared default", got)
	}
}

func listedTool(t *testing.T, session *mcp.ClientSession, name string) *mcp.Tool {
	t.Helper()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	for _, tool := range listed.Tools {
		if tool.Name == name {
			return tool
		}
	}
	t.Fatalf("tool %q is not registered", name)
	return nil
}
