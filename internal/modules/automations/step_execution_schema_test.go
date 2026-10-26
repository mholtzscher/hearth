package automations_test

import (
	"bytes"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestExecutionSchemaLifecycleAndBranchEvidence(t *testing.T) {
	t.Parallel()
	compiler := historySchemaCompiler(t)
	schema, err := compiler.Compile("urn:hearth:schema:automation-history:v2#/$defs/execution")
	if err != nil {
		t.Fatal(err)
	}
	const at = "2026-10-04T10:00:00Z"
	const commandID = "cmd_018f47c2-7b3d-7abc-8abc-123456789abc"
	const correlationID = "cor_018f47c2-7b3d-7abc-8abc-123456789abc"
	const evaluation = `{"evaluated_at":"` + at + `","result":"false","nodes":[{"id":"match","kind":"trigger","result":"false","matched_trigger_ids":[]}]}`
	for _, test := range []struct {
		name  string
		value string
		valid bool
	}{
		{
			name:  "succeeded command requires completion",
			value: `{"step_id":"send","sequence":0,"kind":"command","phase":"succeeded","started_at":"` + at + `","evidence":{"command_id":"` + commandID + `","correlation_id":"` + correlationID + `","verified_command_id":"` + commandID + `","command_outcome":"satisfied"}}`,
		},
		{
			name:  "succeeded command requires verified link",
			value: `{"step_id":"send","sequence":0,"kind":"command","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"command_id":"` + commandID + `","correlation_id":"` + correlationID + `","command_outcome":"satisfied"}}`,
		},
		{
			name:  "canonical succeeded command",
			value: `{"step_id":"send","sequence":0,"kind":"command","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"command_id":"` + commandID + `","correlation_id":"` + correlationID + `","verified_command_id":"` + commandID + `","command_outcome":"satisfied"}}`,
			valid: true,
		},
		{
			name:  "branch cannot be running",
			value: `{"step_id":"route","sequence":0,"kind":"if","phase":"running","started_at":"` + at + `","evidence":{"outcome":"else","evaluations":[{"evaluation":` + evaluation + `}]}}`,
		},
		{
			name:  "if rejects selected branch id",
			value: `{"step_id":"route","sequence":0,"kind":"if","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"outcome":"else","selected_branch_id":"other","evaluations":[{"evaluation":` + evaluation + `}]}}`,
		},
		{
			name:  "choose rejects malformed selected id",
			value: `{"step_id":"route","sequence":0,"kind":"choose","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"outcome":"branch","selected_branch_id":"not valid","evaluations":[{"branch_id":"other","evaluation":{"evaluated_at":"` + at + `","result":"true","nodes":[{"id":"match","kind":"trigger","result":"true","matched_trigger_ids":["button"]}]}}]}}`,
		},
		{
			name:  "then outcome requires true evaluation",
			value: `{"step_id":"route","sequence":0,"kind":"if","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"outcome":"then","evaluations":[{"evaluation":` + evaluation + `}]}}`,
		},
		{
			name:  "choose branch requires true evaluation",
			value: `{"step_id":"route","sequence":0,"kind":"choose","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"outcome":"branch","selected_branch_id":"other","evaluations":[{"branch_id":"other","evaluation":` + evaluation + `}]}}`,
		},
		{
			name:  "unknown branch outcome cannot succeed",
			value: `{"step_id":"route","sequence":0,"kind":"choose","phase":"succeeded","started_at":"` + at + `","completed_at":"` + at + `","evidence":{"outcome":"unknown","failure_code":"branch_condition_unknown","evaluations":[{"branch_id":"other","evaluation":{"evaluated_at":"` + at + `","result":"unknown","nodes":[{"id":"state","kind":"entity_state","result":"unknown","unknown_reason":"entity_missing"}]}}]}}`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, decodeErr := jsonschema.UnmarshalJSON(bytes.NewBufferString(test.value))
			if decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if validateErr := schema.Validate(value); (validateErr == nil) != test.valid {
				t.Fatalf("valid = %v, want %v: %v", validateErr == nil, test.valid, validateErr)
			}
		})
	}
}

func TestPublicExecutionEncoderSequenceBounds(t *testing.T) {
	t.Parallel()
	for _, sequence := range []int{-1, 0, 63, 64} {
		raw, err := automations.EncodePublicStepExecution(automations.StepExecution{
			StepID: "wait", Sequence: sequence,
			Details: automations.DelayExecutionDetails{StartedAt: time.Date(2026, 10, 4, 10, 0, 0, 0, time.UTC)},
		})
		valid := sequence >= 0 && sequence <= 63
		if (err == nil) != valid {
			t.Fatalf("sequence %d accepted=%t, want %t: %v", sequence, err == nil, valid, err)
		}
		if valid && len(raw) == 0 {
			t.Fatalf("sequence %d produced no execution", sequence)
		}
	}
}
