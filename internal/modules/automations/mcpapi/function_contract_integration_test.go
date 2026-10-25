//go:build integration

package mcpapi_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestFunctionMCPExistingRunHasEmptyFunctionEvidence(t *testing.T) {
	t.Parallel()
	service := newAutomationService(t, newAPIDevices())
	session := connectAutomationMCP(t, service)
	document := delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`)
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

func TestFunctionMCPAuthoringErrorsPreserveStepAndReason(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"create_automation", "replace_automation"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			stub := newRecordingAutomations()
			stub.saveErr = fmt.Errorf("private diagnostic: %w", &automations.FunctionReferenceError{
				StepID: "nested-function", ReasonCode: "function_contract_changed",
			})
			session := connectAutomationMCP(t, stub)
			document := delayDocument(
				`{"id":"nested-function","kind":"function","adapter_id":"synthetic-functions","function_key":"inspect","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","arguments":{"literal":{}}}`,
			)
			arguments := map[string]any{"definition": json.RawMessage(document)}
			if operation == "replace_automation" {
				arguments["automation_id"] = createdIDForInput(t)
				arguments["expected_revision"] = 1
			}
			result := callAutomationTool(t, session, operation, arguments)
			text := toolErrorText(t, result)
			failure := decodeStructuredInto[map[string]any](t, result)
			if failure["failure_code"] != "invalid_automation" || failure["step_id"] != "nested-function" ||
				failure["reason_code"] != "function_contract_changed" || strings.Contains(text, "private diagnostic") ||
				stub.callCount != 1 {
				t.Fatalf("authoring error lost safe Function details: %s calls=%d", text, stub.callCount)
			}
		})
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
					`{"id":"inspect","kind":"function","adapter_id":"synthetic-functions","function_key":"inspect","contract_fingerprint":"sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","arguments":{"literal":{"sequence":9007199254740993}}}`,
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
