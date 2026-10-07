package automations_test

import (
	"bytes"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// The public contract must reject contradictory variants while accepting selected
// JSON null and exact numeric values. These are wire fixtures, not domain encodings.
func TestHistorySchemaEvidenceVariants(t *testing.T) {
	t.Parallel()
	compiler := historySchemaCompiler(t)
	for _, test := range []struct {
		name, definition, value string
		valid                   bool
	}{
		{"known null", "conditionNode", `{"id":"state","kind":"entity_state","result":"true","observation_id":"obs_1","observed_at":"2026-10-04T10:00:00Z","selected_value":null}`, true},
		{"known missing selection", "conditionNode", `{"id":"state","kind":"entity_state","result":"true","observation_id":"obs_1","observed_at":"2026-10-04T10:00:00Z"}`, false},
		{"missing entity", "conditionNode", `{"id":"state","kind":"entity_state","result":"unknown","unknown_reason":"entity_missing"}`, true},
		{"missing entity with selection", "conditionNode", `{"id":"state","kind":"entity_state","result":"unknown","unknown_reason":"entity_missing","selected_value":null}`, false},
		{"trigger match", "conditionNode", `{"id":"match","kind":"trigger","result":"true","matched_trigger_ids":["button"]}`, true},
		{"empty true intersection", "conditionNode", `{"id":"match","kind":"trigger","result":"true","matched_trigger_ids":[]}`, false},
		{"not configured", "conditionDecision", `{"mode":"not_configured","bypass_requested":false}`, true},
		{"contradictory bypass", "conditionDecision", `{"mode":"not_configured","bypass_requested":true}`, false},
		{"manual cause", "cause", `{"kind":"manual"}`, true},
		{"manual with fact", "cause", `{"kind":"manual","fact":{}}`, false},
		{"observation null predecessor", "cause", `{"kind":"device_fact","fact":{"family":"observation","fact_id":"fact_1","entity_id":"ent_1","emitted_at":"2026-10-04T10:00:00Z","observation_id":"obs_1","disposition":"applied","value":9007199254740993,"previous_value":null}}`, true},
		{"event with observation value", "cause", `{"kind":"device_fact","fact":{"family":"entity_event","fact_id":"fact_1","entity_id":"ent_1","emitted_at":"2026-10-04T10:00:00Z","event_id":"evt_1","name":"pressed","value":null}}`, false},
		{"interrupted before verification", "stepAttempt", `{"position":0,"step_id":"command","status":"interrupted","failure_code":"core_restart","started_at":"2026-10-04T10:00:00Z","completed_at":"2026-10-04T10:00:00Z"}`, true},
		{"success without verification", "stepAttempt", `{"position":0,"step_id":"command","status":"satisfied","started_at":"2026-10-04T10:00:00Z","completed_at":"2026-10-04T10:00:00Z"}`, false},
		{"if read error", "branchDecision", `{"position":0,"step_id":"if","kind":"if","evaluated_at":"2026-10-04T10:00:00Z","outcome":"error","failure_code":"branch_state_read_failed","evaluations":[]}`, true},
		{"unknown failure mislabeled error", "branchDecision", `{"position":0,"step_id":"if","kind":"if","evaluated_at":"2026-10-04T10:00:00Z","outcome":"error","failure_code":"branch_condition_unknown","evaluations":[]}`, false},
		{"if then with false root", "branchDecision", `{"position":0,"step_id":"if","kind":"if","evaluated_at":"2026-10-04T10:00:00Z","outcome":"then","evaluations":[{"evaluation":{"evaluated_at":"2026-10-04T10:00:00Z","result":"false","nodes":[{"id":"match","kind":"trigger","result":"false","matched_trigger_ids":[]}]}}]}`, false},
		{"admission with Trigger evidence", "admissionEvaluation", `{"evaluated_at":"2026-10-04T10:00:00Z","result":"false","nodes":[{"id":"match","kind":"trigger","result":"false","matched_trigger_ids":[]}]}`, false},
		{"invalid Event name", "fact", `{"family":"entity_event","fact_id":"fact_1","entity_id":"ent_1","emitted_at":"2026-10-04T10:00:00Z","event_id":"evt_1","name":"Pressed.With Spaces"}`, false},
		{"if selected branch", "branchDecision", `{"position":0,"step_id":"if","kind":"if","evaluated_at":"2026-10-04T10:00:00Z","outcome":"branch","selected_branch_id":"other","evaluations":[]}`, false},
		{"running delay", "delayExecution", `{"position":0,"step_id":"wait","duration_ms":86400000,"status":"running","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-05T10:00:00Z"}`, true},
		{"completed delay after clock rollback", "delayExecution", `{"position":0,"step_id":"wait","duration_ms":1,"status":"completed","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-04T10:00:00.001Z","completed_at":"2026-10-03T10:00:00Z"}`, true},
		{"running delay with completion", "delayExecution", `{"position":0,"step_id":"wait","duration_ms":1,"status":"running","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-04T10:00:00.001Z","completed_at":"2026-10-04T10:00:00Z"}`, false},
		{"interrupted delay without reason", "delayExecution", `{"position":0,"step_id":"wait","duration_ms":1,"status":"interrupted","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-04T10:00:00.001Z","completed_at":"2026-10-04T10:00:00Z"}`, false},
		{"interrupted delay with unrelated reason", "delayExecution", `{"position":0,"step_id":"wait","duration_ms":1,"status":"interrupted","started_at":"2026-10-04T10:00:00Z","due_at":"2026-10-04T10:00:00.001Z","completed_at":"2026-10-04T10:00:00Z","failure_code":"command_failed"}`, false},
	} {
		schema, compileErr := compiler.Compile("urn:hearth:schema:automation-history:v2#/$defs/" + test.definition)
		if compileErr != nil {
			t.Fatal(compileErr)
		}
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			value, err := jsonschema.UnmarshalJSON(bytes.NewBufferString(test.value))
			if err != nil {
				t.Fatal(err)
			}
			if err = schema.Validate(value); (err == nil) != test.valid {
				t.Fatalf("valid = %v, want %v: %v", err == nil, test.valid, err)
			}
		})
	}
}

func historySchemaCompiler(t *testing.T) *jsonschema.Compiler {
	t.Helper()
	codec, err := automations.NewDefinitionCodec()
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	for uri, raw := range map[string][]byte{
		"urn:hearth:schema:automation-definition:v2": codec.AutomationDefinitionSchema(),
		"urn:hearth:schema:automation-history:v2":    automations.AutomationHistorySchema(),
	} {
		document, decodeErr := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err = compiler.AddResource(uri, document); err != nil {
			t.Fatal(err)
		}
	}
	return compiler
}
