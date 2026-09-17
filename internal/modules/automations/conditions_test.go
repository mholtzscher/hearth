package automations_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"pgregory.net/rapid"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// conditionEntity returns one canonical Entity identity for a Condition fixture.
// Decimal digits are also lowercase hex, so every index stays canonical.
func conditionEntity(index int) devices.EntityID {
	return devices.EntityID(fmt.Sprintf("ent_01890f47-7a6b-7c4d-8e9f-%012d", index))
}

func conditionTime() time.Time {
	return time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
}

func conditionAge(seconds int64) *int64 {
	age := seconds
	return &age
}

func conditionLeaf(
	id string,
	entity devices.EntityID,
	pointer string,
	operator automations.ComparisonOperator,
	operand string,
	maxAge *int64,
) automations.Condition {
	return automations.Condition{
		ID:   automations.ConditionID(id),
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID:      entity,
			Pointer:       pointer,
			Operator:      operator,
			Operand:       json.RawMessage(operand),
			MaxAgeSeconds: maxAge,
		},
	}
}

func conditionGroup(
	kind automations.ConditionKind,
	children ...automations.Condition,
) automations.Condition {
	return automations.Condition{
		ID:       "root",
		Kind:     kind,
		Children: children,
	}
}

func conditionNot(id string, child automations.Condition) automations.Condition {
	return automations.Condition{
		ID:    automations.ConditionID(id),
		Kind:  automations.ConditionNot,
		Child: &child,
	}
}

func conditionState(id devices.EntityID, value string, observedAt time.Time) devices.EntityStateSnapshotEntry {
	return devices.EntityStateSnapshotEntry{
		EntityID: id,
		Exists:   true,
		State: &devices.State{
			EntityID:      id,
			Value:         devices.Value(value),
			ObservationID: conditionObservationID(id),
			ObservedAt:    observedAt,
		},
	}
}

// conditionObservationID derives a canonical Observation identity from one
// Condition fixture Entity, so retained evidence carries real Observation IDs
// instead of an empty placeholder the decision validator must reject.
func conditionObservationID(id devices.EntityID) devices.ObservationID {
	return devices.ObservationID("obs_" + strings.TrimPrefix(string(id), "ent_"))
}

func conditionMissingState(id devices.EntityID) devices.EntityStateSnapshotEntry {
	return devices.EntityStateSnapshotEntry{EntityID: id, Exists: true}
}

func conditionAbsent(id devices.EntityID) devices.EntityStateSnapshotEntry {
	return devices.EntityStateSnapshotEntry{EntityID: id}
}

func conditionSnapshot(entries ...devices.EntityStateSnapshotEntry) devices.EntityStateSnapshot {
	snapshot := devices.EntityStateSnapshot{
		Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{},
	}
	for _, entry := range entries {
		snapshot.Entries[entry.EntityID] = entry
	}
	return snapshot
}

func mustEvaluateCondition(
	t *testing.T,
	root automations.Condition,
	snapshot devices.EntityStateSnapshot,
	evaluatedAt time.Time,
) automations.ConditionEvaluation {
	t.Helper()
	evaluation, err := automations.EvaluateConditions(root, snapshot, evaluatedAt)
	if err != nil {
		t.Fatalf("EvaluateAutomationConditions: %v", err)
	}
	return evaluation
}

// truthGroupResult builds one group of boolean leaves whose State values produce
// the requested three-valued leaf results, then returns the root result.
func truthGroupResult(
	t *testing.T,
	kind automations.ConditionKind,
	results ...automations.ConditionResult,
) automations.ConditionResult {
	t.Helper()
	children := make([]automations.Condition, 0, len(results))
	entries := make([]devices.EntityStateSnapshotEntry, 0, len(results))
	for index, want := range results {
		entity := conditionEntity(index + 1)
		children = append(children, conditionLeaf(
			fmt.Sprintf("leaf-%d", index), entity, "", automations.ComparisonEqual, "true", nil,
		))
		switch want {
		case automations.ConditionTrue:
			entries = append(entries, conditionState(entity, "true", conditionTime()))
		case automations.ConditionFalse:
			entries = append(entries, conditionState(entity, "false", conditionTime()))
		case automations.ConditionUnknown:
			entries = append(entries, conditionMissingState(entity))
		default:
			t.Fatalf("unsupported leaf result %q", want)
		}
	}
	root := conditionGroup(kind, children...)
	return mustEvaluateCondition(t, root, conditionSnapshot(entries...), conditionTime()).Result
}

// The nine ordered pairs for binary all and any, plus all-unknown groups, match
// the settled truth table. Expectations are written from the contract, not
// derived from production helpers.
func TestAutomationConditionTruthTable(t *testing.T) {
	t.Parallel()
	results := []automations.ConditionResult{
		automations.ConditionTrue,
		automations.ConditionFalse,
		automations.ConditionUnknown,
	}
	table := map[string]struct {
		all automations.ConditionResult
		any automations.ConditionResult
	}{
		"true,true":       {automations.ConditionTrue, automations.ConditionTrue},
		"true,false":      {automations.ConditionFalse, automations.ConditionTrue},
		"true,unknown":    {automations.ConditionUnknown, automations.ConditionTrue},
		"false,true":      {automations.ConditionFalse, automations.ConditionTrue},
		"false,false":     {automations.ConditionFalse, automations.ConditionFalse},
		"false,unknown":   {automations.ConditionFalse, automations.ConditionUnknown},
		"unknown,true":    {automations.ConditionUnknown, automations.ConditionTrue},
		"unknown,false":   {automations.ConditionFalse, automations.ConditionUnknown},
		"unknown,unknown": {automations.ConditionUnknown, automations.ConditionUnknown},
	}
	for _, left := range results {
		for _, right := range results {
			key := fmt.Sprintf("%s,%s", left, right)
			want := table[key]
			if got := truthGroupResult(t, automations.ConditionAll, left, right); got != want.all {
				t.Errorf("all(%s) = %s, want %s", key, got, want.all)
			}
			if got := truthGroupResult(t, automations.ConditionAny, left, right); got != want.any {
				t.Errorf("any(%s) = %s, want %s", key, got, want.any)
			}
		}
	}
	// A true any admits despite an unknown sibling; an all with any false is false.
	if got := truthGroupResult(
		t, automations.ConditionAny,
		automations.ConditionTrue, automations.ConditionUnknown,
	); got != automations.ConditionTrue {
		t.Errorf("any(true, unknown) = %s, want true", got)
	}
	if got := truthGroupResult(
		t, automations.ConditionAll,
		automations.ConditionUnknown, automations.ConditionFalse,
		automations.ConditionTrue,
	); got != automations.ConditionFalse {
		t.Errorf("all(unknown, false, true) = %s, want false", got)
	}
	if got := truthGroupResult(
		t, automations.ConditionAll,
		automations.ConditionUnknown, automations.ConditionUnknown,
	); got != automations.ConditionUnknown {
		t.Errorf("all(unknown, unknown) = %s, want unknown", got)
	}
}

// not inverts true and false while preserving unknown; a not(unknown) does not
// admit.
func TestAutomationConditionNotTruthTable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		input automations.ConditionResult
		want  automations.ConditionResult
	}{
		{automations.ConditionTrue, automations.ConditionFalse},
		{automations.ConditionFalse, automations.ConditionTrue},
		{automations.ConditionUnknown, automations.ConditionUnknown},
	}
	for _, testCase := range cases {
		got := truthNotResult(t, testCase.input)
		if got != testCase.want {
			t.Errorf("not(%s) = %s, want %s", testCase.input, got, testCase.want)
		}
		if testCase.input == automations.ConditionUnknown && got == automations.ConditionTrue {
			t.Error("not(unknown) must not admit")
		}
	}
}

// truthNotResult evaluates one not node around a leaf that produces input.
func truthNotResult(
	t *testing.T,
	input automations.ConditionResult,
) automations.ConditionResult {
	t.Helper()
	entity := conditionEntity(1)
	leaf := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)
	entry := conditionMissingState(entity)
	switch input {
	case automations.ConditionTrue:
		entry = conditionState(entity, "true", conditionTime())
	case automations.ConditionFalse:
		entry = conditionState(entity, "false", conditionTime())
	case automations.ConditionUnknown:
	default:
		t.Fatalf("unsupported leaf result %q", input)
	}
	root := conditionNot("root", leaf)
	return mustEvaluateCondition(t, root, conditionSnapshot(entry), conditionTime()).Result
}

// Evaluation records every node in definition pre-order without short-circuiting,
// so an admitting any still retains its unknown sibling's explanatory leaf.
// Only entity_state leaves are recorded; a group result is derivable from its
// children and is not duplicated as evidence.
func TestAutomationConditionEvaluationIsFullPreOrder(t *testing.T) {
	t.Parallel()
	admitting := conditionEntity(1)
	unknownEntity := conditionEntity(2)
	tree := conditionGroup(
		automations.ConditionAny,
		conditionLeaf("first", admitting, "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("second", unknownEntity, "", automations.ComparisonEqual, "true", nil),
	)
	snapshot := conditionSnapshot(
		conditionState(admitting, "true", conditionTime()),
		conditionMissingState(unknownEntity),
	)
	evaluation := mustEvaluateCondition(t, tree, snapshot, conditionTime())
	if evaluation.Result != automations.ConditionTrue {
		t.Fatalf("root result = %s, want true", evaluation.Result)
	}
	if len(evaluation.Nodes) != 2 {
		t.Fatalf("leaf count = %d, want every leaf evaluated", len(evaluation.Nodes))
	}
	wantIDs := []automations.ConditionID{"first", "second"}
	for index, id := range wantIDs {
		if evaluation.Nodes[index].ID != id {
			t.Fatalf("leaf %d = %q, want %q", index, evaluation.Nodes[index].ID, id)
		}
	}
	second := evaluation.Nodes[1]
	if second.UnknownReason == nil || *second.UnknownReason != automations.ConditionUnknownStateMissing {
		t.Fatalf("unknown sibling reason = %v, want state_missing", second.UnknownReason)
	}
}

// Leaf unknown reasons follow the settled precedence, and missing Entity or State
// carries no fabricated Observation evidence.
func TestAutomationConditionUnknownReasons(t *testing.T) {
	t.Parallel()
	now := conditionTime()
	entity := conditionEntity(1)
	cases := []struct {
		name      string
		condition automations.Condition
		entry     devices.EntityStateSnapshotEntry
		want      automations.ConditionUnknownReason
		metadata  bool
	}{
		{
			"entity missing",
			conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil),
			conditionAbsent(entity),
			automations.ConditionUnknownEntityMissing,
			false,
		},
		{
			"state missing",
			conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil),
			conditionMissingState(entity),
			automations.ConditionUnknownStateMissing,
			false,
		},
		{
			"future evidence",
			conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", conditionAge(300)),
			conditionState(entity, "true", now.Add(time.Nanosecond)),
			automations.ConditionUnknownEvidenceInFuture,
			true,
		},
		{
			"expired evidence",
			conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", conditionAge(300)),
			conditionState(entity, "true", now.Add(-time.Hour)),
			automations.ConditionUnknownEvidenceExpired,
			true,
		},
		{
			"pointer missing",
			conditionLeaf("leaf", entity, "/missing", automations.ComparisonEqual, "true", nil),
			conditionState(entity, `{"present":true}`, now),
			automations.ConditionUnknownPointerMissing,
			true,
		},
		{
			"type mismatch eq",
			conditionLeaf("leaf", entity, "", automations.ComparisonEqual, `"true"`, nil),
			conditionState(entity, "true", now),
			automations.ConditionUnknownTypeMismatch,
			true,
		},
		{
			"type mismatch ne is unknown not true",
			conditionLeaf("leaf", entity, "", automations.ComparisonNotEqual, `"true"`, nil),
			conditionState(entity, "true", now),
			automations.ConditionUnknownTypeMismatch,
			true,
		},
		{
			"non-numeric ordering",
			conditionLeaf("leaf", entity, "", automations.ComparisonLessThan, "1", nil),
			conditionState(entity, `"not-a-number"`, now),
			automations.ConditionUnknownTypeMismatch,
			true,
		},
	}
	for _, testCase := range cases {
		evaluation := mustEvaluateCondition(
			t, testCase.condition, conditionSnapshot(testCase.entry), now,
		)
		if evaluation.Result != automations.ConditionUnknown {
			t.Errorf("%s: result = %s, want unknown", testCase.name, evaluation.Result)
		}
		node := evaluation.Nodes[len(evaluation.Nodes)-1]
		if node.UnknownReason == nil || *node.UnknownReason != testCase.want {
			t.Errorf("%s: reason = %v, want %q", testCase.name, node.UnknownReason, testCase.want)
		}
		if (node.ObservationID != nil) != testCase.metadata {
			t.Errorf("%s: metadata presence = %v, want %v", testCase.name, node.ObservationID != nil, testCase.metadata)
		}
	}
}

// A selected JSON null is real evidence, distinct from a missing selection, and
// same-kind unequal containers are false rather than unknown.
func TestAutomationConditionSelectedNullAndContainers(t *testing.T) {
	t.Parallel()
	now := conditionTime()
	entity := conditionEntity(1)
	nullLeaf := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "null", nil)
	evaluation := mustEvaluateCondition(t, nullLeaf, conditionSnapshot(conditionState(entity, "null", now)), now)
	node := evaluation.Nodes[0]
	if evaluation.Result != automations.ConditionTrue {
		t.Fatalf("selected null eq null = %s, want true", evaluation.Result)
	}
	if node.SelectedValue == nil || string(node.SelectedValue) != "null" {
		t.Fatalf("selected value = %q, want selected JSON null", node.SelectedValue)
	}
	arrayLeaf := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "[2,1]", nil)
	arrayEvaluation := mustEvaluateCondition(
		t, arrayLeaf, conditionSnapshot(conditionState(entity, "[1,2]", now)), now,
	)
	if arrayEvaluation.Result != automations.ConditionFalse {
		t.Fatalf("unequal arrays = %s, want false", arrayEvaluation.Result)
	}
	objectLeaf := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, `{"a":1}`, nil)
	objectEvaluation := mustEvaluateCondition(
		t, objectLeaf, conditionSnapshot(conditionState(entity, `{"a":1,"b":2}`, now)), now,
	)
	if objectEvaluation.Result != automations.ConditionFalse {
		t.Fatalf("differing objects = %s, want false", objectEvaluation.Result)
	}
}

// Ordering and equality use exact rationals, and ne 30 against 40 is true.
func TestAutomationConditionNumericComparisons(t *testing.T) {
	t.Parallel()
	now := conditionTime()
	entity := conditionEntity(1)
	cases := []struct {
		name     string
		operator automations.ComparisonOperator
		operand  string
		value    string
		want     automations.ConditionResult
	}{
		{"ne 30 against 40", automations.ComparisonNotEqual, "30", "40", automations.ConditionTrue},
		{
			"exact decimal ordering",
			automations.ComparisonGreaterThan,
			"0.3",
			"0.300000000000000000001",
			automations.ConditionTrue,
		},
		{
			"large integers stay exact",
			automations.ComparisonEqual,
			"9007199254740993",
			"9007199254740993",
			automations.ConditionTrue,
		},
		{
			"large integers differ",
			automations.ComparisonEqual,
			"9007199254740993",
			"9007199254740992",
			automations.ConditionFalse,
		},
	}
	for _, testCase := range cases {
		leaf := conditionLeaf("leaf", entity, "", testCase.operator, testCase.operand, nil)
		evaluation := mustEvaluateCondition(
			t, leaf, conditionSnapshot(conditionState(entity, testCase.value, now)), now,
		)
		if evaluation.Result != testCase.want {
			t.Errorf("%s = %s, want %s", testCase.name, evaluation.Result, testCase.want)
		}
	}
}

// Unusable stored JSON is an error, not an unknown leaf, and it carries the
// devices corruption class so admission can log the fixed diagnostic.
func TestAutomationConditionStoredStateCorruptionIsAnError(t *testing.T) {
	t.Parallel()
	entity := conditionEntity(1)
	leaf := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)
	_, err := automations.EvaluateConditions(
		leaf, conditionSnapshot(conditionState(entity, "{not json", conditionTime())), conditionTime(),
	)
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("corrupt stored State error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
}

// Age bounds allow equality, reject one nanosecond past, mark future evidence
// unknown only with a bound, and always retain the selected value when the
// pointer resolves.
func TestAutomationConditionEvidenceAge(t *testing.T) {
	t.Parallel()
	now := conditionTime()
	entity := conditionEntity(1)
	bounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", conditionAge(300))
	unbounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)

	exact := mustEvaluateCondition(
		t, bounded, conditionSnapshot(conditionState(entity, "true", now.Add(-300*time.Second))), now,
	)
	if exact.Result != automations.ConditionTrue {
		t.Fatalf("age equal to the bound = %s, want true", exact.Result)
	}
	past := mustEvaluateCondition(
		t, bounded, conditionSnapshot(conditionState(entity, "true", now.Add(-300*time.Second-time.Nanosecond))), now,
	)
	if past.Result != automations.ConditionUnknown {
		t.Fatalf("age one nanosecond past the bound = %s, want unknown", past.Result)
	}
	if node := past.Nodes[0]; node.SelectedValue == nil || string(node.SelectedValue) != "true" {
		t.Fatalf("expired evidence selected value = %q, want retained true", node.SelectedValue)
	}
	if node := past.Nodes[0]; node.ObservationID == nil || node.ObservedAt == nil {
		t.Fatal("expired evidence must retain Observation identity and time")
	}
	unboundedAncient := mustEvaluateCondition(
		t, unbounded, conditionSnapshot(conditionState(entity, "true", now.Add(-100*time.Hour))), now,
	)
	if unboundedAncient.Result != automations.ConditionTrue {
		t.Fatalf("unbounded ancient evidence = %s, want compared normally", unboundedAncient.Result)
	}
	unboundedFuture := mustEvaluateCondition(
		t, unbounded, conditionSnapshot(conditionState(entity, "true", now.Add(time.Hour))), now,
	)
	if unboundedFuture.Result != automations.ConditionTrue {
		t.Fatalf("unbounded future evidence = %s, want compare normally", unboundedFuture.Result)
	}
	// An unchanged Observation that refreshes observed_at moves back inside the
	// bound without changing the value.
	refreshed := mustEvaluateCondition(
		t, bounded, conditionSnapshot(conditionState(entity, "true", now.Add(-time.Minute))), now,
	)
	if refreshed.Result != automations.ConditionTrue {
		t.Fatalf("refreshed unchanged Observation = %s, want true", refreshed.Result)
	}
}

// Adapter acquisition time and upstream change time never alter a result; only
// the broker-assigned observed_at drives the evidence age bound.
func TestAutomationConditionEvidenceAgeIgnoresAdapterAndUpstreamTimes(t *testing.T) {
	t.Parallel()
	entity := conditionEntity(1)
	now := conditionTime()
	upstream := now.Add(2 * time.Hour)
	state := &devices.State{
		EntityID:          entity,
		Value:             devices.Value("true"),
		ObservedAt:        now.Add(-time.Hour),
		AdapterReceivedAt: now.Add(time.Hour),
		SourceUpdatedAt:   &upstream,
	}
	entry := devices.EntityStateSnapshotEntry{EntityID: entity, Exists: true, State: state}
	bounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", conditionAge(300))
	expired := mustEvaluateCondition(t, bounded, conditionSnapshot(entry), now)
	if expired.Result != automations.ConditionUnknown ||
		*expired.Nodes[0].UnknownReason != automations.ConditionUnknownEvidenceExpired {
		t.Fatalf("bounded result = %#v, want evidence_expired from observed_at alone", expired.Nodes[0])
	}
	unbounded := conditionLeaf("leaf", entity, "", automations.ComparisonEqual, "true", nil)
	matched := mustEvaluateCondition(t, unbounded, conditionSnapshot(entry), now)
	if matched.Result != automations.ConditionTrue {
		t.Fatalf("unbounded result = %s, want true", matched.Result)
	}
}

// A missing map key returns the typed coverage error carrying the whole tree's
// required Entity set, and it never produces a partial evaluation.
func TestEvaluateAutomationConditionsRequiresCompleteCoverage(t *testing.T) {
	t.Parallel()
	covered := conditionEntity(1)
	uncoveredLow := conditionEntity(2)
	uncoveredHigh := conditionEntity(3)
	tree := conditionGroup(
		automations.ConditionAll,
		conditionLeaf("covered", covered, "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("uncovered-low", uncoveredLow, "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("uncovered-high", uncoveredHigh, "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("uncovered-low-again", uncoveredLow, "", automations.ComparisonEqual, "true", nil),
	)
	snapshot := conditionSnapshot(conditionState(covered, "true", conditionTime()))
	evaluation, err := automations.EvaluateConditions(tree, snapshot, conditionTime())
	if !errors.Is(err, automations.ErrConditionSnapshotRequired) {
		t.Fatalf("coverage error = %v, want ErrConditionSnapshotRequired", err)
	}
	var required *automations.ConditionSnapshotRequiredError
	if !errors.As(err, &required) {
		t.Fatalf("coverage error type = %T, want *ConditionSnapshotRequiredError", err)
	}
	want := []devices.EntityID{covered, uncoveredLow, uncoveredHigh}
	if !slices.Equal(required.RequiredEntityIDs, want) {
		t.Fatalf("required IDs = %v, want complete sorted set %v", required.RequiredEntityIDs, want)
	}
	if evaluation.Nodes != nil || evaluation.Result != "" {
		t.Fatalf("coverage failure must not evaluate: %#v", evaluation)
	}
	if err.Error() != "automation condition snapshot coverage is incomplete" {
		t.Fatalf("coverage error text = %q", err.Error())
	}
}

func TestRequiredConditionEntityIDsAreSortedAndDeduplicated(t *testing.T) {
	t.Parallel()
	tree := conditionGroup(
		automations.ConditionAll,
		conditionLeaf("b", conditionEntity(3), "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("a", conditionEntity(1), "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("c", conditionEntity(1), "", automations.ComparisonEqual, "true", nil),
	)
	ids, err := automations.RequiredConditionEntityIDs(tree)
	if err != nil {
		t.Fatal(err)
	}
	want := []devices.EntityID{conditionEntity(1), conditionEntity(3)}
	if !slices.Equal(ids, want) {
		t.Fatalf("required IDs = %v, want %v", ids, want)
	}
	malformed := conditionGroup(automations.ConditionAll)
	if _, err = automations.RequiredConditionEntityIDs(malformed); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("malformed tree error = %v, want ErrInvalidAutomation", err)
	}
}

// Typed trees reject depth and node overflows at the exact boundaries.
func TestAutomationConditionTreeBounds(t *testing.T) {
	t.Parallel()
	if _, err := automations.NormalizeConditions(notChain(7)); err != nil {
		t.Fatalf("depth 8 tree: %v", err)
	}
	if _, err := automations.NormalizeConditions(
		notChain(8),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("depth 9 tree error = %v, want ErrInvalidAutomation", err)
	}
	if _, err := automations.NormalizeConditions(wideGroup(63)); err != nil {
		t.Fatalf("64 node tree: %v", err)
	}
	if _, err := automations.NormalizeConditions(
		wideGroup(64),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("65 node tree error = %v, want ErrInvalidAutomation", err)
	}
}

// notChain returns a not chain with count not nodes plus one leaf.
func notChain(count int) automations.Condition {
	node := conditionLeaf(
		fmt.Sprintf("leaf-%d", count),
		conditionEntity(1),
		"",
		automations.ComparisonEqual,
		"true",
		nil,
	)
	for level := count - 1; level >= 0; level-- {
		node = conditionNot(fmt.Sprintf("not-%d", level), node)
	}
	return node
}

// wideGroup returns one all group with count leaf children.
func wideGroup(count int) automations.Condition {
	children := make([]automations.Condition, 0, count)
	for index := range count {
		children = append(children, conditionLeaf(
			fmt.Sprintf("leaf-%d", index), conditionEntity(1), "", automations.ComparisonEqual, "true", nil,
		))
	}
	return automations.Condition{
		ID:       "root",
		Kind:     automations.ConditionAll,
		Children: children,
	}
}

// Typed validation rejects cycles and shared payloads before recursing without
// bound, plus duplicate IDs, contradictory payloads, invalid pointers, operands,
// and age bounds.
func TestAutomationConditionTreeRejectsInvalidTypedTrees(t *testing.T) {
	t.Parallel()
	leaf := conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", nil)

	selfCycle := automations.Condition{ID: "self", Kind: automations.ConditionNot}
	selfCycle.Child = &selfCycle

	children := make([]automations.Condition, 1)
	sliceCycle := automations.Condition{
		ID:       "root",
		Kind:     automations.ConditionAll,
		Children: children,
	}
	children[0] = sliceCycle

	shared := []automations.Condition{leaf}
	aliased := automations.Condition{
		ID: "root", Kind: automations.ConditionAll,
		Children: []automations.Condition{
			{ID: "left", Kind: automations.ConditionAny, Children: shared},
			{ID: "right", Kind: automations.ConditionAny, Children: shared},
		},
	}

	duplicate := conditionGroup(automations.ConditionAll,
		conditionLeaf("dup", conditionEntity(1), "", automations.ComparisonEqual, "true", nil),
		conditionLeaf("dup", conditionEntity(2), "", automations.ComparisonEqual, "true", nil),
	)
	emptyGroup := conditionGroup(automations.ConditionAny)
	notWithChildren := automations.Condition{
		ID: "root", Kind: automations.ConditionNot,
		Children: []automations.Condition{leaf}, Child: &leaf,
	}
	leafWithChildren := automations.Condition{
		ID: "root", Kind: automations.ConditionEntityState,
		Children: []automations.Condition{leaf},
		EntityState: &automations.EntityStateCondition{
			EntityID: conditionEntity(1), Operator: automations.ComparisonEqual, Operand: json.RawMessage("true"),
		},
	}
	cases := []struct {
		name string
		root automations.Condition
	}{
		{"child cycle", selfCycle},
		{"slice cycle", sliceCycle},
		{"shared children payload", aliased},
		{"duplicate IDs", duplicate},
		{"empty group", emptyGroup},
		{"not with children", notWithChildren},
		{"leaf with children", leafWithChildren},
		{
			"non-slug ID",
			automations.Condition{ID: "Root", Kind: automations.ConditionNot, Child: &leaf},
		},
		{
			"pointer too long",
			conditionLeaf(
				"leaf",
				conditionEntity(1),
				"/"+string(make([]byte, 256)),
				automations.ComparisonEqual,
				"true",
				nil,
			),
		},
		{
			"pointer without slash",
			conditionLeaf("leaf", conditionEntity(1), "a", automations.ComparisonEqual, "true", nil),
		},
		{
			"unknown operator",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonOperator("like"), "true", nil),
		},
		{
			"ordering operand not numeric",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonLessThan, `"1"`, nil),
		},
		{
			"age below minimum",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", conditionAge(0)),
		},
		{
			"age above maximum",
			conditionLeaf("leaf", conditionEntity(1), "", automations.ComparisonEqual, "true", conditionAge(2_592_001)),
		},
		{
			"nil operand",
			automations.Condition{
				ID: "leaf", Kind: automations.ConditionEntityState,
				EntityState: &automations.EntityStateCondition{
					EntityID: conditionEntity(1), Operator: automations.ComparisonEqual,
				},
			},
		},
	}
	for _, testCase := range cases {
		if _, err := automations.NormalizeConditions(
			testCase.root,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Errorf("%s: error = %v, want ErrInvalidAutomation", testCase.name, err)
		}
	}
	// Age boundaries are inclusive on both ends.
	for _, seconds := range []int64{1, 2_592_000} {
		bounded := conditionLeaf(
			"leaf",
			conditionEntity(1),
			"",
			automations.ComparisonEqual,
			"true",
			conditionAge(seconds),
		)
		if _, err := automations.NormalizeConditions(bounded); err != nil {
			t.Errorf("age %d: %v", seconds, err)
		}
	}
}

// Normalization returns owned copies, so neither caller mutation nor pointer or
// byte aliasing can change the normalized tree.
func TestNormalizeAutomationConditionsReturnsOwnedCopy(t *testing.T) {
	t.Parallel()
	operand := json.RawMessage(`{"a":1}`)
	input := automations.Condition{
		ID: "root", Kind: automations.ConditionAll,
		Children: []automations.Condition{
			{
				ID: "leaf", Kind: automations.ConditionEntityState,
				EntityState: &automations.EntityStateCondition{
					EntityID: conditionEntity(1), Pointer: "", Operator: automations.ComparisonEqual,
					Operand: operand, MaxAgeSeconds: conditionAge(60),
				},
			},
		},
	}
	normalized, err := automations.NormalizeConditions(input)
	if err != nil {
		t.Fatal(err)
	}
	operand[0] = '['
	input.Children = nil
	input.EntityState = &automations.EntityStateCondition{EntityID: conditionEntity(2)}
	if string(normalized.Children[0].EntityState.Operand) != `{"a":1}` {
		t.Fatalf("normalized operand changed with caller mutation: %s", normalized.Children[0].EntityState.Operand)
	}
	if normalized.Children[0].EntityState.EntityID != conditionEntity(1) {
		t.Fatalf("normalized entity changed: %s", normalized.Children[0].EntityState.EntityID)
	}
	normalized.Children[0].ID = "mutated"
	*normalized.Children[0].EntityState.MaxAgeSeconds = 10
	if normalized.Children[0].ID != "mutated" {
		t.Fatal("returned tree must be independent of the input")
	}
}

// Random groups preserve double negation and child permutation, and normalization
// is idempotent over generated valid trees.
func TestAutomationConditionRapidProperties(t *testing.T) {
	t.Parallel()
	rapid.Check(t, func(rapidT *rapid.T) {
		generated := drawConditionFixture(rapidT)
		evaluation := evaluateOrFail(rapidT, generated.tree, generated.snapshot)

		wrapped := conditionNot("wrap-outer", conditionNot("wrap-inner", generated.tree))
		wrappedEvaluation := evaluateOrFail(rapidT, wrapped, generated.snapshot)
		if wrappedEvaluation.Result != evaluation.Result {
			rapidT.Fatalf("double negation changed result: %s vs %s", wrappedEvaluation.Result, evaluation.Result)
		}
		if len(wrappedEvaluation.Nodes) != len(evaluation.Nodes) {
			rapidT.Fatalf(
				"double negation leaf count = %d, want %d",
				len(wrappedEvaluation.Nodes),
				len(evaluation.Nodes),
			)
		}
		permuted := conditionGroup(generated.kind, slices.Clone(generated.children)...)
		slices.Reverse(permuted.Children)
		permutedEvaluation := evaluateOrFail(rapidT, permuted, generated.snapshot)
		if permutedEvaluation.Result != evaluation.Result {
			rapidT.Fatalf("permutation changed result: %s vs %s", permutedEvaluation.Result, evaluation.Result)
		}
		normalized, err := automations.NormalizeConditions(generated.tree)
		if err != nil {
			rapidT.Fatal(err)
		}
		again, err := automations.NormalizeConditions(normalized)
		if err != nil {
			rapidT.Fatal(err)
		}
		if !reflect.DeepEqual(normalized, again) {
			rapidT.Fatalf("normalization is not idempotent: %#v vs %#v", normalized, again)
		}
	})
}

// evaluateOrFail evaluates one generated tree, failing the Rapid property on an
// unexpected error.
func evaluateOrFail(
	rapidT *rapid.T,
	root automations.Condition,
	snapshot devices.EntityStateSnapshot,
) automations.ConditionEvaluation {
	rapidT.Helper()
	evaluation, err := automations.EvaluateConditions(root, snapshot, conditionTime())
	if err != nil {
		rapidT.Fatal(err)
	}
	return evaluation
}

type conditionFixture struct {
	tree     automations.Condition
	kind     automations.ConditionKind
	children []automations.Condition
	snapshot devices.EntityStateSnapshot
}

// drawConditionFixture generates a small valid group tree with a matching State
// snapshot so generated results cover true, false, and unknown.
func drawConditionFixture(rapidT *rapid.T) conditionFixture {
	kind := rapid.SampledFrom([]automations.ConditionKind{
		automations.ConditionAll,
		automations.ConditionAny,
	}).Draw(rapidT, "kind")
	type comparison struct {
		operator automations.ComparisonOperator
		operand  string
	}
	comparisons := []comparison{
		{automations.ComparisonEqual, "true"},
		{automations.ComparisonEqual, `"x"`},
		{automations.ComparisonNotEqual, "0"},
		{automations.ComparisonNotEqual, `"x"`},
		{automations.ComparisonLessThan, "1"},
		{automations.ComparisonGreaterThan, "5"},
	}
	count := rapid.IntRange(1, 5).Draw(rapidT, "child count")
	children := make([]automations.Condition, 0, count)
	entries := make([]devices.EntityStateSnapshotEntry, 0, count)
	for index := range count {
		entity := conditionEntity(index + 1)
		selected := rapid.SampledFrom(comparisons).Draw(rapidT, "comparison")
		children = append(children, conditionLeaf(
			fmt.Sprintf("leaf-%d", index), entity, "", selected.operator, selected.operand, nil,
		))
		state := rapid.SampledFrom([]string{"true", "false", "0", "5", `"x"`, `[1,2]`}).Draw(rapidT, "value")
		switch rapid.IntRange(0, 3).Draw(rapidT, "coverage") {
		case 0:
			entries = append(entries, conditionMissingState(entity))
		case 1:
			entries = append(entries, conditionAbsent(entity))
		default:
			entries = append(entries, conditionState(entity, state, conditionTime()))
		}
	}
	return conditionFixture{
		tree:     conditionGroup(kind, children...),
		kind:     kind,
		children: children,
		snapshot: conditionSnapshot(entries...),
	}
}
