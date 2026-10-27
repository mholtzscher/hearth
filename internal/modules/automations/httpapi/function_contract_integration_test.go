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
		{"OpenAPI", "#/components/schemas/AutomationRunBody/$defs/history/$defs/functionStepExecution", openapi},
		{"operation", "#/$defs/history/$defs/functionStepExecution", tools["get-automation-history-entry"].ResponseSchema},
	} {
		t.Run(contract.name, func(t *testing.T) {
			t.Parallel()
			schema := compileDelayContractSchema(t, contract.document, contract.pointer)
			for _, test := range []struct {
				name, fields string
				valid        bool
			}{
				{"running reservation", `"phase":"running","evidence":{}`, true},
				{"succeeded", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded"}`, true},
				{"failed invocation", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"failed","reason_code":"provider_error"}`, true},
				{"not executed", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"not_executed","reason_code":"adapter_busy"}`, true},
				{"unknown", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"unknown","reason_code":"outcome_timeout"}`, true},
				{"pre-admission failure", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"reason_code":"function_contract_changed"}`, true},
				{"interrupted", `"phase":"interrupted","completed_at":"2026-10-23T10:00:01Z","evidence":{"reason_code":"core_restarted"}`, true},
				{"running invents result", `"phase":"running","evidence":{"invocation_outcome":"succeeded"}`, false},
				{"success lacks revision", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_outcome":"succeeded"}`, false},
				{"success lacks outcome", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002"}`, false},
				{"success copies result", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","result":{}}`, false},
				{"failed success", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","reason_code":"provider_error"}`, false},
				{"interruption invents outcome", `"phase":"interrupted","completed_at":"2026-10-23T10:00:01Z","evidence":{"reason_code":"core_stopping","invocation_outcome":"unknown"}`, false},
				{"unknown without revision", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_outcome":"unknown","reason_code":"outcome_timeout"}`, false},
				{"unknown without outcome", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"reason_code":"outcome_timeout"}`, false},
				{"terminal null completion", `"phase":"interrupted","completed_at":null,"evidence":{"reason_code":"core_stopping"}`, false},
			} {
				t.Run(test.name, func(t *testing.T) {
					t.Parallel()
					body := exactJSONObject(
						t,
						`{"step_id":"inspect","sequence":0,"kind":"function","started_at":"2026-10-23T10:00:00Z",`+test.fields+`}`,
					)
					body["evidence"].(map[string]any)["invocation_id"] = "afi_01980000-0000-7000-8000-000000000001"
					if err := schema.Validate(body); (err == nil) != test.valid {
						t.Fatalf("valid=%t: %v; %v", test.valid, err, body)
					}
				})
			}
		})
	}
}

func TestFunctionHTTPDelayRunHasOnlyDelayExecution(t *testing.T) {
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
	if run["status"] != "succeeded" || len(run["executions"].([]any)) != 1 ||
		run["executions"].([]any)[0].(map[string]any)["kind"] != "delay" {
		t.Fatalf("Delay Run lacks its native execution: %v", run)
	}
}
