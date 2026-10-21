package httpapi_test

import (
	"bytes"
	"encoding/json"
	"maps"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func schemaRejectsEveryValue(value any) bool {
	if value == false {
		return true
	}
	node, ok := value.(map[string]any)
	if !ok || node["type"] != "null" {
		return false
	}
	not, ok := node["not"].(map[string]any)
	return ok && not["type"] == "null"
}
func compilePublishedSchema(t *testing.T, document any, location string) *jsonschema.Schema {
	t.Helper()
	document, location = publishedSchemaResource(document, location)
	raw, err := json.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	uri, _, _ := strings.Cut(location, "#")
	if err = compiler.AddResource(uri, decoded); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(location)
	if err != nil {
		t.Fatal(err)
	}
	return schema
}

// OpenAPI's components keyword is not a JSON Schema applicator. Enter the
// selected component's resource base as an OpenAPI-aware resolver would.
func publishedSchemaResource(document any, location string) (any, string) {
	_, fragment, _ := strings.Cut(location, "#")
	target, component := strings.CutPrefix(fragment, "/components/schemas/")
	if !component {
		return document, location
	}
	name, pointer, nested := strings.Cut(target, "/")
	schemas := document.(map[string]any)["components"].(map[string]any)["schemas"].(map[string]any)
	resource := schemas[name].(map[string]any)
	id, hasID := resource["$id"].(string)
	if !hasID {
		return document, location
	}
	if nested {
		id += "#/" + pointer
	}
	return resource, id
}

func publishedSchemaProperties(schema *jsonschema.Schema) map[string]*jsonschema.Schema {
	properties := make(map[string]*jsonschema.Schema)
	visited := make(map[*jsonschema.Schema]bool)
	var collect func(*jsonschema.Schema)
	collect = func(node *jsonschema.Schema) {
		if node == nil || visited[node] {
			return
		}
		visited[node] = true
		maps.Copy(properties, node.Properties)
		collect(node.Ref)
		for _, branches := range [][]*jsonschema.Schema{node.AnyOf, node.OneOf, node.AllOf} {
			for _, branch := range branches {
				collect(branch)
			}
		}
	}
	collect(schema)
	return properties
}

func inputSchemaDefinition(t *testing.T, name string, contract *httpOperationContract) map[string]any {
	t.Helper()
	return schemaObject(t, name+" definition schema", contract.DefinitionSchema)
}

func schemaObject(t *testing.T, label string, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON object schema", label, value)
	}
	return object
}

func schemaArray(t *testing.T, label string, value any) []any {
	t.Helper()
	array, ok := value.([]any)
	if !ok {
		t.Fatalf("%s = %#v, want a JSON array", label, value)
	}
	return array
}

func schemaStringSet(t *testing.T, label string, value any) map[string]bool {
	t.Helper()
	set := make(map[string]bool)
	for _, member := range schemaArray(t, label, value) {
		name, ok := member.(string)
		if !ok {
			t.Fatalf("%s member = %#v, want a string", label, member)
		}
		set[name] = true
	}
	return set
}
func definitionArguments(t *testing.T, document string) map[string]any {
	t.Helper()
	var definition map[string]any
	if err := json.Unmarshal([]byte(document), &definition); err != nil {
		t.Fatalf("decode definition document: %v", err)
	}
	return definition
}

// assertTriggerInputConstraints checks the closed Trigger branches and their family fields.
func assertTriggerInputConstraints(t *testing.T, name string, triggers map[string]any) {
	t.Helper()
	items := schemaObject(t, name+" triggers items", triggers["items"])
	branches := schemaArray(t, name+" triggers oneOf", items["oneOf"])
	observation, entityEvent, heldState, cron := false, false, false, false
	for _, branch := range branches {
		object := schemaObject(t, name+" trigger branch", branch)
		branchProperties := schemaObject(
			t, name+" trigger branch properties",
			schemaObject(t, name+" trigger branch", branch)["properties"],
		)
		required := schemaStringSet(t, name+" trigger required", object["required"])
		if _, present := branchProperties["expression"]; present {
			cron = true
			assertCronSchema(t, name, object)
		} else if !required["entity_id"] {
			t.Fatalf("%s device Trigger does not require entity_id: %#v", name, object)
		}
		if _, present := branchProperties["dispositions"]; present {
			observation = true
			dispositions := schemaObject(t, name+" dispositions", branchProperties["dispositions"])
			enum := schemaStringSet(
				t, name+" dispositions enum",
				schemaObject(t, name+" dispositions items", dispositions["items"])["enum"],
			)
			if !enum["applied"] || !enum["unchanged"] {
				t.Fatalf("%s disposition enum = %#v, want applied and unchanged", name, enum)
			}
		}
		if _, present := branchProperties["event_name"]; present {
			entityEvent = true
		}
		if _, present := branchProperties["for_seconds"]; present {
			heldState = true
			assertHeldStateInputConstraints(t, name, branchProperties)
		}
	}
	if !observation || !entityEvent || !heldState || !cron || len(branches) != 4 {
		t.Fatalf(
			"%s triggers schema branches = %#v, want Observation, Entity Event, held state, and cron",
			name,
			branches,
		)
	}
}

func assertHeldStateInputConstraints(t *testing.T, name string, properties map[string]any) {
	t.Helper()
	forSeconds := schemaObject(t, name+" for_seconds", properties["for_seconds"])
	if forSeconds["minimum"] != float64(1) || forSeconds["maximum"] != float64(2592000) {
		t.Fatalf("%s held duration schema = %#v", name, forSeconds)
	}
	kind := schemaObject(t, name+" held kind", properties["kind"])
	if kind["const"] != "held_state" {
		t.Fatalf("%s held kind schema = %#v", name, kind)
	}
}
