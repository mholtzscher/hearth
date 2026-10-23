//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"testing"
)

func TestFunctionMCPExistingRunHasEmptyFunctionEvidence(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":1}`)
	created := exactToolBody(
		t,
		callAutomationTool(t, session, "create_automation", json.RawMessage(`{"definition":`+document+`}`)),
	)
	id := created["automation_id"].(string)
	started := exactToolBody(
		t,
		callAutomationTool(t, session, "start_automation_run", map[string]any{"automation_id": id}),
	)
	runID := started["run_id"].(string)
	waitForAPI(t, service, id, runID)
	run := exactToolBody(
		t,
		callAutomationTool(
			t,
			session,
			"get_automation_history_entry",
			map[string]any{"automation_id": id, "entry_id": runID},
		),
	)
	if run["status"] != "succeeded" || canonicalJSON(t, run["functions"]) != "[]" {
		t.Fatalf("existing non-Function Run lost empty evidence: %v", run)
	}
}

func TestFunctionMCPDefinitionDiscoveryIsStrict(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	tools := delayTools(t, session)
	for _, operation := range []string{"create_automation", "replace_automation"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			schema := compileDelayContractSchema(t, inputSchemaDefinition(t, operation, tools[operation]), "#")
			valid := exactJSONObject(
				t,
				delayDocument(
					`{"id":"inspect","kind":"function","adapter_id":"synthetic-functions","function_key":"inspect","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","arguments":{"sequence":9007199254740993}}`,
				),
			)
			if err := schema.Validate(valid); err != nil {
				t.Fatalf("published authoring schema rejects a Function leaf: %v", err)
			}
			leaf := valid["steps"].([]any)[0].(map[string]any)
			leaf["operation"] = "set"
			if schema.Validate(valid) == nil {
				t.Fatal("Function definition accepted Command fields")
			}
			delete(leaf, "operation")
			delete(leaf, "contract_fingerprint")
			if schema.Validate(valid) == nil {
				t.Fatal("Function definition accepted an unpinned contract")
			}
		})
	}
}
