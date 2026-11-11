//go:build integration

package mcpapi_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/mcpapi"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestPresentViewCatalogAndCall(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	mcpapi.Register(server, dashboards.NewService(nil, nil, nil))
	httpServer := httptest.NewServer(server.HTTPHandler())
	defer httpServer.Close()
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(t.Context(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = session.Close() }()
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || len(listed.Tools) != 1 {
		t.Fatalf("list: %v %+v", err, listed)
	}
	raw, err := json.Marshal(listed.Tools[0].InputSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if decodeErr := json.Unmarshal(raw, &schema); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	props := schema["properties"].(map[string]any)
	actual := props["definition"].(map[string]any)
	var expected map[string]any
	if decodeErr := json.Unmarshal(viewsv1.Schema, &expected); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	expectedRaw, _ := json.Marshal(portable(expected))
	actualRaw, _ := json.Marshal(actual)
	if string(actualRaw) != string(expectedRaw) {
		t.Fatalf("schema differs: %s", actualRaw)
	}
	if len(actual["$defs"].(map[string]any)) == 0 {
		t.Fatal("catalog has no shared definitions")
	}
	assertPublishedSourceValidation(t, schema)
	definition := map[string]any{
		"schema_version":  1,
		"catalog_version": "hearth.dashboard/v1",
		"title":           "View",
		"spec": map[string]any{
			"root":     "a",
			"elements": map[string]any{"a": map[string]any{"type": "Note", "props": map[string]any{"text": "Hello"}}},
		},
	}
	result, err := session.CallTool(
		t.Context(),
		&mcp.CallToolParams{Name: "present_view", Arguments: map[string]any{"definition": definition}},
	)
	if err != nil || result.IsError {
		t.Fatalf("call: %+v %v", result, err)
	}
}

// Compile the advertised contract independently of the domain decoder: a
// successful call alone cannot prove that its shared reference targets validate.
func assertPublishedSourceValidation(t *testing.T, document map[string]any) {
	t.Helper()
	const uri = "https://hearth.invalid/present-view-test"
	compiler := jsonschema.NewCompiler()
	if err := compiler.AddResource(uri, document); err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.Compile(uri)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		entityID string
		valid    bool
	}{
		{"ent_01980000-0000-7000-8000-000000000001", true},
		{"not-an-entity-id", false},
	} {
		input := `{"definition":{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"Metric","spec":{"root":"metric","elements":{"metric":{"type":"Metric","props":{"source":{"kind":"entity-state","entity_id":"ENTITY_ID","entity_type":"hearth.temperature/v1"}}}}}}}`
		instance, decodeErr := jsonschema.UnmarshalJSON(
			strings.NewReader(strings.ReplaceAll(input, "ENTITY_ID", test.entityID)),
		)
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if validateErr := compiled.Validate(instance); (validateErr == nil) != test.valid {
			t.Fatalf("source %q: validation error = %v, want valid = %v", test.entityID, validateErr, test.valid)
		}
	}
}

// A model can repair a rejected view only from the rule the result names; the
// result must still never quote submitted values.
func TestPresentViewRejectionNamesTheViolatedRule(t *testing.T) {
	t.Parallel()
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	mcpapi.Register(server, dashboards.NewService(nil, nil, nil))
	httpServer := httptest.NewServer(server.HTTPHandler())
	t.Cleanup(httpServer.Close)
	// Parallel subtests outlive this function, so the session ends at cleanup.
	session, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "1"}, nil).
		Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: httpServer.URL}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	listed, err := session.ListTools(t.Context(), nil)
	if err != nil || !strings.Contains(listed.Tools[0].Description, "at most 16 distinct Entities and Devices") ||
		!strings.Contains(listed.Tools[0].Description, "one entity_type and field over one window") {
		t.Fatalf("description does not state view limits: %+v %v", listed, err)
	}
	const sentinel = "sk-submitted-value-must-not-echo"
	for _, test := range []struct {
		name, definition, want string
	}{
		{
			"binding limit",
			sharedExample(t, "too-many-entity-references"),
			"invalid view definition: a view references at most 16 distinct Entities and Devices",
		},
		{
			"schema violation",
			`{"schema_version":1,"catalog_version":"hearth.dashboard/v1","title":"View","spec":{"root":"a","elements":{"a":{"type":"Note","props":{"text":"Hi","extra":"` + sentinel + `"}}}}}`,
			"invalid view definition: does not match the view schema",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, callErr := session.CallTool(t.Context(), &mcp.CallToolParams{
				Name:      "present_view",
				Arguments: map[string]any{"definition": json.RawMessage(test.definition)},
			})
			if callErr != nil || !result.IsError || len(result.Content) != 1 {
				t.Fatalf("call: %+v %v", result, callErr)
			}
			text := result.Content[0].(*mcp.TextContent).Text
			want := `{"failure_code":"invalid_definition","message":"` + test.want + `"}`
			if text != want || strings.Contains(text, sentinel) {
				t.Fatalf("result = %s, want %s", text, want)
			}
		})
	}
}

func sharedExample(t *testing.T, name string) string {
	t.Helper()
	var examples []struct {
		Name       string          `json:"name"`
		Definition json.RawMessage `json:"definition"`
	}
	if err := json.Unmarshal(viewsv1.Examples, &examples); err != nil {
		t.Fatal(err)
	}
	for _, example := range examples {
		if example.Name == name {
			return string(example.Definition)
		}
	}
	t.Fatalf("missing shared example %s", name)
	return ""
}

// MCP's portable schema representation rewrites boolean false to an equivalent
// impossible schema; all other catalog constraints must remain intact.
func portable(value any) any {
	switch v := value.(type) {
	case bool:
		if !v {
			return map[string]any{"type": "null", "not": map[string]any{"type": "null"}}
		}
		return v
	case map[string]any:
		for key, child := range v {
			v[key] = portable(child)
		}
		return v
	case []any:
		for i, child := range v {
			v[i] = portable(child)
		}
		return v
	default:
		return value
	}
}
