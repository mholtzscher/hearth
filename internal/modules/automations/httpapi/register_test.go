package httpapi_test

import (
	"slices"
	"testing"

	"github.com/danielgtaylor/huma/v2"
)

// OpenAPI must document PUT's strict {expected_revision, definition} envelope
// while create continues to accept a bare definition.
func TestAutomationAPIReplacementRequestEnvelopeDocument(t *testing.T) {
	t.Parallel()
	openapi := newHTTPScenario(t, nil).api
	document := openAutomationAPIDocument(t, openapi)

	replaceRef := requestBodyRef(t, document, "/v1/automations/{automation_id}", "put")
	if replaceRef != "#/components/schemas/AutomationReplacement" {
		t.Fatalf("replacement request body schema = %q, want the replacement envelope", replaceRef)
	}
	createRef := requestBodyRef(t, document, "/v1/automations", "post")
	if createRef != "#/components/schemas/AutomationDefinition" {
		t.Fatalf("create request body schema = %q, want AutomationDefinition", createRef)
	}
	assertReplacementEnvelopeSchema(t, document.Components.Schemas["AutomationReplacement"])
}

// OpenAPI must preserve operation IDs and tags without exposing reserved Command identities.
func TestAutomationAPIOperationIDsAndTags(t *testing.T) {
	t.Parallel()
	openapi := newHTTPScenario(t, nil).api
	operations := map[string]*huma.Operation{}
	for _, item := range openapi.OpenAPI().Paths {
		for _, operation := range []*huma.Operation{
			item.Get, item.Put, item.Post, item.Delete, item.Patch, item.Head, item.Options, item.Trace,
		} {
			if operation != nil {
				operations[operation.OperationID] = operation
			}
		}
	}
	want := []string{
		"create-automation", "list-automations", "get-automation", "replace-automation",
		"delete-automation", "start-automation-run", "list-automation-history", "get-automation-history-entry",
	}
	for _, id := range want {
		operation, found := operations[id]
		if !found {
			t.Fatalf("operation %q is not registered", id)
		}
		if len(operation.Tags) != 1 || operation.Tags[0] != "Automations" {
			t.Fatalf("operation %q tags = %#v", id, operation.Tags)
		}
	}
	definition, found := openapi.OpenAPI().Components.Schemas.Map()["AutomationDefinition"]
	if !found {
		t.Fatal("AutomationDefinition component is not published")
	}
	if definition.Extensions["additionalProperties"] != false {
		t.Fatalf("definition component is not strict: %#v", definition.Extensions["additionalProperties"])
	}
	runSchema := compilePublishedSchema(
		t,
		runtimeOpenAPIMap(t, openapi),
		"https://hearth.invalid/openapi#/components/schemas/AutomationRunBody",
	)
	runProperties := publishedSchemaProperties(runSchema)
	if _, hasExecutions := runProperties["executions"]; !hasExecutions {
		t.Fatal("Run schema is missing unified executions")
	}
	if _, hasLegacySteps := runProperties["steps"]; hasLegacySteps {
		t.Fatal("Run schema still exposes legacy steps")
	}
	if !slices.Contains(runSchema.Ref.Required, "executions") {
		t.Fatal("Run schema does not require executions")
	}
}
