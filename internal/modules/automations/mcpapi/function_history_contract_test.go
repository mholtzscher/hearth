package mcpapi //nolint:testpackage // Verifies the actual private success contract, which MCP discovery deliberately omits.

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestFunctionMCPHistoryExecutionSchema(t *testing.T) {
	t.Parallel()
	schema := compileResultContract(t, "historyDetail", "#/$defs/evidence/$defs/functionStepExecution")
	const common = `"step_id":"inspect","sequence":0,"kind":"function","started_at":"2026-10-23T10:00:00Z"`
	for _, test := range []struct {
		name, fields string
		valid        bool
	}{
		{"running reservation", `"phase":"running","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001"}`, true},
		{"succeeded", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded"}`, true},
		{"failed invocation", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"failed","reason_code":"provider_error"}`, true},
		{"not executed", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"not_executed","reason_code":"adapter_busy"}`, true},
		{"unknown", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"unknown","reason_code":"outcome_timeout"}`, true},
		{"pre-admission failure", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"function_contract_changed"}`, true},
		{"interrupted", `"phase":"interrupted","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"core_restarted"}`, true},
		{"running invents result", `"phase":"running","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","invocation_outcome":"succeeded"}`, false},
		{"success lacks revision", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","invocation_outcome":"succeeded"}`, false},
		{"success lacks outcome", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002"}`, false},
		{"failed success", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","reason_code":"provider_error"}`, false},
		{"unknown without revision", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","invocation_outcome":"unknown","reason_code":"outcome_timeout"}`, false},
		{"unknown without outcome", `"phase":"failed","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"outcome_timeout"}`, false},
		{"terminal null completion", `"phase":"interrupted","completed_at":null,"evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"core_stopping"}`, false},
		{"success copies result", `"phase":"succeeded","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","catalog_revision":"afc_01980000-0000-7000-8000-000000000002","invocation_outcome":"succeeded","result":{}}`, false},
		{"interruption invents revision", `"phase":"interrupted","completed_at":"2026-10-23T10:00:01Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"core_stopping","catalog_revision":"afc_01980000-0000-7000-8000-000000000002"}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString("{" + common + "," + test.fields + "}"))
			if err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(value)
			if (err == nil) != test.valid {
				t.Fatalf("valid=%t: %v; %v", test.valid, err, value)
			}
		})
	}
}
