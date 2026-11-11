package httpapi_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"

	viewsv1 "github.com/mholtzscher/hearth/contracts/views/v1"
	"github.com/mholtzscher/hearth/internal/modules/dashboards/httpapi"
)

//nolint:gocognit // Check both complete published request contracts and their negative cases.
func TestDashboardWriteRequestSchemas(t *testing.T) {
	t.Parallel()
	api := humaecho.New(echo.New(), huma.DefaultConfig("test", "1"))
	httpapi.Register(huma.NewGroup(api, "/v1"), failedDashboards{}, nil)
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if decodeErr := json.Unmarshal(raw, &document); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	var expected map[string]any
	if decodeErr := json.Unmarshal(viewsv1.Schema, &expected); decodeErr != nil {
		t.Fatal(decodeErr)
	}
	if len(expected["$defs"].(map[string]any)) == 0 {
		t.Fatal("catalog has no shared definitions")
	}
	resourceID, _ := expected["$id"].(string)
	if resourceID == "" || strings.Count(string(raw), `"$id":"`+resourceID+`"`) != 1 {
		t.Fatal("OpenAPI must publish the canonical schema resource exactly once")
	}
	const uri = "https://hearth.local/dashboard-openapi"
	paths := document["paths"].(map[string]any)
	components := document["components"].(map[string]any)["schemas"].(map[string]any)
	definition := map[string]any{
		"schema_version":  float64(1),
		"catalog_version": "hearth.dashboard/v1",
		"title":           "View",
		"spec": map[string]any{
			"root":     "n",
			"elements": map[string]any{"n": map[string]any{"type": "Note", "props": map[string]any{"text": "Hello"}}},
		},
	}
	invalidSource := map[string]any{
		"schema_version": float64(1), "catalog_version": "hearth.dashboard/v1", "title": "Invalid source",
		"spec": map[string]any{
			"root": "metric", "elements": map[string]any{
				"metric": map[string]any{"type": "Metric", "props": map[string]any{
					"source": map[string]any{
						"kind": "entity-state", "entity_id": "not-an-entity-id", "entity_type": "hearth.temperature/v1",
					},
				}},
			},
		},
	}
	for _, tc := range []struct {
		path, method, pointer string
		revision              bool
	}{
		{"/v1/dashboards", "post", "~1v1~1dashboards", false},
		{"/v1/dashboards/{id}", "put", "~1v1~1dashboards~1{id}", true},
	} {
		t.Run(tc.method, func(t *testing.T) {
			t.Parallel()
			compiler := jsonschema.NewCompiler()
			if addErr := compiler.AddResource(uri, document); addErr != nil {
				t.Fatal(addErr)
			}
			op := paths[tc.path].(map[string]any)[tc.method].(map[string]any)
			body := op["requestBody"].(map[string]any)
			if body["required"] != true {
				t.Fatalf("request body not required: %+v", body)
			}
			schema := body["content"].(map[string]any)["application/json"].(map[string]any)["schema"].(map[string]any)
			wrapper := resolveSchema(schema, components)
			props := wrapper["properties"].(map[string]any)
			actual := resolveSchema(props["definition"].(map[string]any), components)
			if !reflect.DeepEqual(actual, expected) {
				t.Fatal("published definition differs from embedded catalog schema")
			}
			compiled, compileErr := compiler.Compile(
				uri + "#/paths/" + tc.pointer + "/" + tc.method + "/requestBody/content/application~1json/schema",
			)
			if compileErr != nil {
				t.Fatal(compileErr)
			}
			valid := map[string]any{"definition": definition}
			if tc.revision {
				valid["expected_revision"] = float64(1)
			}
			if validateErr := compiled.Validate(valid); validateErr != nil {
				t.Fatalf("valid request: %v", validateErr)
			}
			invalid := []any{
				map[string]any{},
				map[string]any{"definition": definition, "unknown": true},
				map[string]any{"definition": map[string]any{}},
			}
			badSource := map[string]any{"definition": invalidSource}
			if tc.revision {
				badSource["expected_revision"] = float64(1)
			}
			invalid = append(invalid, badSource)
			if tc.revision {
				invalid = append(
					invalid,
					map[string]any{"definition": definition},
					map[string]any{"definition": definition, "expected_revision": 0},
				)
			}
			for _, input := range invalid {
				if compiled.Validate(input) == nil {
					t.Fatalf("accepted invalid request: %#v", input)
				}
			}
		})
	}
}

func resolveSchema(schema, components map[string]any) map[string]any {
	if ref, ok := schema["$ref"].(string); ok {
		return components[strings.TrimPrefix(ref, "#/components/schemas/")].(map[string]any)
	}
	return schema
}
