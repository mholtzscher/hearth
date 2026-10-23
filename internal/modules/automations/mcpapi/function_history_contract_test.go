package mcpapi //nolint:testpackage // Verifies the actual private success contract, which MCP discovery deliberately omits.

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// A9: MCP's independent detail schema must retain the canonical Function evidence
// family, without claiming that a reserved identity proves a dispatched call.
func TestFunctionMCPHistorySuccessContractEvidenceShapes(t *testing.T) {
	t.Parallel()
	schema := compileResultContract(t, "historyDetail",
		"#/$defs/evidence/$defs/functionExecution")
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
		{"interruption invents revision", `"status":"interrupted","completed_at":"2026-10-23T10:00:01Z","reason_code":"core_stopping","catalog_revision":"afc_01980000-0000-7000-8000-000000000002"`, false},
		{"unknown without revision", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","invocation_outcome":"unknown","reason_code":"outcome_timeout"`, false},
		{"unknown without outcome", `"status":"failed","completed_at":"2026-10-23T10:00:01Z","reason_code":"outcome_timeout"`, false},
		{"terminal null completion", `"status":"interrupted","completed_at":null,"reason_code":"core_stopping"`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			body, err := jsonschema.UnmarshalJSON(
				bytes.NewBufferString(
					`{"step_id":"inspect","position":0,"invocation_id":"afi_01980000-0000-7000-8000-000000000001","started_at":"2026-10-23T10:00:00Z",` + test.fields + `}`,
				),
			)
			if err != nil {
				t.Fatal(err)
			}
			if validationErr := schema.Validate(body); (validationErr == nil) != test.valid {
				t.Fatalf("valid=%t: %v; %v", test.valid, validationErr, body)
			}
		})
	}
}
