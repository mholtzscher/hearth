package api_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// Huma also serves standalone component documents. Compiling those actual
// responses catches pointer rewrites that work only inside /openapi.json.
func TestAutomationStandaloneSchemasResolve(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	for _, name := range []string{
		"AutomationDefinition", "AutomationBody", "AutomationCollectionBody", "AutomationRunBody",
		"AutomationSkipBody", "AutomationHistoryEntryBody", "AutomationHistorySummaryBody", "AutomationHistoryCollectionBody",
	} {
		path := "/schemas/" + name + ".json"
		response := performJSON(router, http.MethodGet, path, "")
		if response.Code != http.StatusOK {
			t.Fatalf("schema %s: %d %s", name, response.Code, response.Body.String())
		}
		schema := compilePublishedSchema(t, exactJSONObject(t, response.Body.String()), "https://hearth.test"+path)
		if err := schema.Validate(map[string]any{}); err == nil {
			t.Fatalf("schema %s accepts missing required fields", name)
		}
	}
}

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

// The runtime document is the consumer contract, not the embedded schema file.
func TestOpenAPIPublishesStrictCronDefinitionAndScheduleSources(t *testing.T) {
	t.Parallel()
	router, _, _ := newAutomationHTTP(t, newAPIDevices())
	document := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	definition := schemas["AutomationDefinition"].(map[string]any)
	triggers := definition["properties"].(map[string]any)["triggers"].(map[string]any)
	assertTriggerInputConstraints(t, "OpenAPI", triggers)
	for _, name := range []string{"run", "skip", "historySummary"} {
		properties := publishedSchemaProperties(
			compilePublishedSchema(
				t,
				document,
				"https://hearth.invalid/openapi#/components/schemas/AutomationHistoryEntryBody/$defs/history/$defs/"+name,
			),
		)
		if _, old := properties["source"]; old {
			t.Fatalf("%s still publishes source", name)
		}
		cause := properties["cause"]
		for _, raw := range []string{
			`{"kind":"manual"}`, `{"kind":"schedule"}`,
			`{"kind":"held_state","evidence":{"trigger_id":"held","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-04T10:01:00Z"}}`,
			`{"kind":"device_fact","fact":{"family":"entity_event","fact_id":"fact","entity_id":"entity","emitted_at":"2026-10-04T10:00:00Z","event_id":"event","name":"pressed"}}`,
		} {
			if err := cause.Validate(exactJSONObject(t, raw)); err != nil {
				t.Errorf("%s rejects Cause %s: %v", name, raw, err)
			}
		}
		if err := cause.Validate(map[string]any{"kind": "manual", "fact": nil}); err == nil {
			t.Errorf("%s accepts contradictory Cause", name)
		}
	}
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

// Published components must resolve recursive variants and enforce the same
// family-specific shape as the canonical codecs.
func TestOpenAPIPublishesStrictConditionVariants(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)

	condition := compilePublishedSchema(
		t,
		document,
		"https://hearth.invalid/openapi#/components/schemas/AutomationRunBody/$defs/definition/$defs/condition",
	)
	leaf := `{"id":"state","kind":"entity_state","entity_id":"entity","value_pointer":"","operator":"eq","operand":null}`
	tree := exactJSONObject(t, `{"id":"root","kind":"all","children":[{"id":"negated","kind":"not","child":`+leaf+`}]}`)
	if err := condition.Validate(tree); err != nil {
		t.Fatal(err)
	}
	if err := condition.Validate(
		map[string]any{"id": "trigger", "kind": "trigger", "trigger_ids": []any{"button"}},
	); err == nil {
		t.Fatal("admission Condition schema accepts branch-only Trigger leaf")
	}
	run := compilePublishedSchema(t, document, "https://hearth.invalid/openapi#/components/schemas/AutomationRunBody")
	decision := publishedSchemaProperties(run)["condition_decision"]
	for _, raw := range []string{
		`{"mode":"not_configured","bypass_requested":false}`,
		`{"mode":"not_evaluated","bypass_requested":false,"snapshot":` + leaf + `}`,
		`{"mode":"bypassed","bypass_requested":true,"snapshot":` + leaf + `}`,
		`{"mode":"evaluated","bypass_requested":false,"snapshot":` + leaf + `,"evaluation":{"evaluated_at":"2026-10-04T10:00:00Z","result":"unknown","nodes":[{"id":"state","kind":"entity_state","result":"unknown","unknown_reason":"entity_missing"}]}}`,
	} {
		if err := decision.Validate(exactJSONObject(t, raw)); err != nil {
			t.Fatalf("decision %s: %v", raw, err)
		}
	}
	if err := decision.Validate(map[string]any{"mode": "not_configured", "bypass_requested": true}); err == nil {
		t.Fatal("decision schema accepts contradictory bypass")
	}
}

func TestOpenAPIAdvertisesIndependentObservationComparisonLimits(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)
	trigger := compilePublishedSchema(
		t,
		document,
		"https://hearth.invalid/openapi#/components/schemas/AutomationBody/$defs/definition/properties/triggers/items",
	)
	properties := publishedSchemaProperties(trigger)
	for _, property := range []string{"previous_comparisons", "comparisons"} {
		array := properties[property]
		if array == nil || array.MaxItems == nil || *array.MaxItems != 8 {
			t.Errorf("%s does not publish maxItems 8", property)
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
