//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"reflect"
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

// TestPortableInputSchemaPreservesValidation proves the rewrite is
// semantics-preserving: for each schema shape it touches, the schema a client
// receives accepts exactly the argument documents the schema it replaced
// accepted.
//
// Every case declares its expected outcome, and the test checks the original as
// well as the rewritten document against it, so a case that lost its
// discriminating power fails instead of passing vacuously.
func TestPortableInputSchemaPreservesValidation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		schema    string
		instances []portabilityInstance
	}{
		{
			name:   "empty schemas expand without rewriting instance-valued objects",
			schema: `{"type":"object","properties":{"subject":{},"fixed":{"const":{}}},"required":["subject","fixed"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null,"fixed":{}}`, valid: true},
				{value: `{"subject":[1,"two"],"fixed":{}}`, valid: true},
				{value: `{"subject":{},"fixed":1}`, valid: false},
				{value: `{"subject":true,"fixed":{"extra":1}}`, valid: false},
			},
		},
		{
			name:   "unconstrained JSON value stays unconstrained",
			schema: `{"type":"object","properties":{"subject":true},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":false}`, valid: true},
				{value: `{"subject":7}`, valid: true},
				{value: `{"subject":"text"}`, valid: true},
				{value: `{"subject":[1,"two"]}`, valid: true},
				{value: `{"subject":{"nested":{}}}`, valid: true},
				{value: `{}`, valid: false},
				{value: `{"subject":1,"extra":1}`, valid: false},
			},
		},
		{
			name:   "nullable string keeps its constraints",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"minLength":3,"maxLength":5,"pattern":"^a"}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"abc"}`, valid: true},
				{value: `{"subject":"abcde"}`, valid: true},
				{value: `{"subject":"ab"}`, valid: false},
				{value: `{"subject":"abcdef"}`, valid: false},
				{value: `{"subject":"bcde"}`, valid: false},
				{value: `{"subject":42}`, valid: false},
			},
		},
		{
			name:   "nullable array keeps its items and count limits",
			schema: `{"type":"object","properties":{"subject":{"type":["null","array"],"items":{"type":"integer","minimum":0},"minItems":1,"maxItems":2}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":[0]}`, valid: true},
				{value: `{"subject":[0,1]}`, valid: true},
				{value: `{"subject":[]}`, valid: false},
				{value: `{"subject":[0,1,2]}`, valid: false},
				{value: `{"subject":[-1]}`, valid: false},
				{value: `{"subject":["zero"]}`, valid: false},
				{value: `{"subject":"zero"}`, valid: false},
			},
		},
		{
			name:   "nullable object keeps its closed shape",
			schema: `{"type":"object","properties":{"subject":{"type":["null","object"],"properties":{"name":{"type":"string"}},"required":["name"],"additionalProperties":false}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":{"name":"entry"}}`, valid: true},
				{value: `{"subject":{}}`, valid: false},
				{value: `{"subject":{"name":"entry","extra":1}}`, valid: false},
				{value: `{"subject":"entry"}`, valid: false},
			},
		},
		{
			name:   "always-false member stays unsatisfiable",
			schema: `{"type":"object","properties":{"subject":false},"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{}`, valid: true},
				{value: `{"subject":1}`, valid: false},
				{value: `{"subject":null}`, valid: false},
				{value: `{"other":1}`, valid: false},
			},
		},
		{
			name:   "multi-member union splits scoped constraints",
			schema: `{"type":"object","properties":{"subject":{"type":["string","integer","null"],"minLength":2,"minimum":10}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":10}`, valid: true},
				{value: `{"subject":10.0}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":9}`, valid: false},
				{value: `{"subject":10.5}`, valid: false},
				{value: `{"subject":true}`, valid: false},
			},
		},
		{
			name:   "existing anyOf constraint survives a type union rewrite",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"anyOf":[{"const":"ok"}]}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":"ok"}`, valid: true},
				{value: `{"subject":"other"}`, valid: false},
				{value: `{"subject":null}`, valid: false},
				{value: `{"subject":3}`, valid: false},
			},
		},
		{
			name:   "union nested behind a ref keeps resolving",
			schema: `{"type":"object","$defs":{"leaf":{"type":["null","string"],"minLength":2}},"properties":{"subject":{"$ref":"#/$defs/leaf"}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":5}`, valid: false},
			},
		},
		{
			name:   "union in an embedded-id document keeps its base",
			schema: `{"type":"object","properties":{"subject":{"type":["null","object"],"$id":"urn:hearth:portability:embedded","$defs":{"name":{"type":"string","minLength":2}},"properties":{"name":{"$ref":"#/$defs/name"}},"required":["name"],"additionalProperties":false}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":{"name":"ab"}}`, valid: true},
				{value: `{"subject":{"name":"a"}}`, valid: false},
				{value: `{"subject":{}}`, valid: false},
				{value: `{"subject":{"name":"ab","extra":1}}`, valid: false},
			},
		},
		{
			name:   "default on a nullable member still validates",
			schema: `{"type":"object","properties":{"subject":{"type":["null","string"],"default":"fallback","minLength":2}},"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{}`, valid: true},
				{value: `{"subject":null}`, valid: true},
				{value: `{"subject":"ab"}`, valid: true},
				{value: `{"subject":"a"}`, valid: false},
				{value: `{"subject":3}`, valid: false},
			},
		},
		{
			name:   "boolean under a boolean-valued keyword stays a keyword value",
			schema: `{"type":"object","properties":{"subject":{"type":"object","additionalProperties":true}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":{}}`, valid: true},
				{value: `{"subject":{"anything":1}}`, valid: true},
				{value: `{"subject":1}`, valid: false},
			},
		},
		{
			name:   "instance values inside enum survive the rewrite",
			schema: `{"type":"object","properties":{"subject":{"type":["string","boolean"],"enum":["auto",true]}},"required":["subject"],"additionalProperties":false}`,
			instances: []portabilityInstance{
				{value: `{"subject":"auto"}`, valid: true},
				{value: `{"subject":true}`, valid: true},
				{value: `{"subject":false}`, valid: false},
				{value: `{"subject":"manual"}`, valid: false},
				{value: `{"subject":1}`, valid: false},
			},
		},
	}

	// The matrix belongs to the pure rewrite owner; registered type hooks above
	// prove that the production registration path publishes that same rewrite.
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			advertised, normalizeErr := mcpapi.NormalizeSchemaForTest(json.RawMessage(test.schema))
			if normalizeErr != nil {
				t.Fatal(normalizeErr)
			}
			if findings := portableSchemaFindings(advertised); len(findings) != 0 {
				t.Fatalf("advertised schema is not portable:\n%s", strings.Join(findings, "\n"))
			}
			advertisedJSON, err := json.Marshal(advertised)
			if err != nil {
				t.Fatalf("marshal advertised schema: %v", err)
			}
			for _, instance := range test.instances {
				assertSchemaAcceptance(t, "original "+test.name, test.schema, instance)
				assertSchemaAcceptance(t, "portable "+test.name, string(advertisedJSON), instance)
			}
		})
	}
}

func assertSchemaAcceptance(t *testing.T, label, schema string, instance portabilityInstance) {
	t.Helper()
	if accepted := schemaAccepts(t, label, schema, instance.value); accepted != instance.valid {
		t.Fatalf("%s accepted %s = %t, want %t", label, instance.value, accepted, instance.valid)
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

// TestPortableInputSchemaIsIdempotent proves the rewrite is a fixed point: a
// schema that already passed through it is republished unchanged, so a tool
// registered from another tool's advertised schema cannot drift further.
func TestPortableInputSchemaIsIdempotent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		schema string
	}{
		{name: "override document", schema: portabilityOverrideSchema},
		{
			name: "union with scoped constraints",
			schema: `{
  "type": "object",
  "properties": {
    "subject": {"type": ["null", "array"], "items": {"type": "string"}, "minItems": 1}
  },
  "required": ["subject"],
  "additionalProperties": false
}`,
		},
	}
	for _, test := range tests {
		first, err := mcpapi.NormalizeSchemaForTest(json.RawMessage(test.schema))
		if err != nil {
			t.Fatal(err)
		}
		second, err := mcpapi.NormalizeSchemaForTest(first)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("%s is not a fixed point:\nfirst:  %#v\nsecond: %#v", test.name, first, second)
		}
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
