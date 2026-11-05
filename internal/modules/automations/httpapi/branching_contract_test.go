package httpapi_test

import (
	"slices"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Compile the actual advertised schema locations, so dangling recursive refs fail.
func TestBranchingPublishedSchemasResolveAndValidateNestedDefinitions(t *testing.T) {
	t.Parallel()
	entity, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	session := newHTTPScenario(t, nil)
	router := session.router
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	runSchema := compilePublishedSchema(
		t,
		openapi,
		"https://hearth.invalid/openapi#/components/schemas/AutomationRunBody",
	)
	if runSchema.Ref == nil || !slices.Contains(runSchema.Ref.Required, "executions") {
		t.Fatal("Run schema does not require executions")
	}
	assertPublishedBranchSchema(
		t,
		openapi,
		"https://hearth.test/openapi.json",
		"#/components/schemas/AutomationBody/$defs/definition",
		nestedBranchDocument(t, entity),
	)
	tools, err := session.contracts()
	if err != nil {
		t.Fatal(err)
	}
	for _, tool := range tools.Operations {
		if tool.Name != "create-automation" && tool.Name != "replace-automation" {
			continue
		}
		definition := inputSchemaDefinition(t, tool.Name, tool)
		assertPublishedBranchSchema(
			t,
			definition,
			"https://hearth.test/"+tool.Name,
			"",
			nestedBranchDocument(t, entity),
		)
	}
}
