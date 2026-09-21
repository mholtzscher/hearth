package mcpapi_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/google/jsonschema-go/jsonschema"
	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/mholtzscher/hearth/internal/mcpapi"
)

// portabilityDerivedInput exercises every schema shape the portability rewrite
// touches in a schema the SDK derives from Go types: an unconstrained JSON value,
// a nullable scalar, a nullable array, a nullable object, and a list of nullable
// objects.
type portabilityDerivedInput struct {
	Free  any                `json:"free,omitempty"`
	Note  *string            `json:"note,omitempty"`
	Tags  []string           `json:"tags,omitempty"`
	Point *portabilityPoint  `json:"point,omitempty"`
	Path  []portabilityPoint `json:"path,omitempty"`
}

type portabilityPoint struct {
	Label    string `json:"label"`
	Priority *int   `json:"priority,omitempty"`
}

// portabilityDerivedOutput mirrors the input shapes on the result side, so the
// advertised success schema is validated against a body carrying an
// unconstrained member and nullable members.
type portabilityDerivedOutput struct {
	Points []portabilityPoint `json:"points"`
	Note   any                `json:"note"`
	Label  *string            `json:"label,omitempty"`
	Target *portabilityPoint  `json:"target,omitempty"`
}

// portabilityOverrideSchema is a caller-supplied argument schema carrying every
// shape the rewrite replaces: a bare `true` and a bare `false`, array-valued
// `type` unions inside a `$id`-scoped document with `$defs`, and a `default` on a
// nullable member.
const portabilityOverrideSchema = `{
  "type": "object",
  "properties": {
    "definition": {
      "type": "object",
      "$id": "urn:hearth:portability:override",
      "title": "Portability override",
      "$defs": {
        "leaf": {
          "type": ["null", "object"],
          "properties": {"operand": true},
          "required": ["operand"],
          "additionalProperties": false
        }
      },
      "properties": {
        "operand": true,
        "note": {"type": ["null", "string"], "default": "none"},
        "condition": {"$ref": "#/$defs/leaf"},
        "variants": {
          "type": ["null", "array"],
          "minItems": 1,
          "items": {"oneOf": [{"type": "string"}, false]}
        }
      },
      "required": ["operand"],
      "additionalProperties": false
    }
  },
  "required": ["definition"],
  "additionalProperties": false
}`

// TestRegisteredToolSchemasArePortable proves every schema the wrapper registers
// reaches a client free of the two shapes strict MCP clients reject: a boolean
// subschema node and an array-valued `type`.
//
// It inspects the schemas tools/list actually publishes and registers one tool
// per path the wrapper can emit a schema from: a derived schema, an explicit
// override, the request escape hatch, and an untyped argument.
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
	mcpapi.Register(server, mcpapi.Tool[map[string]any, any]{
		Name:        "override",
		Description: "Explicit argument schema",
		InputSchema: json.RawMessage(portabilityOverrideSchema),
		Handler: func(context.Context, map[string]any) (any, error) {
			return map[string]any{}, nil
		},
	})
	mcpapi.RegisterWithRequest(server, mcpapi.ToolWithRequest[portabilityDerivedInput, portabilityDerivedOutput]{
		Name:        "request",
		Description: "Derived schemas through the request escape hatch",
		Handler: func(
			_ context.Context,
			_ *mcp.CallToolRequest,
			_ portabilityDerivedInput,
		) (portabilityDerivedOutput, error) {
			return portabilityDerivedOutput{}, nil
		},
	})
	mcpapi.Register(server, mcpapi.Tool[any, any]{
		Name:        "untyped",
		Description: "Accepts any argument object",
		Handler: func(context.Context, any) (any, error) {
			return map[string]any{}, nil
		},
	})

	session := connectSession(t, server.HTTPHandler())
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 4 {
		t.Fatalf("tools = %d, want 4", len(listed.Tools))
	}
	for _, tool := range listed.Tools {
		findings := portableSchemaFindings(wireSchemaValue(t, tool.Name+" input", tool.InputSchema))
		if len(findings) != 0 {
			t.Errorf("%s input schema is not portable:\n%s", tool.Name, strings.Join(findings, "\n"))
		}
		if tool.OutputSchema == nil {
			continue
		}
		findings = portableSchemaFindings(wireSchemaValue(t, tool.Name+" output", tool.OutputSchema))
		if len(findings) != 0 {
			t.Errorf("%s output schema is not portable:\n%s", tool.Name, strings.Join(findings, "\n"))
		}
	}

	if schema := listedTool(t, session, "untyped").OutputSchema; schema != nil {
		t.Fatalf("untyped tool output schema = %#v, want none", schema)
	}
}

// TestPortableSchemaFindingsDetectNonPortableSchemas is the negative control for
// the checker the portability tests rely on: it must report the shapes the
// rewrite removes and must not report a boolean that is a keyword value or an
// instance rather than a schema.
func TestPortableSchemaFindingsDetectNonPortableSchemas(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		schema    string
		wantFound bool
	}{
		{
			name:      "bare true in a property position",
			schema:    `{"type":"object","properties":{"value":true}}`,
			wantFound: true,
		},
		{
			name:      "bare false in a property position",
			schema:    `{"type":"object","properties":{"value":false}}`,
			wantFound: true,
		},
		{
			name:      "array-valued type",
			schema:    `{"type":"object","properties":{"value":{"type":["null","string"]}}}`,
			wantFound: true,
		},
		{
			name:      "boolean under a boolean-valued keyword",
			schema:    `{"type":"object","properties":{"value":{"type":"object","readOnly":true,"properties":{"tags":{"type":"array","items":{"type":"string"},"uniqueItems":true}}}}}`,
			wantFound: false,
		},
		{
			name:      "boolean inside an instance-valued keyword",
			schema:    `{"type":"object","properties":{"value":{"enum":["auto",true,1]}}}`,
			wantFound: false,
		},
		{
			name:      "already portable union",
			schema:    `{"type":"object","properties":{"value":{"anyOf":[{"type":"null"},{"type":"string"}]}}}`,
			wantFound: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			findings := portableSchemaFindings(decodedJSONValue(t, test.schema))
			if (len(findings) != 0) != test.wantFound {
				t.Fatalf("findings = %v, wantFound = %t", findings, test.wantFound)
			}
		})
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

	// A tool name has to be an identifier, so each case registers under its
	// index while its subtest keeps the readable name.
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	for index, test := range tests {
		mcpapi.Register(server, mcpapi.Tool[map[string]any, any]{
			Name:        fmt.Sprintf("case_%d", index),
			Description: "Validation equivalence case",
			InputSchema: json.RawMessage(test.schema),
			Handler: func(context.Context, map[string]any) (any, error) {
				return map[string]any{}, nil
			},
		})
	}
	session := connectSession(t, server.HTTPHandler())

	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			advertised := wireSchemaValue(
				t, test.name+" advertised schema",
				listedTool(t, session, fmt.Sprintf("case_%d", index)).InputSchema,
			)
			if findings := portableSchemaFindings(advertised); len(findings) != 0 {
				t.Fatalf("advertised schema is not portable:\n%s", strings.Join(findings, "\n"))
			}
			advertisedJSON, err := json.Marshal(advertised)
			if err != nil {
				t.Fatalf("marshal advertised schema: %v", err)
			}
			for _, instance := range test.instances {
				original := schemaAccepts(t, "original "+test.name, test.schema, instance.value)
				if original != instance.valid {
					t.Fatalf(
						"original schema accepted %s = %t, want %t",
						instance.value, original, instance.valid,
					)
				}
				normalized := schemaAccepts(
					t, "portable "+test.name, string(advertisedJSON), instance.value,
				)
				if normalized != instance.valid {
					t.Fatalf(
						"portable schema accepted %s = %t, want %t",
						instance.value, normalized, instance.valid,
					)
				}
			}
		})
	}
}

// TestPortableOutputSchemaValidatesSuccessAndFailure proves the normalized
// output union still describes every result a typed handler produces: a success
// body carrying an unconstrained member and nullable members, and a structured
// [mcpapi.ToolError].
func TestPortableOutputSchemaValidatesSuccessAndFailure(t *testing.T) {
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
	schema := advertisedOutputSchema(t, session, "derived")

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
		if validationErr := validateStructured(t, schema, result); validationErr != nil {
			t.Fatalf("success structured content does not match: %v", validationErr)
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
		if validationErr := validateStructured(t, schema, result); validationErr != nil {
			t.Fatalf("failure structured content does not match: %v", validationErr)
		}
	})
}

// TestPortableInputSchemaStillRejectsBeforeTheHandler proves the rewrite changes
// only the shape clients read: the SDK still validates arguments against the
// normalized derived schema, still accepts a null for a nullable member, and
// still never runs the handler for an invalid argument.
//
// the SDK enforces with, not just the one clients read.
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
		if text := firstText(t, result); !strings.Contains(text, "validating") {
			t.Fatalf("error text = %q, want the SDK's validation message", text)
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
	mcpapi.Register(server, mcpapi.Tool[map[string]any, any]{
		Name:        "defaults",
		Description: "Applies a member default",
		InputSchema: json.RawMessage(`{
  "type": "object",
  "properties": {"subject": {"type": ["null", "string"], "default": "fallback"}},
  "additionalProperties": false
}`),
		Handler: func(_ context.Context, input map[string]any) (any, error) {
			note, _ := input["subject"].(string)
			received <- note
			return map[string]any{}, nil
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
	server := mcpapi.New(mcpapi.Config{Name: "hearth", Version: "1.0.0"})
	for index, test := range tests {
		mcpapi.Register(server, mcpapi.Tool[map[string]any, any]{
			Name:        fmt.Sprintf("first_%d", index),
			Description: "First pass",
			InputSchema: json.RawMessage(test.schema),
			Handler: func(context.Context, map[string]any) (any, error) {
				return map[string]any{}, nil
			},
		})
	}
	session := connectSession(t, server.HTTPHandler())

	// Each second pass is registered from the first pass's advertised schema,
	// read back through the wire just as a client would.
	for index, test := range tests {
		advertised, err := json.Marshal(listedTool(t, session, fmt.Sprintf("first_%d", index)).InputSchema)
		if err != nil {
			t.Fatalf("%s: marshal advertised schema: %v", test.name, err)
		}
		mcpapi.Register(server, mcpapi.Tool[map[string]any, any]{
			Name:        fmt.Sprintf("second_%d", index),
			Description: "Second pass",
			InputSchema: json.RawMessage(advertised),
			Handler: func(context.Context, map[string]any) (any, error) {
				return map[string]any{}, nil
			},
		})
	}

	for index, test := range tests {
		first := wireSchemaValue(
			t, test.name+" first", listedTool(t, session, fmt.Sprintf("first_%d", index)).InputSchema,
		)
		second := wireSchemaValue(
			t, test.name+" second", listedTool(t, session, fmt.Sprintf("second_%d", index)).InputSchema,
		)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("%s is not a fixed point:\nfirst:  %#v\nsecond: %#v", test.name, first, second)
		}
	}
}

type portabilityInstance struct {
	value string
	valid bool
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

func wireSchemaValue(t *testing.T, label string, schema any) any {
	t.Helper()
	raw, err := json.Marshal(schema)
	if err != nil {
		t.Fatalf("%s: marshal schema: %v", label, err)
	}
	var value any
	if decodeErr := json.Unmarshal(raw, &value); decodeErr != nil {
		t.Fatalf("%s: decode schema %s: %v", label, raw, decodeErr)
	}
	return value
}

func decodedJSONValue(t *testing.T, document string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(document), &value); err != nil {
		t.Fatalf("decode %s: %v", document, err)
	}
	return value
}

// schemaAccepts reports whether one argument document validates against one
// argument schema under the validator the SDK itself uses, so an equivalence
// check compares enforcement rather than the wrapper's intent.
func schemaAccepts(t *testing.T, label, schema, instance string) bool {
	t.Helper()
	var document jsonschema.Schema
	if err := json.Unmarshal([]byte(schema), &document); err != nil {
		t.Fatalf("%s: decode schema %s: %v", label, schema, err)
	}
	resolved, err := document.Resolve(&jsonschema.ResolveOptions{ValidateDefaults: true})
	if err != nil {
		t.Fatalf("%s: resolve schema %s: %v", label, schema, err)
	}
	value := decodedJSONValue(t, instance)
	return resolved.Validate(value) == nil
}

func decodedObject(t *testing.T, label string, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON object", label, value)
	}
	return object
}

// assertTypeUnion requires node to be an `anyOf` of single-type branches naming
// exactly memberTypes, in order, and no array-valued `type` of its own.
func assertTypeUnion(t *testing.T, label string, node map[string]any, memberTypes []string) {
	t.Helper()
	if _, present := node["type"]; present {
		t.Fatalf("%s keeps an array-valued type: %#v", label, node)
	}
	branches := decodedArray(t, label+" anyOf", node["anyOf"])
	if len(branches) != len(memberTypes) {
		t.Fatalf("%s anyOf has %d branches, want %d", label, len(branches), len(memberTypes))
	}
	for index, branch := range branches {
		object := decodedObject(t, fmt.Sprintf("%s branch %d", label, index), branch)
		if object["type"] != memberTypes[index] {
			t.Fatalf("%s branch %d type = %#v, want %q", label, index, object["type"], memberTypes[index])
		}
	}
}

func anyOfBranch(t *testing.T, label string, node map[string]any, memberType string) map[string]any {
	t.Helper()
	for _, branch := range decodedArray(t, label+" anyOf", node["anyOf"]) {
		object := decodedObject(t, label+" branch", branch)
		if object["type"] == memberType {
			return object
		}
	}
	t.Fatalf("%s has no %q branch: %#v", label, memberType, node)
	return nil
}

func decodedArray(t *testing.T, label string, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON array", label, value)
	}
	return array
}

// portableSchemaFindings reports every node of one decoded JSON Schema a strict
// MCP client cannot read: a boolean subschema node, or an array-valued `type`.
//
// It mirrors the Inspector's lint, so it does not report a boolean that is a
// keyword value (`uniqueItems`, `readOnly`) and it does not descend into keyword
// values that hold instances rather than schemas (`enum`, `const`, `default`,
// `examples`).
func portableSchemaFindings(schema any) []string {
	// A boolean under one of these keywords is a keyword value or an instance,
	// not a schema: "uniqueItems" and "readOnly" take a boolean, and "enum",
	// "const", "default", and "examples" hold instances.
	booleanValueKeywords := map[string]bool{
		"uniqueItems": true,
		"readOnly":    true,
		"writeOnly":   true,
		"deprecated":  true,
	}
	instanceValueKeywords := map[string]bool{
		"enum":        true,
		"const":       true,
		"default":     true,
		"examples":    true,
		"$vocabulary": true,
	}
	var findings []string
	var walk func(node any, path, keyword string)
	walk = func(node any, path, keyword string) {
		switch value := node.(type) {
		case bool:
			if booleanValueKeywords[keyword] {
				return
			}
			findings = append(findings, fmt.Sprintf("%s is the boolean schema %v", path, value))
		case map[string]any:
			if members, isArray := value["type"].([]any); isArray {
				findings = append(findings, fmt.Sprintf("%s.type is the array %v", path, members))
			}
			for key, child := range value {
				if instanceValueKeywords[key] || booleanValueKeywords[key] {
					continue
				}
				walk(child, path+"."+key, key)
			}
		case []any:
			for index, element := range value {
				walk(element, fmt.Sprintf("%s[%d]", path, index), keyword)
			}
		}
	}
	walk(schema, "", "")
	return findings
}
