package api_test

import (
	"encoding/json"
	"slices"
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

func openAPISchemas(t *testing.T, document map[string]any) map[string]any {
	t.Helper()
	components, ok := document["components"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI document has no components")
	}
	schemas, ok := components["schemas"].(map[string]any)
	if !ok {
		t.Fatal("OpenAPI components have no schemas")
	}
	return schemas
}

func openAPISchema(t *testing.T, document map[string]any, name string) map[string]any {
	t.Helper()
	schema, ok := openAPISchemas(t, document)[name].(map[string]any)
	if !ok {
		t.Fatalf("OpenAPI is missing component schema %q", name)
	}
	return schema
}

// openAPIProperties returns one schema's named property sub-schema.
func openAPIProperties(t *testing.T, schema map[string]any, name string) map[string]any {
	t.Helper()
	properties, ok := schema["properties"].(map[string]any)
	if !ok {
		t.Fatalf("schema %v has no properties", schema)
	}
	property, ok := properties[name].(map[string]any)
	if !ok {
		t.Fatalf("schema is missing property %q: %v", name, properties)
	}
	return property
}

// openAPIRef returns one nested schema reference.
func openAPIRef(t *testing.T, schema map[string]any) string {
	t.Helper()
	ref, _ := schema["$ref"].(string)
	if ref == "" {
		t.Fatalf("schema %v is not a reference", schema)
	}
	return ref
}

// assertOpenAPIEnum checks one schema property's closed value set.
func assertOpenAPIEnum(t *testing.T, schema map[string]any, name string, want ...string) {
	t.Helper()
	property := openAPIProperties(t, schema, name)
	values, ok := property["enum"].([]any)
	if !ok {
		t.Fatalf("property %q has no enum: %v", name, property)
	}
	got := make([]string, 0, len(values))
	for _, value := range values {
		text, isString := value.(string)
		if !isString {
			t.Fatalf("property %q enum value %v is not a string", name, value)
		}
		got = append(got, text)
	}
	if !slices.Equal(got, want) {
		t.Fatalf("property %q enum = %v, want %v", name, got, want)
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

// The published Condition, decision, evaluation, node, Skip, and summary
// schemas must carry the closed enums and the strict recursive family and
// decision-mode constraints the transport contract depends on, not a permissive
// DTO shape.
func TestAutomationConditionOpenAPIPublishesRecursiveClosedSchemas(t *testing.T) {
	t.Parallel()
	_, openapi, _ := newAutomationHTTP(t, newAPIDevices())
	document := runtimeOpenAPIMap(t, openapi)

	assertConditionFamilySchemas(t, document)
	assertConditionDecisionSchema(t, document)

	evaluation := openAPISchema(t, document, "AutomationConditionEvaluationBody")
	assertOpenAPIEnum(t, evaluation, "result", "true", "false", "unknown")

	node := openAPISchema(t, document, "AutomationConditionNodeResultBody")
	assertOpenAPIEnum(t, node, "result", "true", "false", "unknown")
	assertOpenAPIEnum(t, node, "unknown_reason",
		"entity_missing", "state_missing", "evidence_in_future",
		"evidence_expired", "pointer_missing", "type_mismatch",
	)

	skip := openAPISchema(t, document, "AutomationSkipBody")
	assertOpenAPIEnum(t, skip, "source", "device_fact", "manual")
	assertOpenAPIEnum(t, skip, "reason", "automation_busy", "stale_fact", "conditions_false", "conditions_unknown")
	if ref := openAPIRef(
		t,
		openAPIProperties(t, skip, "condition_decision"),
	); ref != "#/components/schemas/AutomationConditionDecisionBody" {
		t.Fatalf("skip condition_decision ref = %q", ref)
	}
	skipRequired := skip["required"].([]any)
	for _, member := range []string{"source", "condition_decision"} {
		if !containsOpenAPIValue(skipRequired, member) {
			t.Fatalf("AutomationSkipBody does not require %q: %v", member, skipRequired)
		}
	}
	if containsOpenAPIValue(skipRequired, "fact") {
		t.Fatalf("AutomationSkipBody requires fact: %v", skipRequired)
	}

	summary := openAPISchema(t, document, "AutomationHistorySummaryBody")
	assertOpenAPIEnum(t, summary, "source", "device_fact", "manual")
	assertOpenAPIEnum(t, summary, "condition_mode",
		"not_configured", "not_evaluated", "bypassed", "evaluated",
	)
	assertOpenAPIEnum(t, summary, "condition_result", "true", "false", "unknown")
	_ = openAPIProperties(t, summary, "bypass_requested")

	definition := openAPISchema(t, document, "AutomationDefinitionBody")
	if ref := openAPIRef(
		t,
		openAPIProperties(t, definition, "conditions"),
	); ref != "#/components/schemas/AutomationConditionBody" {
		t.Fatalf("definition conditions ref = %q", ref)
	}
	run := openAPISchema(t, document, "AutomationRunBody")
	if ref := openAPIRef(
		t,
		openAPIProperties(t, run, "condition_decision"),
	); ref != "#/components/schemas/AutomationConditionDecisionBody" {
		t.Fatalf("run condition_decision ref = %q", ref)
	}
}

// assertConditionFamilySchemas checks that the Condition component is one closed
// oneOf over the four flattened family components and that every family forbids
// family-inapplicable members.
func assertConditionFamilySchemas(t *testing.T, document map[string]any) {
	t.Helper()
	condition := openAPISchema(t, document, "AutomationConditionBody")
	wantFamilies := []string{
		"#/components/schemas/AutomationConditionEntityStateBody",
		"#/components/schemas/AutomationConditionAllBody",
		"#/components/schemas/AutomationConditionAnyBody",
		"#/components/schemas/AutomationConditionNotBody",
	}
	if refs := openAPIOneOfRefs(t, condition); !slices.Equal(refs, wantFamilies) {
		t.Fatalf("condition oneOf = %v, want %v", refs, wantFamilies)
	}

	entityState := openAPISchema(t, document, "AutomationConditionEntityStateBody")
	assertOpenAPIStrictFamily(t, entityState,
		[]string{"id", "kind", "entity_id", "pointer", "operator", "operand"})
	assertOpenAPIEnum(t, entityState, "operator", "eq", "ne", "lt", "lte", "gt", "gte")
	if kind := openAPIConst(t, entityState, "kind"); kind != "entity_state" {
		t.Fatalf("entity_state kind const = %v", kind)
	}
	if ref := openAPIRef(
		t,
		openAPIProperties(t, entityState, "id"),
	); ref != "#/components/schemas/AutomationConditionIDBody" {
		t.Fatalf("entity_state id ref = %q", ref)
	}
	if pointer := openAPIProperties(t, entityState, "pointer"); pointer["type"] != "string" ||
		pointer["$ref"] != nil {
		t.Fatalf("entity_state pointer must be an inline required string: %v", pointer)
	}
	age := openAPIProperties(t, entityState, "max_age_seconds")
	if age["minimum"] != float64(1) || age["maximum"] != float64(2592000) {
		t.Fatalf("max_age_seconds bounds = %v..%v, want 1..2592000", age["minimum"], age["maximum"])
	}

	for _, name := range []string{"AutomationConditionAllBody", "AutomationConditionAnyBody"} {
		group := openAPISchema(t, document, name)
		assertOpenAPIStrictFamily(t, group, []string{"id", "kind", "children"})
		if ref := openAPIRef(
			t,
			openAPIProperties(t, group, "children"),
		); ref != "#/components/schemas/AutomationConditionChildrenBody" {
			t.Fatalf("%s children ref = %q", name, ref)
		}
	}
	children := openAPISchema(t, document, "AutomationConditionChildrenBody")
	if children["minItems"] != float64(1) {
		t.Fatalf("children minItems = %v, want 1", children["minItems"])
	}
	if ref := openAPIRef(t, children["items"].(map[string]any)); ref != "#/components/schemas/AutomationConditionBody" {
		t.Fatalf("children items ref = %q, want the recursive condition component", ref)
	}

	not := openAPISchema(t, document, "AutomationConditionNotBody")
	assertOpenAPIStrictFamily(t, not, []string{"id", "kind", "child"})
	if ref := openAPIRef(
		t,
		openAPIProperties(t, not, "child"),
	); ref != "#/components/schemas/AutomationConditionBody" {
		t.Fatalf("not child ref = %q, want the recursive condition component", ref)
	}
}

// assertConditionDecisionSchema checks the closed mode enum, the exact snapshot
// and evaluation members each mode requires, and the bypass rule.
func assertConditionDecisionSchema(t *testing.T, document map[string]any) {
	t.Helper()
	decision := openAPISchema(t, document, "AutomationConditionDecisionBody")
	assertOpenAPIEnum(t, decision, "mode", "not_configured", "not_evaluated", "bypassed", "evaluated")
	if decision["additionalProperties"] != false {
		t.Fatalf("decision additionalProperties = %v, want false", decision["additionalProperties"])
	}
	if required := openAPIRequiredStrings(t, decision); !slices.Equal(
		required, []string{"mode", "bypass_requested"},
	) {
		t.Fatalf("decision required = %v, want mode and bypass_requested", required)
	}
	if ref := openAPIRef(
		t,
		openAPIProperties(t, decision, "snapshot"),
	); ref != "#/components/schemas/AutomationConditionBody" {
		t.Fatalf("decision snapshot ref = %q", ref)
	}
	if ref := openAPIRef(
		t,
		openAPIProperties(t, decision, "evaluation"),
	); ref != "#/components/schemas/AutomationConditionEvaluationBody" {
		t.Fatalf("decision evaluation ref = %q", ref)
	}
	assertDecisionModeRules(t, decision)
}

// openAPIOneOfRefs returns the referenced components of one oneOf schema.
func openAPIOneOfRefs(t *testing.T, schema map[string]any) []string {
	t.Helper()
	branches, ok := schema["oneOf"].([]any)
	if !ok {
		t.Fatalf("schema has no oneOf: %v", schema)
	}
	refs := make([]string, 0, len(branches))
	for _, raw := range branches {
		branch, isObject := raw.(map[string]any)
		if !isObject {
			t.Fatalf("oneOf branch is not an object: %v", raw)
		}
		refs = append(refs, openAPIRef(t, branch))
	}
	return refs
}

// assertOpenAPIStrictFamily checks one closed Condition family: an object with
// exactly the required members and no additional properties.
func assertOpenAPIStrictFamily(t *testing.T, schema map[string]any, wantRequired []string) {
	t.Helper()
	if schema["type"] != "object" {
		t.Fatalf("family type = %v, want object", schema["type"])
	}
	if schema["additionalProperties"] != false {
		t.Fatalf("family additionalProperties = %v, want false", schema["additionalProperties"])
	}
	if required := openAPIRequiredStrings(t, schema); !slices.Equal(required, wantRequired) {
		t.Fatalf("family required = %v, want %v", required, wantRequired)
	}
}

// openAPIRequiredStrings returns one schema's required member list in order.
func openAPIRequiredStrings(t *testing.T, schema map[string]any) []string {
	t.Helper()
	raw, ok := schema["required"].([]any)
	if !ok {
		t.Fatalf("schema has no required list: %v", schema)
	}
	required := make([]string, 0, len(raw))
	for _, value := range raw {
		text, isString := value.(string)
		if !isString {
			t.Fatalf("required member %v is not a string", value)
		}
		required = append(required, text)
	}
	return required
}

// openAPIConst returns one property's JSON Schema const value.
func openAPIConst(t *testing.T, schema map[string]any, name string) any {
	t.Helper()
	property := openAPIProperties(t, schema, name)
	value, present := property["const"]
	if !present {
		t.Fatalf("property %q has no const: %v", name, property)
	}
	return value
}

// assertDecisionModeRules checks the exact snapshot/evaluation presence and the
// bypass value of every decision mode, including that not_configured forbids both
// snapshot and evaluation while leaving a manual Run's bypass request free.
func assertDecisionModeRules(t *testing.T, decision map[string]any) {
	t.Helper()
	notConfigured := openAPIDecisionBranch(t, decision, "not_configured")
	for _, forbidden := range []string{"snapshot", "evaluation"} {
		if !openAPINotRequires(t, notConfigured, forbidden) {
			t.Fatalf("not_configured does not forbid %q: %v", forbidden, notConfigured)
		}
	}
	if _, present := notConfigured["required"]; present {
		t.Fatalf("not_configured must not require snapshot or evaluation: %v", notConfigured)
	}
	if properties, ok := notConfigured["properties"].(map[string]any); ok {
		if _, present := properties["bypass_requested"]; present {
			t.Fatalf("not_configured must not constrain bypass_requested: %v", properties)
		}
	}

	notEvaluated := openAPIDecisionBranch(t, decision, "not_evaluated")
	assertDecisionBranch(t, notEvaluated, "not_evaluated", []string{"snapshot"}, false, true)
	bypassed := openAPIDecisionBranch(t, decision, "bypassed")
	assertDecisionBranch(t, bypassed, "bypassed", []string{"snapshot"}, true, true)
	evaluated := openAPIDecisionBranch(t, decision, "evaluated")
	assertDecisionBranch(t, evaluated, "evaluated", []string{"snapshot", "evaluation"}, false, false)
}

// assertDecisionBranch checks one configured decision mode's required members,
// bypass const, and whether it forbids an evaluation.
func assertDecisionBranch(
	t *testing.T,
	branch map[string]any,
	mode string,
	wantRequired []string,
	wantBypass bool,
	forbidEvaluation bool,
) {
	t.Helper()
	if required := openAPIRequiredStrings(t, branch); !slices.Equal(required, wantRequired) {
		t.Fatalf("%s required = %v, want %v", mode, required, wantRequired)
	}
	if bypass := openAPIConst(t, branch, "bypass_requested"); bypass != wantBypass {
		t.Fatalf("%s bypass_requested const = %v, want %v", mode, bypass, wantBypass)
	}
	if forbidEvaluation && !openAPINotRequires(t, branch, "evaluation") {
		t.Fatalf("%s does not forbid evaluation: %v", mode, branch)
	}
}

// openAPIDecisionBranch returns the oneOf decision branch whose mode const
// matches the requested mode.
func openAPIDecisionBranch(t *testing.T, decision map[string]any, mode string) map[string]any {
	t.Helper()
	branches, ok := decision["oneOf"].([]any)
	if !ok {
		t.Fatalf("decision has no oneOf: %v", decision)
	}
	for _, raw := range branches {
		branch, isObject := raw.(map[string]any)
		if !isObject {
			t.Fatalf("decision oneOf branch is not an object: %v", raw)
		}
		if openAPIConst(t, branch, "mode") == mode {
			return branch
		}
	}
	t.Fatalf("decision has no %q branch: %v", mode, decision)
	return nil
}

// openAPINotRequires reports whether one branch forbids a member via
// not/required or not/anyOf/required.
func openAPINotRequires(t *testing.T, branch map[string]any, member string) bool {
	t.Helper()
	negated, isNegation := branch["not"].(map[string]any)
	if !isNegation {
		return false
	}
	if required, isList := negated["required"].([]any); isList {
		return containsOpenAPIValue(required, member)
	}
	branches, ok := negated["anyOf"].([]any)
	if !ok {
		return false
	}
	for _, raw := range branches {
		alternative, isObject := raw.(map[string]any)
		if !isObject {
			continue
		}
		if required, isList := alternative["required"].([]any); isList && containsOpenAPIValue(required, member) {
			return true
		}
	}
	return false
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
