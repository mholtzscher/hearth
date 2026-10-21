package automations_test

import (
	"context"
	"encoding/json"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// admissionConditionTree builds one entity_state Condition leaf comparing
// /level with a static operand, so a definition can be made conditional without
// a full tree fixture.
func admissionConditionTree(entityID devices.EntityID, operand string) *automations.Condition {
	return &automations.Condition{ID: "dark", Body: automations.EntityStateCondition{
		EntityID: entityID,
		Pointer:  "/level",
		Operator: automations.ComparisonLessThan,
		Operand:  json.RawMessage(operand),
	}}
}

// admissionDefinitionFor builds one enabled definition whose Observation
// Trigger matches the fixture Fact and whose Conditions are the supplied tree.
func admissionDefinitionFor(
	t *testing.T,
	triggerEntity devices.EntityID,
	conditions *automations.Condition,
) automations.Definition {
	t.Helper()
	definition := runtimeDefinitionFor(t, triggerEntity)
	definition.Conditions = conditions
	return definition
}

// admissionSnapshot assembles one coherent snapshot from explicit entries.
func admissionSnapshot(entries ...devices.EntityStateSnapshotEntry) devices.EntityStateSnapshot {
	snapshot := devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
	for _, entry := range entries {
		snapshot.Entries[entry.EntityID] = entry
	}
	return snapshot
}

// admissionState covers one Entity with accepted State carrying the supplied
// JSON value.
func admissionState(
	t *testing.T,
	entityID devices.EntityID,
	value string,
	observedAt time.Time,
) devices.EntityStateSnapshotEntry {
	t.Helper()
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return devices.EntityStateSnapshotEntry{
		EntityID: entityID,
		Exists:   true,
		State: &devices.State{
			EntityID:      entityID,
			Value:         devices.Value(value),
			ObservationID: observationID,
			ObservedAt:    observedAt,
		},
	}
}

// requireManualConditionBlock admits one manual request that must commit a
// Condition Skip and returns the committed Skip identity.
func requireManualConditionBlock(
	t *testing.T,
	service *automations.Service,
	id automations.AutomationID,
) automations.SkipID {
	t.Helper()
	_, err := service.StartManualRun(context.Background(), automations.ManualRunInput{AutomationID: id})
	var blocked *automations.ConditionsBlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("manual admission = %v, want a committed Condition block", err)
	}
	return blocked.SkipID
}

// observingAdmissionRepository records definition pre-reads while delegating
// every admission operation to the real SQLite repository.
type observingAdmissionRepository struct {
	automations.Repository

	listEnabledCalls atomic.Int32
}

func (repository *observingAdmissionRepository) ListEnabledAutomations(
	ctx context.Context,
) ([]automations.Record, error) {
	repository.listEnabledCalls.Add(1)
	return repository.Repository.ListEnabledAutomations(ctx)
}

// replacingAdmissionRepository changes a definition after the Service pre-read
// and before the transaction, deterministically exercising the coverage race.
type replacingAdmissionRepository struct {
	*observingAdmissionRepository

	replace  func()
	attempts atomic.Int32
}

func (repository *replacingAdmissionRepository) AdmitDeviceFact(
	ctx context.Context,
	fact automations.DeviceFact,
	snapshot devices.EntityStateSnapshot,
	now time.Time,
	startupAt time.Time,
) (automations.AdmissionResult, error) {
	if repository.attempts.Add(1) == 1 && repository.replace != nil {
		repository.replace()
	}
	return repository.Repository.AdmitDeviceFact(ctx, fact, snapshot, now, startupAt)
}

// DecideConditions maps a true, false, or unknown evaluation to the admission
// decision and Skip reason it implies, and rejects an uncovered snapshot before
// evaluating anything.
func TestDecideConditionsOutcome(t *testing.T) {
	t.Parallel()
	entity := newEntityID(t)
	tests := []struct {
		name         string
		snapshot     devices.EntityStateSnapshot
		wantReason   automations.SkipReason
		wantResult   automations.ConditionResult
		wantCoverage bool
	}{
		{
			name:       "true admits",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":10}`, runtimeTestNow)),
			wantResult: automations.ConditionTrue,
		},
		{
			name:       "false blocks",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":90}`, runtimeTestNow)),
			wantReason: automations.SkipConditionsFalse,
			wantResult: automations.ConditionFalse,
		},
		{
			name:       "unknown blocks",
			snapshot:   admissionSnapshot(admissionState(t, entity, `{"level":"high"}`, runtimeTestNow)),
			wantReason: automations.SkipConditionsUnknown,
			wantResult: automations.ConditionUnknown,
		},
		{name: "uncovered rejects", snapshot: admissionSnapshot(), wantCoverage: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			conditions := admissionConditionTree(entity, "30")
			decision, reason, err := automations.DecideConditions(conditions, test.snapshot, runtimeTestNow)
			if test.wantCoverage {
				requireConditionCoverage(t, err, entity)
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			requireEvaluatedDecision(t, decision, test.wantResult)
			if reason != test.wantReason {
				t.Fatalf("skip reason = %q, want %q", reason, test.wantReason)
			}
		})
	}
}

// requireConditionCoverage asserts one coverage rejection naming exactly the
// requested Entity.
func requireConditionCoverage(t *testing.T, err error, entity devices.EntityID) {
	t.Helper()
	coverage, found := errors.AsType[*automations.ConditionSnapshotRequiredError](err)
	if !found {
		t.Fatalf("error = %v, want ConditionSnapshotRequiredError", err)
	}
	if len(coverage.RequiredEntityIDs) != 1 || coverage.RequiredEntityIDs[0] != entity {
		t.Fatalf("required entities = %v, want %v", coverage.RequiredEntityIDs, entity)
	}
}

// requireEvaluatedDecision asserts one complete evaluated decision against the
// evaluated tree and result.
func requireEvaluatedDecision(
	t *testing.T,
	decision automations.ConditionDecision,
	wantResult automations.ConditionResult,
) {
	t.Helper()
	if decision.DecisionMode() != automations.ConditionDecisionEvaluated ||
		decision.DecisionEvaluation() == nil ||
		decision.DecisionEvaluation().Result != wantResult {
		t.Fatalf("decision = %#v", decision)
	}
}
