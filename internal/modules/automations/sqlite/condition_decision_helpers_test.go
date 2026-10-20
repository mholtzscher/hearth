package sqlite_test

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// admissionNow is the fixed admission decision time shared by these fixtures.
//
//nolint:gochecknoglobals // One immutable fixture instant.
var admissionNow = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)

// conditionLeaf builds one entity_state leaf comparing a selected value with a
// static operand.
func conditionLeaf(
	id string,
	entityID devices.EntityID,
	operator automations.ComparisonOperator,
	operand string,
) *automations.Condition {
	return &automations.Condition{ID: automations.ConditionID(id), Body: automations.EntityStateCondition{
		EntityID: entityID,
		Pointer:  "/level",
		Operator: operator,
		Operand:  json.RawMessage(operand),
	}}
}

// stateSnapshotWith assembles one coherent snapshot from explicit entries.
func stateSnapshotWith(entries ...devices.EntityStateSnapshotEntry) devices.EntityStateSnapshot {
	snapshot := devices.EntityStateSnapshot{Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{}}
	for _, entry := range entries {
		snapshot.Entries[entry.EntityID] = entry
	}
	return snapshot
}

// presentStateEntry covers one Entity with accepted State carrying the supplied
// JSON value.
func presentStateEntry(
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

// newEntityID mints one canonical Entity identity for a fixture.
func newEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	id, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
