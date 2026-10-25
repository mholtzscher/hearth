//nolint:testpackage // Deciding checks exercise the executor-owned context and durable advancement gates.
package automations

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func flowLiteral(raw string) ValueBinding {
	return ValueBinding{Body: LiteralValue{Value: json.RawMessage(raw)}}
}
func flowExpression(source string) ValueBinding {
	return ValueBinding{Body: ExpressionValue{Source: source}}
}
func flowDefinition(t *testing.T, variables map[VariableName]ValueBinding, steps []Step) Definition {
	t.Helper()
	definition, err := NormalizeDefinition(Definition{Name: "Values", Enabled: true, Variables: variables,
		Triggers: []Trigger{{ID: "tick", Body: CronTrigger{Expression: "* * * * *"}}}, Steps: steps})
	if err != nil {
		t.Fatal(err)
	}
	return definition
}
func flowRun(definition Definition) Run {
	return Run{
		ID:           "arn_01980000-0000-7000-8000-000000000001",
		AutomationID: "aut_01980000-0000-7000-8000-000000000001",
		Revision:     1,
		Snapshot:     definition,
		Cause:        ManualCause{},
		StartedAt:    time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC),
	}
}

func TestV3BindingWireAndPlacement(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{}`, `null`, `"x"`, `{"literal":1,"expr":"1"}`, `{"literal":1,"extra":true}`, `{"expr":null}`, `{"expr":1}`, `{"expr":" "}`, `{"literal":1,"literal":2}`} {
		var binding ValueBinding
		if err := json.Unmarshal([]byte(raw), &binding); err == nil {
			t.Errorf("accepted invalid binding %s", raw)
		}
	}
	for _, raw := range []string{`{"literal":null}`, `{"literal":{"expr":"vars.not_code","literal":9007199254740993}}`, `{"expr":"vars.x"}`} {
		var binding ValueBinding
		if err := json.Unmarshal([]byte(raw), &binding); err != nil {
			t.Fatal(err)
		}
		encoded, err := json.Marshal(binding)
		if err != nil || string(encoded) != raw {
			t.Fatalf("round trip %s = %s, %v", raw, encoded, err)
		}
	}
	for _, source := range []string{`vars.x`, `get(vars, "x", 1)`, `has(steps, "output")`, `false ? vars.x : 1`} {
		_, err := normalizeVariables(map[VariableName]ValueBinding{"x": flowExpression(source)}, true)
		if err == nil {
			t.Errorf("initial root accepted: %s", source)
		}
	}
	for _, name := range []VariableName{"", "1x", "x.y", "__hearth_private", VariableName(strings.Repeat("a", 64))} {
		if _, err := normalizeVariables(map[VariableName]ValueBinding{name: flowLiteral(`null`)}, false); err == nil {
			t.Errorf("accepted name %q", name)
		}
	}
	read := Step{ID: "read", Body: ReadStateStep{EntityID: "ent_01980000-0000-7000-8000-000000000001", Pointer: ""}}
	for _, source := range []string{`steps.missing.output`, `steps.wait.output`} {
		_, err := NormalizeDefinition(
			Definition{
				Name:     "Invalid",
				Triggers: []Trigger{{ID: "tick", Body: CronTrigger{Expression: "* * * * *"}}},
				Steps: []Step{
					{
						ID:   "wait",
						Body: DelayStep{DurationMS: 1},
					},
					{
						ID:   "assign",
						Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"x": flowExpression(source)}},
					},
				},
			},
		)
		if err == nil {
			t.Errorf("accepted static non-output reference %s", source)
		}
	}
	definition := flowDefinition(
		t,
		nil,
		[]Step{
			read,
			{
				ID: "assign",
				Body: SetVariablesStep{
					Assignments: map[VariableName]ValueBinding{"x": flowExpression(`steps.read.output`)},
				},
			},
		},
	)
	definition.Conditions = &Condition{
		ID:   "group",
		Body: AllCondition{Children: []Condition{{ID: "expr", Body: ExpressionCondition{Value: flowLiteral(`true`)}}}},
	}
	if _, err := NormalizeDefinition(definition); err == nil {
		t.Fatal("admission expression accepted recursively")
	}
}

type flowRepository struct {
	Repository

	data       []RunDataRecord
	failure    *RunDataRecord
	writeErr   error
	interrupts int
	branches   []BranchDecision
}

func (repo *flowRepository) RecordRunData(_ context.Context, _ RunID, record RunDataRecord) error {
	if repo.writeErr != nil {
		return repo.writeErr
	}
	repo.data = append(repo.data, record)
	return nil
}
func (repo *flowRepository) FailRunData(_ context.Context, _ RunID, record RunDataRecord) error {
	repo.failure = &record
	return nil
}
func (repo *flowRepository) InterruptRun(context.Context, RunID, time.Time) error {
	repo.interrupts++
	return nil
}

func (repo *flowRepository) RecordBranchDecision(
	_ context.Context,
	_ RunID,
	decision BranchDecision,
	data *RunDataRecord,
) error {
	if repo.writeErr != nil {
		return repo.writeErr
	}
	if data != nil {
		repo.failure = data
	}
	repo.branches = append(repo.branches, decision)
	return nil
}

//nolint:gocognit // The successful/failed atomic gate matrix checks decision, failure and live context together.
func TestExpressionBranchFailureUsesOneAtomicDecisionGate(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(t, nil, []Step{{ID: "route", Body: ChooseStep{Branches: []ChooseBranch{
		{
			ID:         "first",
			Conditions: Condition{ID: "false", Body: ExpressionCondition{Value: flowLiteral(`false`)}},
			Steps:      []Step{{ID: "a", Body: DelayStep{DurationMS: 1}}},
		},
		{
			ID:         "error",
			Conditions: Condition{ID: "missing", Body: ExpressionCondition{Value: flowExpression(`vars.missing`)}},
			Steps:      []Step{{ID: "b", Body: DelayStep{DurationMS: 1}}},
		},
	}, Default: []Step{{ID: "fallback", Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"wrong": flowLiteral(`true`)}}}}}}})
	for _, failCommit := range []bool{false, true} {
		run := flowRun(definition)
		values, _ := newRunValues(run)
		repo := &flowRepository{}
		service := NewService(repo, &flowDevices{}, Dependencies{})
		if !service.initializeValues(t.Context(), run, values) {
			t.Fatal("initialization failed")
		}
		if failCommit {
			repo.writeErr = errors.New("branch commit failed")
		}
		if service.executeBranch(t.Context(), run, definition.Steps[0], &executionCursor{values: values}) {
			t.Fatal("branch error selected fallback")
		}
		if len(values.env.Vars) != 0 {
			t.Fatal("failed branch advanced context")
		}
		if failCommit {
			if len(repo.branches) != 0 || repo.failure != nil || repo.interrupts != 1 {
				t.Fatal("failed decision commit advanced or omitted interruption")
			}
			continue
		}
		if len(repo.branches) != 1 || repo.failure == nil || repo.branches[0].Outcome() != BranchError {
			t.Fatal("expression error was not committed with decision")
		}
		failure := repo.failure.Body.(RunDataFailed)
		if failure.Path != "/steps/0/branches/1/conditions/value" || failure.FailureCode != "expression_missing" ||
			repo.failure.Sequence != 1 {
			t.Fatalf("failure = %#v", repo.failure)
		}
		if len(repo.branches[0].Body.(ChooseDecision).Result.(ChooseError).Evaluations) != 1 {
			t.Fatal("completed Choose prefix was lost")
		}
	}
}

type flowDevices struct {
	AutomationDevices

	snapshot devices.EntityStateSnapshot
	readErr  error
	requests [][]devices.EntityID
}

func (*flowDevices) CommandAdmissionOpen() bool { return true }

func (device *flowDevices) GetEntityStateSnapshot(
	_ context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	device.requests = append(device.requests, append([]devices.EntityID(nil), ids...))
	return device.snapshot, device.readErr
}

//nolint:gocognit // Each commit outcome checks the same live-context and journal invariants.
func TestVariablesResolveSimultaneouslyAndAdvanceOnlyAfterCommit(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(
		t,
		map[VariableName]ValueBinding{"a": flowLiteral(`9007199254740993`), "b": flowLiteral(`2`)},
		[]Step{
			{
				ID: "swap",
				Body: SetVariablesStep{
					Assignments: map[VariableName]ValueBinding{
						"a": flowExpression(`vars.b`),
						"b": flowExpression(`vars.a`),
					},
				},
			},
			{
				ID:   "fraction",
				Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"f": flowExpression(`3 / 1`)}},
			},
		},
	)
	run := flowRun(definition)
	for _, failCommit := range []bool{false, true} {
		repo := &flowRepository{}
		service := NewService(repo, &flowDevices{}, Dependencies{Now: func() time.Time { return run.StartedAt }})
		values, err := newRunValues(run)
		if err != nil {
			t.Fatal(err)
		}
		if !service.initializeValues(t.Context(), run, values) || len(repo.data) != 1 {
			t.Fatal("initialization did not commit")
		}
		if failCommit {
			repo.writeErr = errors.New("private database payload")
		}
		advanced := service.executeAssignment(
			t.Context(),
			run,
			values,
			"swap",
			definition.Steps[0].Body.(SetVariablesStep),
		)
		if advanced == failCommit {
			t.Fatalf("assignment advancement %t with failed commit %t", advanced, failCommit)
		}
		if failCommit {
			if values.env.Vars["a"] != int64(9007199254740993) || values.sequence != 1 || repo.interrupts != 1 {
				t.Fatal("failed commit changed context or omitted interruption")
			}
			continue
		}
		if values.env.Vars["a"] != int64(2) || values.env.Vars["b"] != int64(9007199254740993) {
			t.Fatalf("not simultaneous: %#v", values.env.Vars)
		}
		if !service.executeAssignment(
			t.Context(),
			run,
			values,
			"fraction",
			definition.Steps[1].Body.(SetVariablesStep),
		) {
			t.Fatal("fraction assignment failed")
		}
		if _, floating := values.env.Vars["f"].(float64); !floating {
			t.Fatal("live fractional result round-tripped into integer")
		}
		other, err := newRunValues(run)
		if err != nil || len(other.env.Vars) != 0 || len(other.env.Steps) != 0 {
			t.Fatal("Run contexts are shared")
		}
	}
}

func TestInitializationFailureAndDeterministicAssignmentFailure(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(
		t,
		map[VariableName]ValueBinding{"x": flowExpression(`trigger.missing`)},
		[]Step{{ID: "wait", Body: DelayStep{DurationMS: 1}}},
	)
	run := flowRun(definition)
	repo := &flowRepository{}
	service := NewService(repo, &flowDevices{}, Dependencies{})
	values, err := newRunValues(run)
	if err != nil {
		t.Fatal(err)
	}
	if service.initializeValues(t.Context(), run, values) || repo.failure == nil || len(repo.data) != 0 ||
		len(values.env.Vars) != 0 {
		t.Fatal("initial failure did not stop atomically")
	}
	failure := repo.failure.Body.(RunDataFailed)
	if failure.FailureCode != "expression_missing" || failure.Path != "/variables/x" || repo.failure.StepID != nil {
		t.Fatalf("initial failure = %#v", repo.failure)
	}
	bindings, err := normalizeVariables(
		map[VariableName]ValueBinding{"z": flowExpression(`vars.z`), "a": flowExpression(`vars.a`)},
		false,
	)
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = resolveAssignments(t.Context(), values, bindings, "/steps/0/assignments")
	if binding, ok := errors.AsType[*bindingError](err); !ok || binding.path != "/steps/0/assignments/a" {
		t.Fatalf("nondeterministic failure: %#v", err)
	}
}

type flowValidatedDataRepository struct {
	flowRepository

	snapshot Definition
}

func (repo *flowValidatedDataRepository) RecordRunData(ctx context.Context, id RunID, record RunDataRecord) error {
	if err := ValidateRunDataWithPreparedSnapshot(record, repo.snapshot); err != nil {
		return err
	}
	return repo.flowRepository.RecordRunData(ctx, id, record)
}

func TestResolvedFloatEvidencePreservesLiveValuesAtAssignmentGates(t *testing.T) {
	t.Parallel()
	for _, binding := range []ValueBinding{
		flowExpression(`float(9223372036854775807)`),
		flowLiteral(`0.9999999999999999999`),
	} {
		definition := flowDefinition(t, map[VariableName]ValueBinding{"n": binding}, []Step{{
			ID: "assign", Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"n": binding}},
		}})
		run := flowRun(definition)
		repo := &flowValidatedDataRepository{snapshot: definition}
		service := NewService(repo, &flowDevices{}, Dependencies{})
		values, err := newRunValues(run)
		if err != nil {
			t.Fatal(err)
		}
		if !service.initializeValues(t.Context(), run, values) {
			t.Errorf("valid floating-point initialization failed: %#v", repo.failure)
			continue
		}
		if !service.executeAssignment(t.Context(), run, values, "assign", definition.Steps[0].Body.(SetVariablesStep)) {
			t.Errorf("valid floating-point assignment failed: %#v", repo.failure)
			continue
		}
		want := float64(1)
		if _, expression := binding.Body.(ExpressionValue); expression {
			want = float64(math.MaxInt64)
		}
		if got, ok := values.env.Vars["n"].(float64); !ok || got != want {
			t.Errorf("live float lost its type/value: %#v", values.env.Vars["n"])
		}
		for _, record := range repo.data {
			if err = ValidateRunDataWithPreparedSnapshot(record, definition); err != nil {
				t.Errorf("normalized literal/expression evidence rejected: %v", err)
			}
		}
	}
}

func TestInitializationStillRejectsInvalidOriginalNumbers(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`9223372036854775808`, `-9223372036854775809`, `1e-400`, `1e1000000000`} {
		definition := flowDefinition(t, map[VariableName]ValueBinding{"n": flowLiteral(raw)}, []Step{{
			ID: "wait", Body: DelayStep{DurationMS: 1},
		}})
		run := flowRun(definition)
		repo := &flowRepository{}
		service := NewService(repo, &flowDevices{}, Dependencies{})
		values, err := newRunValues(run)
		if err != nil {
			t.Fatal(err)
		}
		if service.initializeValues(t.Context(), run, values) || len(repo.data) != 0 || len(values.env.Vars) != 0 ||
			repo.failure == nil || repo.failure.Body.(RunDataFailed).FailureCode != "expression_value_invalid" {
			t.Errorf("invalid original number advanced initialization: %s", raw)
		}
	}
}

//nolint:gocognit,modernize // The boundary matrix keeps each absence/error expectation alongside both fallback policies.
func TestStateCaptureUnusablePrecedenceAndOperationalFailures(t *testing.T) {
	t.Parallel()
	entity := devices.EntityID("ent_01980000-0000-7000-8000-000000000001")
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	maximum := int64(10)
	known := func(raw string, age time.Duration) devices.EntityStateSnapshotEntry {
		return devices.EntityStateSnapshotEntry{
			EntityID: entity,
			Exists:   true,
			State: &devices.State{
				EntityID:      entity,
				ObservationID: "obs_01980000-0000-7000-8000-000000000001",
				ObservedAt:    at.Add(-age),
				Value:         devices.Value(raw),
			},
		}
	}
	cases := []struct {
		name    string
		entry   *devices.EntityStateSnapshotEntry
		pointer string
		age     *int64
		readErr error
		reason  ConditionUnknownReason
		code    string
		output  string
	}{
		{"scalar", ptrEntry(known(`9007199254740993`, 0)), "", nil, nil, "", "", `9007199254740993`},
		{"object", ptrEntry(known(`{"x":true}`, 0)), "", nil, nil, "", "", `{"x":true}`},
		{"selected null", ptrEntry(known(`{"x":null}`, 0)), "/x", nil, nil, "", "", `null`},
		{"age boundary", ptrEntry(known(`1`, 10*time.Second)), "", &maximum, nil, "", "", `1`},
		{
			"missing entity",
			&devices.EntityStateSnapshotEntry{EntityID: entity},
			"",
			nil,
			nil,
			ConditionUnknownEntityMissing,
			"",
			`null`,
		},
		{
			"missing State",
			&devices.EntityStateSnapshotEntry{EntityID: entity, Exists: true},
			"",
			nil,
			nil,
			ConditionUnknownStateMissing,
			"",
			`null`,
		},
		{
			"future",
			ptrEntry(known(`{}`, -time.Second)),
			"/missing",
			&maximum,
			nil,
			ConditionUnknownEvidenceInFuture,
			"",
			`null`,
		},
		{
			"expired",
			ptrEntry(known(`{}`, 11*time.Second)),
			"/missing",
			&maximum,
			nil,
			ConditionUnknownEvidenceExpired,
			"",
			`null`,
		},
		{"unbounded future", ptrEntry(known(`1`, -time.Second)), "", nil, nil, "", "", `1`},
		{"pointer", ptrEntry(known(`{}`, 0)), "/missing", nil, nil, ConditionUnknownPointerMissing, "", `null`},
		{"read", nil, "", nil, errors.New("sensitive read failure"), "", "read_state_read_failed", ""},
		{"incomplete", nil, "", nil, nil, "", "read_state_snapshot_incomplete", ""},
		{"corrupt", ptrEntry(known(`{`, 11*time.Second)), "", &maximum, nil, "", "read_state_corrupt", ""},
		{
			"duplicate keys",
			ptrEntry(known(`{"x":1,"x":2}`, 11*time.Second)),
			"",
			&maximum,
			nil,
			"",
			"read_state_corrupt",
			"",
		},
		{
			"unsupported integer",
			ptrEntry(known(`9223372036854775808`, 11*time.Second)),
			"",
			&maximum,
			nil,
			"",
			"expression_value_invalid",
			"",
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, policy := range []ReadStateUnusablePolicy{ReadStateFail, ReadStateNull} {
				step := ReadStateStep{
					EntityID:      entity,
					Pointer:       test.pointer,
					MaxAgeSeconds: test.age,
					OnUnusable:    policy,
				}
				definition := flowDefinition(t, nil, []Step{{ID: "read", Body: step}})
				run := flowRun(definition)
				device := &flowDevices{
					snapshot: devices.EntityStateSnapshot{
						Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{},
					},
					readErr: test.readErr,
				}
				if test.entry != nil {
					device.snapshot.Entries[entity] = *test.entry
				}
				repo := &flowRepository{}
				service := NewService(repo, device, Dependencies{Now: func() time.Time { return at }})
				values, _ := newRunValues(run)
				values.sequence = 1
				ok := service.executeReadState(t.Context(), run, values, "read", step)
				code := test.code
				if test.reason != "" && policy == ReadStateFail {
					code = "read_state_unusable"
				}
				if code != "" {
					if ok || repo.failure == nil || len(values.env.Steps) != 0 ||
						repo.failure.Body.(RunDataFailed).FailureCode != code {
						t.Fatalf("failure %s: ok=%t record=%#v", code, ok, repo.failure)
					}
				} else {
					if !ok || len(repo.data) != 1 {
						t.Fatalf("capture failed: %#v", repo.failure)
					}
					body := repo.data[0].Body.(StateCaptured)
					if string(body.Output) != test.output || (body.UnusableReason != nil) != (test.reason != "") {
						t.Fatalf("capture = %#v", body)
					}
				}
				if len(device.requests) != 1 || !reflect.DeepEqual(device.requests[0], []devices.EntityID{entity}) {
					t.Fatal("read was not exactly the static Entity")
				}
			}
		})
	}
}

//nolint:modernize // Shared matrix builder keeps the table's entry columns compact.
func ptrEntry(entry devices.EntityStateSnapshotEntry) *devices.EntityStateSnapshotEntry {
	return &entry
}

func TestBranchExpressionGroupsAreEagerAndChooseIsFirstMatch(t *testing.T) {
	t.Parallel()
	leaf := func(id ConditionID, source string) Condition {
		return Condition{ID: id, Body: ExpressionCondition{Value: flowExpression(source)}}
	}
	definition := flowDefinition(
		t,
		nil,
		[]Step{
			{
				ID: "if",
				Body: IfStep{
					Conditions: Condition{
						ID: "all",
						Body: AllCondition{
							Children: []Condition{leaf("false", "false"), leaf("missing", "vars.missing")},
						},
					},
					Then: []Step{{ID: "wait", Body: DelayStep{DurationMS: 1}}},
				},
			},
		},
	)
	values, _ := newRunValues(flowRun(definition))
	step := definition.Steps[0]
	roots, _ := branchRoots(step)
	decision, err := evaluateBranch(step, roots, nil, nil, devices.EntityStateSnapshot{}, time.Now().UTC(), values)
	if err == nil || decision.Outcome() != BranchError || *decision.FailureCode() != "expression_missing" {
		t.Fatalf("group short-circuited: %#v %v", decision, err)
	}
	definition = flowDefinition(t, nil, []Step{{ID: "choose", Body: ChooseStep{Branches: []ChooseBranch{
		{
			ID:         "first",
			Conditions: leaf("lazy", `false && vars.missing`),
			Steps:      []Step{{ID: "a", Body: DelayStep{DurationMS: 1}}},
		},
		{ID: "second", Conditions: leaf("true", "true"), Steps: []Step{{ID: "b", Body: DelayStep{DurationMS: 1}}}},
		{
			ID:         "third",
			Conditions: leaf("unreached", "vars.missing"),
			Steps:      []Step{{ID: "c", Body: DelayStep{DurationMS: 1}}},
		},
	}}}})
	step = definition.Steps[0]
	roots, _ = branchRoots(step)
	decision, err = evaluateBranch(step, roots, nil, nil, devices.EntityStateSnapshot{}, time.Now().UTC(), values)
	if err != nil || decision.Outcome() != BranchChosen ||
		len(decision.Body.(ChooseDecision).Result.(ChooseSelected).Evaluations) != 2 {
		t.Fatalf("Choose = %#v %v", decision, err)
	}
	if err = ValidateBranchDecisionWithPreparedSnapshot(decision, definition, nil); err != nil {
		t.Fatal(err)
	}
	raw, err := EncodeBranchDecision(decision)
	if err != nil || !strings.Contains(string(raw), `"kind":"expression"`) {
		t.Fatalf("expression evidence missing: %s %v", raw, err)
	}
	if _, err = exprvalue.Compile(`true`); err != nil {
		t.Fatal(err)
	}
}

type flowReferenceDevices struct {
	flowDevices

	staticCalls  int
	literalCalls int
	stateCalls   int
	referenceErr error
}

func (device *flowReferenceDevices) ValidateCommandReference(
	context.Context,
	devices.EntityID,
	devices.OperationName,
) error {
	device.staticCalls++
	return device.referenceErr
}

func (device *flowReferenceDevices) ValidateCommand(
	_ context.Context,
	input devices.CommandInput,
) (devices.CommandParameters, error) {
	device.literalCalls++
	return input.Parameters, nil
}
func (device *flowReferenceDevices) ValidateConditionEntity(context.Context, devices.EntityID) error {
	device.stateCalls++
	return nil
}

func TestSaveValidatesEveryArmWithoutInventingDynamicPayloads(t *testing.T) {
	t.Parallel()
	entity := devices.EntityID("ent_01980000-0000-7000-8000-000000000001")
	definition := flowDefinition(
		t,
		nil,
		[]Step{{ID: "route", Body: IfStep{
			Conditions: Condition{ID: "select", Body: ExpressionCondition{Value: flowLiteral(`true`)}},
			Then: []Step{
				{
					ID: "literal",
					Body: CommandStep{
						EntityID:      entity,
						OperationName: "set",
						Parameters:    flowLiteral(`{"value":true}`),
					},
				},
			},
			Else: []Step{
				{
					ID: "dynamic",
					Body: CommandStep{
						EntityID:      entity,
						OperationName: "set",
						Parameters:    flowExpression(`{value: vars.target}`),
					},
				},
				{ID: "state", Body: ReadStateStep{EntityID: entity}},
			},
		}}},
	)
	device := &flowReferenceDevices{}
	if _, err := validateDefinition(t.Context(), device, nil, nil, definition); err != nil {
		t.Fatal(err)
	}
	if device.staticCalls != 1 || device.literalCalls != 1 || device.stateCalls != 1 || len(device.requests) != 0 {
		t.Fatalf("validation split: %+v", device)
	}
	device.referenceErr = devices.ErrEntityNotFound
	if _, err := validateDefinition(t.Context(), device, nil, nil, definition); !errors.Is(err, ErrInvalidAutomation) {
		t.Fatal("bad dynamic target in unselected arm accepted")
	}
}

func TestValueBudgetAndLiteralVariableNormalization(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(
		t,
		map[VariableName]ValueBinding{"x": flowLiteral(`1e3`)},
		[]Step{
			{ID: "assign", Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"x": flowLiteral(`1.0`)}}},
		},
	)
	run := flowRun(definition)
	values, _ := newRunValues(run)
	repo := &flowRepository{}
	service := NewService(repo, &flowDevices{}, Dependencies{})
	if !service.initializeValues(t.Context(), run, values) {
		t.Fatal("initialization failed")
	}
	if err := ValidateRunDataWithPreparedSnapshot(repo.data[0], definition); err != nil {
		t.Fatalf("normalized integer literal rejected: %v", err)
	}
	values.sequence = RunDataMaxRecords - 1
	if service.executeAssignment(t.Context(), run, values, "assign", definition.Steps[0].Body.(SetVariablesStep)) {
		t.Fatal("assignment exceeded reserved budget")
	}
	if repo.failure == nil || repo.failure.Body.(RunDataFailed).FailureCode != "run_data_limit" ||
		values.env.Vars["x"] != int64(1000) {
		t.Fatal("capacity failure did not preserve atomic context")
	}
}

func TestSelectedBranchVariablesPersistAndUnreachedOutputsStayMissing(t *testing.T) {
	t.Parallel()
	definition := flowDefinition(t, nil, []Step{
		{
			ID: "route",
			Body: IfStep{
				Conditions: Condition{ID: "true", Body: ExpressionCondition{Value: flowLiteral(`true`)}},
				Then: []Step{
					{
						ID:   "assign",
						Body: SetVariablesStep{Assignments: map[VariableName]ValueBinding{"target": flowLiteral(`40`)}},
					},
				},
				Else: []Step{
					{ID: "unreached", Body: ReadStateStep{EntityID: "ent_01980000-0000-7000-8000-000000000001"}},
				},
			},
		},
		{
			ID: "missing",
			Body: SetVariablesStep{
				Assignments: map[VariableName]ValueBinding{"bad": flowExpression(`steps.unreached.output`)},
			},
		},
	})
	run := flowRun(definition)
	values, _ := newRunValues(run)
	repo := &flowRepository{}
	device := &flowDevices{}
	service := NewService(repo, device, Dependencies{})
	if !service.initializeValues(t.Context(), run, values) {
		t.Fatal("initialization failed")
	}
	if service.executeSequence(t.Context(), run, definition.Steps, &executionCursor{values: values}) {
		t.Fatal("required missing output was silently accepted")
	}
	if values.env.Vars["target"] != int64(40) || len(values.env.Steps) != 0 || len(device.requests) != 0 {
		t.Fatal("selection did not preserve assignment or evaluated an unselected State Step")
	}
	if repo.failure == nil || repo.failure.Body.(RunDataFailed).FailureCode != "expression_missing" {
		t.Fatal("missing output failure evidence absent")
	}
}

func TestStateRecaptureDoesNotRefreshPriorOutputAndFailedCaptureDoesNotAdvance(t *testing.T) {
	t.Parallel()
	entity := devices.EntityID("ent_01980000-0000-7000-8000-000000000001")
	definition := flowDefinition(
		t,
		nil,
		[]Step{
			{ID: "first", Body: ReadStateStep{EntityID: entity}},
			{ID: "second", Body: ReadStateStep{EntityID: entity}},
			{ID: "third", Body: ReadStateStep{EntityID: entity}},
		},
	)
	run := flowRun(definition)
	values, _ := newRunValues(run)
	device := &flowDevices{
		snapshot: devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{
			entity: {
				EntityID: entity,
				Exists:   true,
				State: &devices.State{
					EntityID:      entity,
					ObservationID: "obs_01980000-0000-7000-8000-000000000001",
					ObservedAt:    run.StartedAt,
					Value:         devices.Value(`1`),
				},
			},
		}},
	}
	repo := &flowRepository{}
	service := NewService(repo, device, Dependencies{})
	if !service.initializeValues(t.Context(), run, values) {
		t.Fatal("initialization failed")
	}
	if !service.executeReadState(t.Context(), run, values, "first", definition.Steps[0].Body.(ReadStateStep)) {
		t.Fatal("first capture failed")
	}
	device.snapshot.Entries[entity].State.Value[0] = '2'
	if !service.executeReadState(t.Context(), run, values, "second", definition.Steps[1].Body.(ReadStateStep)) {
		t.Fatal("second capture failed")
	}
	first := values.env.Steps["first"].(map[string]any)["output"]
	second := values.env.Steps["second"].(map[string]any)["output"]
	if first != int64(1) || second != int64(2) || len(device.requests) != 2 {
		t.Fatal("State output refreshed or coherent read was reused")
	}
	repo.writeErr = errors.New("capture commit failed")
	if service.executeReadState(t.Context(), run, values, "third", definition.Steps[2].Body.(ReadStateStep)) {
		t.Fatal("failed capture commit advanced")
	}
	if _, exists := values.env.Steps["third"]; exists || repo.interrupts != 1 {
		t.Fatal("failed capture exposed output or omitted interruption")
	}
}
