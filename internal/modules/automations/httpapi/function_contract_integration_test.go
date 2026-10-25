//go:build integration

package httpapi_test

import (
	"testing"
)

// A9: published HTTP contracts distinguish a reserved link from authoritative
// invocation evidence. This uses the registered router/OpenAPI, not the private
// canonical schema alone, so missing relocation or registration is observable.
func TestFunctionHTTPHistoryDiscoveryEvidenceShapes(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	tools := delayOperationContracts(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	for _, contract := range []struct {
		name, pointer string
		document      any
	}{
		{"OpenAPI", "#/components/schemas/AutomationRunBody/$defs/history/$defs/functionExecution", openapi},
		{"operation", "#/$defs/history/$defs/functionExecution", tools["get-automation-history-entry"].ResponseSchema},
	} {
		t.Run(contract.name, func(t *testing.T) {
			t.Parallel()
			schema := compileDelayContractSchema(t, contract.document, contract.pointer)
			for _, test := range []struct {
				name, fields string
				valid        bool
			}{
				{"running reservation", `"status":"running"`, true},
				{"succeeded", `"status":"succeeded","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded"`, true},
				{"failed invocation", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"failed","reason_code":"provider_error"`, true},
				{"not executed", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"not_executed","reason_code":"adapter_busy"`, true},
				{"unknown", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"unknown","reason_code":"outcome_timeout"`, true},
				{"pre-admission failure", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","reason_code":"function_contract_changed"`, true},
				{"interrupted", `"status":"interrupted","completed_at":"2026-10-23T10:00:01Z","reason_code":"core_restarted"`, true},
				{"running invents result", `"status":"running","invocation_outcome":"succeeded"`, false},
				{"success lacks revision", `"status":"succeeded","completed_at":"2026-10-23T10:00:01Z","invocation_outcome":"succeeded"`, false},
				{"success lacks outcome", `"status":"succeeded","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002"`, false},
				{"success copies result", `"status":"succeeded","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","result":{}`, false},
				{"failed success", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","reason_code":"provider_error"`, false},
				{"interruption invents outcome", `"status":"interrupted","completed_at":"2026-10-23T10:00:01Z","reason_code":"core_stopping","invocation_outcome":"unknown"`, false},
				{"unknown without revision", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","invocation_outcome":"unknown","reason_code":"outcome_timeout"`, false},
				{"unknown without outcome", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","reason_code":"outcome_timeout"`, false},
				{"terminal null completion", `"status":"interrupted","completed_at":null,"reason_code":"core_stopping"`, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					body := exactJSONObject(
						t,
						`{"step_id":"inspect","position":0,"invocation_id":"afi_01980000-0000-7000-8000-000000000001","started_at":"2026-10-23T10:00:00Z",`+test.fields+`}`,
					)
					if err := schema.Validate(body); (err == nil) != test.valid {
						t.Fatalf("valid=%t: %v; %v", test.valid, err, body)
					}
				})
			}
		})
	}
}

func TestFunctionHTTPExistingRunHasEmptyFunctionEvidence(t *testing.T) {
	t.Parallel()
	router, _, service := newAutomationHTTP(t, newAPIDevices())
	session := newHTTPScenario(t, service)
	body := writeDelayDefinition(
		t,
		router,
		session,
		"HTTP",
		"",
		delayDocument(`{"id":"wait","kind":"delay","duration_ms":{"literal":1}}`),
	)
	id := body["id"].(string)
	started := startDelayRun(t, router, session, "HTTP", id)
	runID := started["id"].(string)
	waitForAPI(t, service, id, runID)
	entry := pageObject(t, restJSON(t, router, "/v1/automations/"+id+"/history/"+runID))
	run := entry["run"].(map[string]any)
	if run["status"] != "succeeded" || canonicalJSON(t, run["functions"]) != "[]" {
		t.Fatalf("existing non-Function Run lost empty evidence: %v", run)
	}
}
