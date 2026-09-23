package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Real SQLite admission must persist projection-time predecessor evidence on
// both Run and Skip history rows. Replacing the definition afterward must not
// rewrite either retained outcome, and SQL NULL must remain distinct from JSON
// null. This fails if an insert omits the new column or history reads consult
// the current definition/state instead of the committed Fact summary.
func TestSQLiteAdmissionRetainsObservationPredecessorOnRunAndSkip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)

	for _, test := range []struct {
		name          string
		previousValue devices.Value
		condition     bool
		wantReason    automations.SkipReason
	}{
		{name: "Run retains object predecessor", previousValue: devices.Value(`{"temperature":20}`)},
		{name: "Skip retains JSON null predecessor", previousValue: devices.Value(`null`), condition: true,
			wantReason: automations.SkipConditionsFalse},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertStoredObservationTransition(
				ctx, t, repository, test.previousValue, test.condition, test.wantReason,
			)
		})
	}
}

func assertStoredObservationTransition(
	ctx context.Context,
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	previousValue devices.Value,
	condition bool,
	wantReason automations.SkipReason,
) {
	t.Helper()
	definition := transitionHistoryDefinition(t, previousValue, condition)
	record, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, definition.Triggers[0].Observation.EntityID, admissionNow)
	fact.Observation.PreviousValue = append(devices.Value(nil), previousValue...)
	snapshot := transitionHistorySnapshot(t, definition, condition)
	admitted, err := repository.AdmitDeviceFact(ctx, fact, snapshot, admissionNow)
	if err != nil {
		t.Fatal(err)
	}
	entryID := transitionHistoryEntryID(t, admitted, condition, wantReason)
	assertTransitionHistoryEvidence(ctx, t, repository, definition, record, entryID, previousValue)
}

func transitionHistoryDefinition(t *testing.T, previousValue devices.Value, condition bool) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	if previousValue[0] == '{' {
		definition.Triggers[0].Observation.PreviousComparisons = []automations.ObservationComparison{{
			Pointer: "/temperature", Operator: automations.ComparisonLessThanOrEqual,
			Operand: json.RawMessage("25"),
		}}
	}
	if condition {
		definition.Conditions = conditionLeaf(
			"not_dark", newEntityID(t), automations.ComparisonLessThan, "30",
		)
	}
	return definition
}

func transitionHistorySnapshot(
	t *testing.T,
	definition automations.Definition,
	condition bool,
) devices.EntityStateSnapshot {
	t.Helper()
	if !condition {
		return devices.EntityStateSnapshot{}
	}
	conditionEntity := definition.Conditions.EntityState.EntityID
	return stateSnapshotWith(presentStateEntry(t, conditionEntity, `{"level":90}`, admissionNow))
}

func transitionHistoryEntryID(
	t *testing.T,
	admitted automations.AdmissionResult,
	condition bool,
	wantReason automations.SkipReason,
) string {
	t.Helper()
	if condition {
		if admitted.Outcome.RecordedSkips != 1 || len(admitted.Skips) != 1 || admitted.Skips[0].Reason != wantReason {
			t.Fatalf("admission outcome = %#v, want Condition Skip %q", admitted.Outcome, wantReason)
		}
		return string(admitted.Skips[0].SkipID)
	}
	if admitted.Outcome.StartedRuns != 1 || len(admitted.StartedRuns) != 1 {
		t.Fatalf("admission outcome = %#v, want one Run", admitted.Outcome)
	}
	return string(admitted.StartedRuns[0].ID)
}

func assertTransitionHistoryEvidence(
	ctx context.Context,
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	definition automations.Definition,
	record automations.Record,
	entryID string,
	previousValue devices.Value,
) {
	t.Helper()
	// Changing the Trigger definition and revision must not rewrite retained evidence.
	replacement := definition
	replacement.Name = "Replacement definition"
	replacement.Triggers = append([]automations.Trigger(nil), definition.Triggers...)
	replacement.Triggers[0].Observation.PreviousComparisons = nil
	if _, err := repository.ReplaceAutomation(ctx, record.ID, record.Revision, replacement); err != nil {
		t.Fatal(err)
	}
	entry := historyEntry(t, repository, record.ID, entryID)
	summary := transitionSummary(t, entry)
	if string(summary.ObservationValue) != `{"temperature":25,"level":10}` {
		t.Errorf("retained incoming Observation = %s", summary.ObservationValue)
	}
	if string(summary.PreviousStateValue) != string(previousValue) {
		t.Errorf("retained predecessor = %s, want %s", summary.PreviousStateValue, previousValue)
	}
	stored := firstHistorySummary(t, repository, record.ID)
	if stored.Fact == nil || string(stored.Fact.PreviousStateValue) != string(previousValue) {
		t.Errorf("history-list predecessor = %#v, want %s", stored.Fact, previousValue)
	}
}

func transitionSummary(t *testing.T, entry automations.HistoryEntry) *automations.DeviceFactSummary {
	t.Helper()
	if entry.Run != nil {
		return entry.Run.Fact
	}
	if entry.Skip != nil {
		return entry.Skip.Fact
	}
	t.Fatalf("history entry = %#v, want Fact summary", entry)
	return nil
}
