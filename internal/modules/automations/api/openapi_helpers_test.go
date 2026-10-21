package api_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// runtimeOpenAPIMap marshals the generated document so schema assertions observe
// exactly what callers receive.
func runtimeOpenAPIMap(t *testing.T, openapi huma.API) map[string]any {
	t.Helper()
	raw, err := json.Marshal(openapi.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err = json.Unmarshal(raw, &document); err != nil {
		t.Fatal(err)
	}
	return document
}

func assertCronSchema(t *testing.T, name string, branch map[string]any) {
	t.Helper()
	if !schemaRejectsEveryValue(branch["additionalProperties"]) {
		t.Fatalf("%s cron permits unknown fields: %v", name, branch)
	}
	properties := schemaObject(t, name+" cron properties", branch["properties"])
	required := schemaStringSet(t, name+" cron required", branch["required"])
	if len(properties) != 3 || len(required) != 3 || !required["id"] || !required["kind"] || !required["expression"] {
		t.Fatalf("%s cron fields = %v, required = %v", name, properties, required)
	}
	if schemaObject(t, name+" cron kind", properties["kind"])["const"] != "cron" {
		t.Fatalf("%s cron discriminator = %v", name, properties["kind"])
	}
	expression := schemaObject(t, name+" expression", properties["expression"])
	if expression["type"] != "string" || expression["minLength"] != float64(1) ||
		expression["maxLength"] != float64(512) {
		t.Fatalf("%s expression constraints = %v", name, expression)
	}
	description, _ := expression["description"].(string)
	for _, restriction := range []string{"five-field", "literal *", "512 UTF-8 bytes", "seconds", "descriptors", "timezone prefixes", "extensions"} {
		if !strings.Contains(description, restriction) {
			t.Errorf("%s expression description lacks %q: %s", name, restriction, description)
		}
	}
	if len(schemaArray(t, name+" expression examples", expression["examples"])) == 0 {
		t.Errorf("%s expression has no examples", name)
	}
}

func containsOpenAPIValue(values []any, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
