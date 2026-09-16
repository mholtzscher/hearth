package api_test

import (
	"encoding/json"
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

// The manual Run operation must publish an optional, non-nullable, closed body
// schema with one boolean bypass member while Huma keeps validating it.
func TestManualRunOpenAPIPublishesOptionalClosedBypassBody(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)

	runOperation := document["paths"].(map[string]any)["/v1/automations/{automation_id}/runs"].(map[string]any)
	operation := runOperation["post"].(map[string]any)
	requestBody, ok := operation["requestBody"].(map[string]any)
	if !ok {
		t.Fatal("manual run operation publishes no request body schema")
	}
	if required, present := requestBody["required"]; present && required != false {
		t.Fatalf("manual run request body required = %v, want optional", required)
	}
	content := requestBody["content"].(map[string]any)
	bodySchema := content["application/json"].(map[string]any)["schema"].(map[string]any)
	if bodySchema["type"] != "object" {
		t.Fatalf("manual run body type = %v, want object", bodySchema["type"])
	}
	if additional, present := bodySchema["additionalProperties"]; !present || additional != false {
		t.Fatalf("manual run body additionalProperties = %v, want false", additional)
	}
	properties := bodySchema["properties"].(map[string]any)
	if len(properties) != 1 {
		t.Fatalf("manual run body properties = %v, want only bypass_conditions", properties)
	}
	bypass := properties["bypass_conditions"].(map[string]any)
	if bypass["type"] != "boolean" {
		t.Fatalf("bypass_conditions type = %v, want boolean", bypass["type"])
	}
	// A non-nullable object schema must not accept the JSON literal null.
	if nullable, present := bodySchema["nullable"]; present && nullable == true {
		t.Fatalf("manual run body is nullable: %v", bodySchema)
	}
}

// Huma must publish the recursive Condition DTO and the decision mode enum from
// the struct tags alone, with no hand-built component override.
func TestOpenAPIPublishesGeneratedConditionDTOs(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)

	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	condition, ok := schemas["AutomationConditionBody"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI is missing AutomationConditionBody")
	}
	items := condition["properties"].(map[string]any)["children"].(map[string]any)["items"].(map[string]any)
	if ref, _ := items["$ref"].(string); ref != "#/components/schemas/AutomationConditionBody" {
		t.Fatalf("AutomationConditionBody children items $ref = %q", ref)
	}

	decision, ok := schemas["AutomationConditionDecisionBody"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI is missing AutomationConditionDecisionBody")
	}
	if _, overridden := decision["oneOf"]; overridden {
		t.Fatalf("AutomationConditionDecisionBody is not the generated schema: %v", decision)
	}
	mode := decision["properties"].(map[string]any)["mode"].(map[string]any)
	enum := mode["enum"].([]any)
	for _, want := range []string{"not_configured", "not_evaluated", "bypassed", "evaluated"} {
		if !containsOpenAPIValue(enum, want) {
			t.Fatalf("AutomationConditionDecisionBody mode enum is missing %q: %v", want, enum)
		}
	}
}

// The documented 409 problem must carry the optional history reference so a
// blocked manual admission is discoverable from the schema alone.
func TestManualRunOpenAPIDocumentsHistoryReferenceOnConflict(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)

	operation := document["paths"].(map[string]any)["/v1/automations/{automation_id}/runs"].(map[string]any)["post"].(map[string]any)
	conflict := operation["responses"].(map[string]any)["409"].(map[string]any)
	content := conflict["content"].(map[string]any)
	problem := content["application/problem+json"].(map[string]any)["schema"].(map[string]any)

	properties := problem["properties"].(map[string]any)
	for _, member := range []string{"history_id", "history_url"} {
		if _, present := properties[member]; !present {
			t.Fatalf("409 problem schema is missing %q: %v", member, properties)
		}
	}
	if required, present := problem["required"].([]any); present && containsOpenAPIValue(required, "history_id") {
		t.Fatalf("409 problem requires history_id: %v", required)
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
