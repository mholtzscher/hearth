//nolint:testpackage // Exercises canonical private schema bytes and snapshot-relative evidence contracts together.
package automations

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestStateObservationWireNames(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	obs := &ObservationEvidence{
		ObservationID: devices.ObservationID("obs_01900000-0000-7000-8000-000000000001"),
		ObservedAt:    at,
	}
	d := StateExecutionDetails{At: at, ReadAt: at, Observation: obs, Output: json.RawMessage(`1`)}
	env, err := EncodeStepExecutionDetails(d)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(env.EvidenceJSON, []byte(`"observation_id"`)) ||
		bytes.Contains(env.EvidenceJSON, []byte(`"ObservationID"`)) {
		t.Fatalf("unexpected observation wire: %s", env.EvidenceJSON)
	}
	if _, err = DecodeStepExecutionDetails("read", env); err != nil {
		t.Fatal(err)
	}
	reason := ConditionUnknownEvidenceInFuture
	f := ValueFailureExecutionDetails{
		StepKind: StepKindReadState,
		At:       at,
		Failure: ValueFailure{
			FailureCode: "read_state_unusable",
			State: &StateReadFailure{
				EntityID:       devices.EntityID("ent_01900000-0000-7000-8000-000000000001"),
				Pointer:        "",
				ReadAt:         at,
				Observation:    obs,
				UnusableReason: reason,
			},
		},
	}
	env, err = EncodeStepExecutionDetails(f)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(env.EvidenceJSON, []byte(`"observed_at"`)) ||
		bytes.Contains(env.EvidenceJSON, []byte(`"ObservedAt"`)) {
		t.Fatalf("unexpected failure wire: %s", env.EvidenceJSON)
	}
	if _, err = DecodeStepExecutionDetails("read", env); err != nil {
		t.Fatal(err)
	}
}

func TestValueFailureFieldsIdentifyLogicalInput(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		body  StepBody
		field string
	}{
		{CommandStep{EntityID: "ent_01980000-0000-7000-8000-000000000001", OperationName: "set", Parameters: flowExpression("vars.x")}, "parameters"},
		{FunctionStep{AdapterID: "example", FunctionKey: "run", ContractFingerprint: "sha256-v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef", Arguments: flowExpression("vars.x")}, "arguments"},
		{NotificationStep{DestinationID: "ndst_01980000-0000-7000-8000-000000000001", Title: flowExpression("vars.x"), Message: flowLiteral(`"hello"`)}, "title"},
		{SetVariablesStep{Assignments: map[VariableName]ValueBinding{"x": flowExpression("vars.x")}}, "assignments"},
	} {
		snapshot := flowDefinition(t, nil, []Step{{ID: "step", Body: test.body}})
		field := test.field
		if field == "assignments" {
			field += ".x"
		}
		for _, candidate := range []string{field, field + "_expr", "variables.x"} {
			execution := StepExecution{StepID: "step", Details: ValueFailureExecutionDetails{
				StepKind: StepBodyKind(test.body), At: at,
				Failure: ValueFailure{Field: candidate, FailureCode: "expression_type"},
			}}
			err := ValidateStepValueOwnershipWithPreparedSnapshot(execution, snapshot, true)
			if (err == nil) != (candidate == field) {
				t.Errorf("field %s: %v", candidate, err)
			}
		}
	}
	snapshot := flowDefinition(
		t,
		map[VariableName]ValueBinding{"x": flowExpression("trigger.x")},
		[]Step{{ID: "wait", Body: DelayStep{DurationMS: 1}}},
	)
	for _, field := range []string{"variables.x", "variables_expr.x", "variables.unknown"} {
		init := RunValueInitialization{RecordedAt: at, Body: InitializationFailed{
			Failure: ValueFailure{Field: field, FailureCode: "expression_type"},
		}}
		_, err := EncodeRunValueInitializationWithPreparedSnapshot(init, snapshot)
		if (err == nil) != (field == "variables.x") {
			t.Errorf("initial field %s: %v", field, err)
		}
	}
}

func TestBranchFailureRequiresValueEvidenceAndExactBinding(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expr := Condition{
		ID:   "expr",
		Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "vars.x"}}},
	}
	literal := Condition{
		ID:   "literal",
		Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "true"}}},
	}
	root := Condition{
		ID:   "all",
		Body: AllCondition{Children: []Condition{expr, literal, {ID: "trigger", Body: TriggerCondition{}}}},
	}
	snapshot := Definition{Steps: []Step{{ID: "route", Body: IfStep{Conditions: root}}}}
	failure := ValueFailure{
		ConditionID: "expr",
		FailureCode: "expression_missing",
	}
	details := BranchExecutionDetails{
		Decision: BranchDecision{
			StepID:      "route",
			EvaluatedAt: at,
			Body:        IfDecision{Result: IfError{FailureCode: "expression_missing"}},
		},
		ValueFailure: &failure,
	}
	exec := StepExecution{StepID: "route", Details: details}
	if err := ValidateStepValueOwnershipWithPreparedSnapshot(exec, snapshot, true); err != nil {
		t.Fatal(err)
	}
	env, err := EncodeStepExecutionDetails(details)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = DecodeStepExecutionDetails("route", env); err != nil {
		t.Fatal(err)
	}
	env.EvidenceJSON = json.RawMessage(`{"outcome":"error","evaluations":[],"failure_code":"expression_missing"}`)
	if _, err = DecodeStepExecutionDetails("route", env); err == nil {
		t.Error("decoder accepted missing value failure")
	}
	for _, id := range []ConditionID{"", "all", "trigger", "unknown"} {
		bad := failure
		bad.ConditionID = id
		d := details
		d.ValueFailure = &bad
		exec.Details = d
		if err = ValidateStepValueOwnershipWithPreparedSnapshot(exec, snapshot, true); err == nil {
			t.Errorf("accepted Condition %s", id)
		}
	}
	details.ValueFailure = nil
	if _, err = EncodeStepExecutionDetails(details); err == nil {
		t.Error("accepted expression error without value failure")
	}
}

func TestChooseFailureNamesNextAlternative(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	expr := Condition{
		ID:   "expr",
		Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "vars.x"}}},
	}
	first := Condition{
		ID:   "first",
		Body: ExpressionCondition{Value: ValueBinding{Body: ExpressionValue{Source: "false"}}},
	}
	snapshot := Definition{
		Steps: []Step{
			{
				ID: "route",
				Body: ChooseStep{
					Branches: []ChooseBranch{
						{ID: "a", Conditions: first},
						{ID: "b", Conditions: expr},
						{ID: "c", Conditions: expr},
					},
				},
			},
		},
	}
	prefix := []ChooseEvaluation{
		{
			BranchID: "a",
			Evaluation: ConditionEvaluation{
				EvaluatedAt: at,
				Result:      ConditionFalse,
				Nodes:       []ConditionNodeResult{{ID: "first", Evidence: ExpressionMatchEvidence{Matched: false}}},
			},
		},
	}
	failure := ValueFailure{ConditionID: "expr", FailureCode: "expression_type"}
	d := BranchExecutionDetails{
		Decision: BranchDecision{
			StepID:      "route",
			EvaluatedAt: at,
			Body:        ChooseDecision{Result: ChooseError{FailureCode: "expression_type", Evaluations: prefix}},
		},
		ValueFailure: &failure,
	}
	execution := StepExecution{StepID: "route", Details: d}
	if err := ValidateStepValueOwnershipWithPreparedSnapshot(execution, snapshot, true); err != nil {
		t.Fatal(err)
	}
	for _, id := range []ConditionID{"first", "unknown", ""} {
		f := failure
		f.ConditionID = id
		bad := d
		bad.ValueFailure = &f
		execution.Details = bad
		if err := ValidateStepValueOwnershipWithPreparedSnapshot(execution, snapshot, true); err == nil {
			t.Errorf("accepted Condition outside failing root %s", id)
		}
	}
	bad := d
	wrongPrefix := append([]ChooseEvaluation(nil), prefix...)
	wrongPrefix[0].BranchID = "c"
	bad.Decision.Body = ChooseDecision{Result: ChooseError{FailureCode: "expression_type", Evaluations: wrongPrefix}}
	execution.Details = bad
	if err := ValidateBranchDecisionWithPreparedSnapshot(bad.Decision, snapshot, nil); err == nil {
		t.Error("accepted wrong Choose prefix identity")
	}
}

func TestBranchErrorEvidenceCodeMatrix(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	for _, code := range []string{"expression_missing", "expression_type", "expression_evaluation", "expression_value_invalid", "expression_limit", "resolved_input_invalid", "run_data_limit"} {
		t.Run(code, func(t *testing.T) {
			t.Parallel()
			d := BranchExecutionDetails{
				Decision: BranchDecision{
					StepID:      "route",
					EvaluatedAt: at,
					Body:        IfDecision{Result: IfError{FailureCode: code}},
				},
			}
			if _, err := EncodeStepExecutionDetails(d); err == nil {
				t.Error("accepted missing failure")
			}
			d.ValueFailure = &ValueFailure{ConditionID: "check", FailureCode: code}
			if _, err := EncodeStepExecutionDetails(d); err != nil {
				t.Fatal(err)
			}
			d.ValueFailure = &ValueFailure{ConditionID: "check", FailureCode: "expression_missing"}
			if code != "expression_missing" {
				if _, err := EncodeStepExecutionDetails(d); err == nil {
					t.Error("accepted mismatched failure code")
				}
			}
		})
	}
	d := BranchExecutionDetails{
		Decision: BranchDecision{
			StepID:      "route",
			EvaluatedAt: at,
			Body:        IfDecision{Result: IfError{FailureCode: "branch_state_read_failed"}},
		},
	}
	if _, err := EncodeStepExecutionDetails(d); err != nil {
		t.Fatal(err)
	}
	d.ValueFailure = &ValueFailure{ConditionID: "check", FailureCode: "expression_missing"}
	if _, err := EncodeStepExecutionDetails(d); err == nil {
		t.Error("accepted value failure for operational error")
	}
}

func TestRunValueTraceChecksFailureCode(t *testing.T) {
	t.Parallel()
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	failed := func(code string) RunState {
		return CompletedRun{CompletedAt: at, Outcome: FailedRun{FailureCode: code}}
	}
	initFailure := RunValueInitialization{
		RecordedAt: at,
		Body:       InitializationFailed{Failure: ValueFailure{Field: "trigger", FailureCode: "expression_limit"}},
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(
		Definition{},
		nil,
		failed("expression_limit"),
		&initFailure,
		nil,
		true,
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(
		Definition{},
		nil,
		failed("expression_type"),
		&initFailure,
		nil,
		true,
	); err == nil {
		t.Error("accepted wrong initialization parent code")
	}
	snapshot := Definition{
		Steps: []Step{
			{
				ID: "set",
				Body: SetVariablesStep{
					Assignments: map[VariableName]ValueBinding{"v": {Body: ExpressionValue{Source: "vars.x"}}},
				},
			},
		},
	}
	init := RunValueInitialization{RecordedAt: at, Body: ValuesInitialized{Values: json.RawMessage(`{}`)}}
	trace := []StepExecution{
		{
			StepID:   "set",
			Sequence: 0,
			Details: ValueFailureExecutionDetails{
				StepKind: StepKindSetVariables,
				At:       at,
				Failure:  ValueFailure{Field: "assignments.v", FailureCode: "expression_missing"},
			},
		},
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(
		snapshot,
		nil,
		failed("expression_missing"),
		&init,
		trace,
		true,
	); err != nil {
		t.Fatal(err)
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(
		snapshot,
		nil,
		failed("expression_type"),
		&init,
		trace,
		true,
	); err == nil {
		t.Error("accepted wrong Step parent code")
	}
	if err := ValidateRunValueTraceWithPreparedSnapshot(
		snapshot,
		nil,
		CompletedRun{CompletedAt: at, Outcome: FailedRun{}},
		&init,
		nil,
		true,
	); err == nil {
		t.Error("accepted empty parent failure")
	}
}

func TestUnifiedExecutionHistorySchemaValueShapes(t *testing.T) {
	t.Parallel()
	compiler := jsonschema.NewCompiler()
	for uri, raw := range map[string][]byte{"urn:hearth:schema:automation-history:v3": AutomationHistorySchema(), "urn:hearth:schema:automation-definition:v3": automationDefinitionSchema} {
		doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(raw))
		if err != nil {
			t.Fatal(err)
		}
		if err = compiler.AddResource(uri, doc); err != nil {
			t.Fatal(err)
		}
	}
	schema, err := compiler.Compile("urn:hearth:schema:automation-history:v3#/$defs/execution")
	if err != nil {
		t.Fatal(err)
	}
	const at = "2026-01-01T00:00:00Z"
	const inv = "afi_01900000-0000-7000-8000-000000000001"
	const cat = "afc_01900000-0000-7000-8000-000000000001"
	const failure = `{"failure_code":"expression_limit"}`
	base := func(kind, phase, evidence string) string {
		return `{"step_id":"one","sequence":0,"kind":"` + kind + `","phase":"` + phase + `","started_at":"` + at + `","completed_at":"` + at + `","evidence":` + evidence + `}`
	}
	function := `"invocation_id":"` + inv + `","catalog_revision":"` + cat + `","invocation_outcome":"succeeded"`
	for _, tc := range []struct {
		name, raw string
		valid     bool
	}{
		{"function value failure", base("function", "failed", `{`+function+`,"value_failure":`+failure+`}`), true},
		{"function scalar output", base("function", "succeeded", `{`+function+`,"inputs":{"arguments":{}},"output":9007199254740993}`), true},
		{"legacy function success", base("function", "succeeded", `{`+function+`}`), true},
		{"pre-reservation failure", base("function", "failed", `{"value_failure":{"field":"arguments","failure_code":"expression_missing"}}`), true},
		{"pre-reservation failure with external id", base("function", "failed", `{"invocation_id":"`+inv+`","value_failure":{"field":"arguments","failure_code":"expression_missing"}}`), false},
		{"assignment foreign field", base("set_variables", "succeeded", `{"values":{"x":1},"output":1}`), false},
		{"function successful plus failure", base("function", "succeeded", `{`+function+`,"value_failure":`+failure+`}`), false},
		{"function failure plus output", base("function", "failed", `{`+function+`,"output":1,"value_failure":`+failure+`}`), false},
		{"function invalid failure class", base("function", "failed", `{`+function+`,"value_failure":{"failure_code":"expression_missing"}}`), false},
		{"function running with output", `{"step_id":"one","sequence":0,"kind":"function","phase":"running","started_at":"` + at + `","evidence":{"invocation_id":"` + inv + `","output":1}}`, false},
		{"State usable without observation", base("read_state", "succeeded", `{"output":1,"read_at":"`+at+`"}`), false},
		{"State usable", base("read_state", "succeeded", `{"output":null,"read_at":"`+at+`","observation":{"observation_id":"obs_01900000-0000-7000-8000-000000000001","observed_at":"`+at+`"}}`), true},
		{"State fallback with nonnull output", base("read_state", "succeeded", `{"output":1,"read_at":"`+at+`","unusable_reason":"state_missing"}`), false},
		{"State missing with observation", base("read_state", "succeeded", `{"output":null,"read_at":"`+at+`","unusable_reason":"state_missing","observation":{"observation_id":"obs_01900000-0000-7000-8000-000000000001","observed_at":"`+at+`"}}`), false},
		{"State expired without observation", base("read_state", "succeeded", `{"output":null,"read_at":"`+at+`","unusable_reason":"evidence_expired"}`), false},
		{"State fallback", base("read_state", "succeeded", `{"output":null,"read_at":"`+at+`","unusable_reason":"state_missing"}`), true},
		{"branch error", base("if", "failed", `{"outcome":"error","evaluations":[],"failure_code":"expression_limit","value_failure":{"condition_id":"check","failure_code":"expression_limit"}}`), true},
		{"obsolete path", base("command", "failed", `{"value_failure":{"path":"/steps/0","failure_code":"expression_limit"}}`), false},
		{"obsolete span", base("command", "failed", `{"value_failure":{"field":"parameters","source_start":0,"source_end":1,"failure_code":"expression_limit"}}`), false},
		{"conflicting targets", base("command", "failed", `{"value_failure":{"field":"parameters","condition_id":"check","failure_code":"expression_limit"}}`), false},
		{"whole notification", base("notification", "failed", `{"value_failure":{"failure_code":"resolved_input_invalid"}}`), true},
		{"branch missing failure", base("if", "failed", `{"outcome":"error","evaluations":[],"failure_code":"expression_limit"}`), false},
		{"branch success plus failure", base("if", "succeeded", `{"outcome":"then","evaluations":[{"evaluation":{"evaluated_at":"`+at+`","result":"true","nodes":[{"id":"x","kind":"expression","result":"true","value":true}]}}],"value_failure":`+failure+`}`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value, e := jsonschema.UnmarshalJSON(strings.NewReader(tc.raw))
			if e != nil {
				t.Fatal(e)
			}
			e = schema.Validate(value)
			if (e == nil) != tc.valid {
				t.Fatalf("valid=%v want %v: %v", e == nil, tc.valid, e)
			}
		})
	}
}
