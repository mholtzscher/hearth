package automations

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestValueInitializationClosedShapeAndLiteralNormalization(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := Definition{
		Variables: map[VariableName]ValueBinding{"n": {Body: LiteralValue{Value: json.RawMessage("1.0")}}},
	}
	init := RunValueInitialization{RecordedAt: at, Body: ValuesInitialized{Values: json.RawMessage(`{"n":1}`)}}
	if err := ValidateRunValueInitializationWithPreparedSnapshot(init, snapshot); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeRunValueInitialization(init)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeRunValueInitialization(raw); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"kind":"initialized","recorded_at":"2026-01-01T00:00:00Z","values":{},"extra":1}`, `{"kind":"failed","recorded_at":"2026-01-01T00:00:00Z","failure":{"path":"/trigger","failure_code":"expression_type","extra":1}}`} {
		if _, err = DecodeRunValueInitialization(json.RawMessage(bad)); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestStateEvidenceRejectsExplicitEmptyReason(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	empty := ConditionUnknownReason("")
	d := StateExecutionDetails{At: at, ReadAt: at, Output: json.RawMessage(`null`), UnusableReason: &empty}
	if _, err := EncodeStepExecutionDetails(d); err == nil {
		t.Fatal("accepted empty State unusable reason")
	}
}

func TestFunctionProviderSuccessValueFailurePreservesProviderOutcome(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	d := FunctionExecutionDetails{
		StartedAt:    at,
		InvocationID: adapters.InvocationID("afi_01900000-0000-7000-8000-000000000001"),
		State: FunctionSucceeded{
			CompletedAt:     at,
			CatalogRevision: adapters.CatalogID("afc_01900000-0000-7000-8000-000000000001"),
		},
		ValueFailure: &ValueFailure{Path: "/steps/0", FailureCode: "expression_limit"},
	}
	env, err := EncodeStepExecutionDetails(d)
	if err != nil {
		t.Fatal(err)
	}
	if env.Phase != executionFailed {
		t.Fatal(env.Phase)
	}
	decoded, err := DecodeStepExecutionDetails("one", env)
	if err != nil {
		t.Fatal(err)
	}
	got := decoded.(FunctionExecutionDetails)
	if _, ok := got.State.(FunctionSucceeded); !ok {
		t.Fatalf("provider success lost: %#v", got.State)
	}
	d.ValueFailure.FailureCode = "expression_missing"
	if _, err = EncodeStepExecutionDetails(d); err == nil {
		t.Fatal("accepted impossible post-success failure")
	}
}

func TestPreReservationFailureRequiresClosedEvidence(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	env := StepExecutionEnvelope{
		Kind:        StepKindCommand,
		Phase:       executionFailed,
		StartedAt:   at,
		CompletedAt: &at,
		EvidenceJSON: json.RawMessage(
			`{"value_failure":{"path":"/steps/0/parameters","failure_code":"expression_type"}}`,
		),
	}
	if _, err := DecodeStepExecutionDetails("one", env); err != nil {
		t.Fatal(err)
	}
	env.EvidenceJSON = json.RawMessage(
		`{"value_failure":{"path":"/steps/0/parameters","failure_code":"expression_type"},"command_id":"cmd_01900000-0000-7000-8000-000000000001"}`,
	)
	if _, err := DecodeStepExecutionDetails("one", env); err == nil {
		t.Fatal("accepted partial reservation")
	}
}

func TestSuccessfulRecordedTraceRequiresDataSteps(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	snapshot := Definition{
		Steps: []Step{
			{
				ID: "one",
				Body: SetVariablesStep{
					Assignments: map[VariableName]ValueBinding{"v": {Body: LiteralValue{Value: json.RawMessage(`1`)}}},
				},
			},
		},
	}
	init := RunValueInitialization{RecordedAt: at, Body: ValuesInitialized{Values: json.RawMessage(`{}`)}}
	if err := ValidateRunValueTraceWithPreparedSnapshot(snapshot, nil, RunSucceeded, &init, nil, true); err == nil {
		t.Fatal("accepted missing data Step")
	}
	trace := []StepExecution{
		{StepID: "one", Sequence: 0, Details: VariablesExecutionDetails{At: at, Values: json.RawMessage(`{"v":1}`)}},
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(snapshot, nil, RunSucceeded, &init, trace, true); err != nil {
		t.Fatal(err)
	}
}
