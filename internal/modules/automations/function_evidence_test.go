package automations_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

//nolint:gocognit // The projection matrix checks concrete fields and their canonical schema together.
func TestFunctionEvidenceEncodingPreservesOutcomeDistinctions(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 10, 23, 0, 0, 0, 0, time.UTC)
	const revision = adapters.CatalogID("afc_01980000-0000-7000-8000-000000000001")
	schema, err := historySchemaCompiler(
		t,
	).Compile("urn:hearth:schema:automation-history:v3#/$defs/functionStepExecution")
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name    string
		state   automations.FunctionExecutionState
		status  string
		outcome string
		reason  string
	}{
		{"running", automations.FunctionRunning{}, "running", "", ""},
		{"succeeded", automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: revision}, "succeeded", "succeeded", ""},
		{"rejected before admission", automations.FunctionFailed{CompletedAt: at, Evidence: automations.FunctionAdmissionRejected{ReasonCode: "function_contract_changed"}}, "failed", "", "function_contract_changed"},
		{"provider failure", automations.FunctionFailed{CompletedAt: at, Evidence: automations.FunctionInvocationFailed{CatalogRevision: revision, Outcome: adapters.Failed{ReasonCode: "provider_refused"}}}, "failed", "failed", "provider_refused"},
		{"not executed", automations.FunctionFailed{CompletedAt: at, Evidence: automations.FunctionInvocationFailed{CatalogRevision: revision, Outcome: adapters.NotExecuted{ReasonCode: "adapter_busy"}}}, "failed", "not_executed", "adapter_busy"},
		{"unknown", automations.FunctionFailed{CompletedAt: at, Evidence: automations.FunctionInvocationFailed{CatalogRevision: revision, Outcome: adapters.Unknown{ReasonCode: "outcome_timeout"}}}, "failed", "unknown", "outcome_timeout"},
		{"interrupted", automations.FunctionInterrupted{CompletedAt: at, ReasonCode: "core_restarted"}, "interrupted", "", "core_restarted"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			raw, encodeErr := automations.EncodePublicStepExecution(automations.StepExecution{
				StepID: "inspect", Sequence: 0,
				Details: automations.FunctionExecutionDetails{StartedAt: at.Add(time.Second),
					InvocationID: "afi_01980000-0000-7000-8000-000000000001", State: test.state},
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			var actual map[string]json.RawMessage
			if decodeErr := json.Unmarshal(raw, &actual); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			var evidence map[string]json.RawMessage
			if decodeErr := json.Unmarshal(actual["evidence"], &evidence); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if got := strings.Trim(string(actual["phase"]), `"`); got != test.status {
				t.Fatalf("phase = %s, want %q in %s", got, test.status, raw)
			}
			for field, want := range map[string]string{"invocation_outcome": test.outcome, "reason_code": test.reason} {
				got := strings.Trim(string(evidence[field]), `"`)
				if got != want {
					t.Fatalf("evidence.%s = %s, want %q in %s", field, got, want, raw)
				}
			}
			if string(actual["started_at"]) != `"2026-10-23T00:00:01Z"` ||
				(test.status != "running" && string(actual["completed_at"]) != `"2026-10-23T00:00:00Z"`) {
				t.Fatalf("diagnostic times changed: %s", raw)
			}
			_, hasRevision := evidence["catalog_revision"]
			if hasRevision != (test.outcome != "") {
				t.Fatalf("fabricated/missing catalog revision: %s", raw)
			}
			value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if validateErr := schema.Validate(value); validateErr != nil {
				t.Fatalf("invalid public evidence: %s %v", raw, validateErr)
			}
		})
	}
}

func TestFunctionEvidenceSchemaRejectsContradictions(t *testing.T) {
	t.Parallel()
	schema, err := historySchemaCompiler(
		t,
	).Compile("urn:hearth:schema:automation-history:v3#/$defs/functionStepExecution")
	if err != nil {
		t.Fatal(err)
	}
	const common = `"step_id":"inspect","sequence":0,"kind":"function","started_at":"2026-10-23T00:00:00Z"`
	for _, tail := range []string{
		`"phase":"running","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","invocation_outcome":"succeeded"}`,
		`"phase":"succeeded","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001"}`,
		`"phase":"failed","completed_at":"2026-10-23T00:00:00Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"function_contract_changed","invocation_outcome":"succeeded"}`,
		`"phase":"interrupted","completed_at":"2026-10-23T00:00:00Z","evidence":{"invocation_id":"afi_01980000-0000-7000-8000-000000000001","reason_code":"core_restarted","catalog_revision":"afc_01980000-0000-7000-8000-000000000001"}`,
	} {
		value, decodeErr := jsonschema.UnmarshalJSON(strings.NewReader("{" + common + "," + tail + "}"))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if schema.Validate(value) == nil {
			t.Fatalf("contradictory evidence accepted: %s", tail)
		}
	}
}
