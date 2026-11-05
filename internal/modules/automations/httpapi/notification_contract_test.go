package httpapi_test

import (
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// Discovery is an independent consumer contract: it must not advertise invented
// provider evidence or allow terminal Step status with missing/null completion.
func TestNotificationHistoryDiscoveryLifecycleShapes(t *testing.T) {
	t.Parallel()
	session := newHTTPScenario(t, nil)
	router := session.router
	tools := delayOperationContracts(t, session)
	openapi := pageObject(t, restJSON(t, router, "/openapi.json"))
	schemas := []*jsonschema.Schema{
		compileDelayContractSchema(
			t,
			openapi,
			"#/components/schemas/AutomationRunBody/$defs/history/$defs/notificationStepExecution",
		),
		compileDelayContractSchema(
			t,
			tools["get-automation-history-entry"].ResponseSchema,
			"#/$defs/history/$defs/notificationStepExecution",
		),
	}
	for _, test := range []struct {
		name, fields string
		valid        bool
	}{
		{"running", `"phase":"running","evidence":{}`, true},
		{"accepted", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{"outcome":"accepted"}`, true},
		{"pre-attempt failure", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"reason_code":"notification_admission_closed"}`, true},
		{"interrupted", `"phase":"interrupted","completed_at":"2026-10-11T10:00:01Z","evidence":{"reason_code":"core_restarted"}`, true},
		{"running invents outcome", `"phase":"running","evidence":{"outcome":"accepted"}`, false},
		{"accepted without outcome", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{}`, false},
		{"accepted unknown", `"phase":"succeeded","completed_at":"2026-10-11T10:00:01Z","evidence":{"outcome":"unknown","reason_code":"outcome_timeout"}`, false},
		{"failed accepted", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"outcome":"accepted","reason_code":"provider_error"}`, false},
		{"terminal null completion", `"phase":"interrupted","completed_at":null,"evidence":{"reason_code":"core_restarted"}`, false},
		{"interrupted invents provider evidence", `"phase":"interrupted","completed_at":"2026-10-11T10:00:01Z","evidence":{"outcome":"unknown","reason_code":"core_restarted"}`, false},
		{"provider failure lacks outcome", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"reason_code":"provider_error"}`, false},
		{"unknown wrong reason", `"phase":"failed","completed_at":"2026-10-11T10:00:01Z","evidence":{"outcome":"unknown","reason_code":"destination_unavailable"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body := exactJSONObject(
				t,
				`{"step_id":"notify","sequence":0,"kind":"notification","started_at":"2026-10-11T10:00:00Z",`+test.fields+`}`,
			)
			body["evidence"].(map[string]any)["attempt_id"] = "ntf_01980000-0000-7000-8000-000000000001"
			for _, schema := range schemas {
				if err := schema.Validate(body); (err == nil) != test.valid {
					t.Fatalf("valid=%v, schema error=%v", test.valid, err)
				}
			}
		})
	}
}
