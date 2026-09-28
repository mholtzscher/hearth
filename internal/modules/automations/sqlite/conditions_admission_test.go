package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
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
	return &automations.Condition{
		ID:   automations.ConditionID(id),
		Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: entityID,
			Pointer:  "/level",
			Operator: operator,
			Operand:  json.RawMessage(operand),
		},
	}
}

// conditionalDefinitionFor builds one enabled definition whose Observation
// Trigger matches observationFactFor and whose Conditions are the supplied tree.
func conditionalDefinitionFor(
	t *testing.T,
	triggerEntity devices.EntityID,
	conditions *automations.Condition,
) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Triggers[0].Observation.EntityID = triggerEntity
	definition.Conditions = conditions
	return definition
}

// observationFactFor builds one accepted Observation Fact matching the fixture
// Trigger's comparison.
func observationFactFor(t *testing.T, entityID devices.EntityID, emittedAt time.Time) automations.DeviceFact {
	t.Helper()
	factID, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.DeviceFact{
		Family: automations.DeviceFactObservation,
		Observation: &automations.ObservationFact{
			FactID:        factID,
			ObservationID: observationID,
			EntityID:      entityID,
			Disposition:   devices.DispositionApplied,
			Value:         devices.Value(`{"temperature":25,"level":10}`),
			EmittedAt:     emittedAt,
		},
	}
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

func absentEntityEntry(entityID devices.EntityID) devices.EntityStateSnapshotEntry {
	return devices.EntityStateSnapshotEntry{EntityID: entityID}
}

// historyEntry reads one retained Run or Skip from the repository under test.
func historyEntry(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	automationID automations.AutomationID,
	entryID string,
) automations.HistoryEntry {
	t.Helper()
	entry, err := repository.GetHistoryEntry(context.Background(), automationID, entryID)
	if err != nil {
		t.Fatal(err)
	}
	return entry
}

// listHistory reads one Automation's retained history newest first.
func listHistory(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	automationID automations.AutomationID,
) []automations.HistorySummary {
	t.Helper()
	page, err := repository.ListHistory(context.Background(), automations.ListHistoryParams{
		AutomationID: automationID,
	})
	if err != nil {
		t.Fatal(err)
	}
	return page.Items
}

// firstHistorySummary reads the only expected summary of one Automation.
func firstHistorySummary(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	automationID automations.AutomationID,
) automations.HistorySummary {
	t.Helper()
	summaries := listHistory(t, repository, automationID)
	if len(summaries) != 1 {
		t.Fatalf("history summaries = %#v, want exactly one", summaries)
	}
	return summaries[0]
}

// rowCount counts rows in one fixture-owned table.
func rowCount(t *testing.T, database *sql.DB, table string) int {
	t.Helper()
	var count int
	if err := database.QueryRowContext(context.Background(),
		fmt.Sprintf("SELECT count(*) FROM %s", table),
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}

// An uncovered snapshot caused by a definition edit must write no history,
// receipt, or Step and must return the complete current required Entity set,
// not only the missing subset.
func TestAdmitDeviceFactUncoveredSnapshotWritesNothingAndReturnsCompleteSet(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	firstEntity := newEntityID(t)
	secondEntity := newEntityID(t)
	if _, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("first", firstEntity, automations.ComparisonLessThan, "30"),
	)); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("second", secondEntity, automations.ComparisonLessThan, "30"),
	)); err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, trigger, admissionNow)
	_, err := repository.AdmitDeviceFact(ctx, fact, stateSnapshotWith(), admissionNow, admissionNow.Add(-time.Minute))
	var coverage *automations.ConditionSnapshotRequiredError
	if !errors.As(err, &coverage) {
		t.Fatalf("empty-snapshot admission error = %v, want ConditionSnapshotRequiredError", err)
	}
	if len(coverage.RequiredEntityIDs) != 1 ||
		(coverage.RequiredEntityIDs[0] != firstEntity && coverage.RequiredEntityIDs[0] != secondEntity) {
		t.Fatalf("required Entity set = %v, want one uncovered Automation requirement", coverage.RequiredEntityIDs)
	}
	if !errors.Is(err, automations.ErrConditionSnapshotRequired) {
		t.Fatalf("coverage error does not match ErrConditionSnapshotRequired: %v", err)
	}
	for _, table := range []string{"automation_history", "automation_fact_receipts", "automation_run_steps"} {
		if count := rowCount(t, database, table); count != 0 {
			t.Fatalf("%s rows = %d, want 0 after a coverage-error pass", table, count)
		}
	}
}

// One covered admission must atomically commit matching unconditional and
// conditional outcomes together, including a known missing State result.
func TestAdmitDeviceFactMixedConditionalAndUnconditionalMatchesCommitTogether(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	firstEntity := newEntityID(t)
	secondEntity := newEntityID(t)
	unconditional, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(t, trigger, nil))
	if err != nil {
		t.Fatal(err)
	}
	conditional, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("first", firstEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("second", secondEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	result, err := repository.AdmitDeviceFact(ctx, observationFactFor(t, trigger, admissionNow), stateSnapshotWith(
		presentStateEntry(t, firstEntity, `{"level":10}`, admissionNow),
		absentEntityEntry(secondEntity),
	), admissionNow, admissionNow.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.StartedRuns != 2 || result.Outcome.RecordedSkips != 1 || result.Outcome.MatchedAutomations != 3 {
		t.Fatalf("mixed admission outcome = %#v, want two Runs and one Skip", result.Outcome)
	}
	admittedIDs := make(map[automations.AutomationID]bool, len(result.StartedRuns))
	for _, run := range result.StartedRuns {
		admittedIDs[run.AutomationID] = true
	}
	if !admittedIDs[unconditional.ID] || !admittedIDs[conditional.ID] {
		t.Fatalf("started Runs = %v, want unconditional and true conditional", admittedIDs)
	}
	if len(result.Skips) != 1 || result.Skips[0].AutomationID != second.ID ||
		result.Skips[0].Reason != automations.SkipConditionsUnknown {
		t.Fatalf("mixed Condition Skip = %#v, want unknown second conditional", result.Skips)
	}
	if rowCount(t, database, "automation_fact_receipts") != 3 || rowCount(t, database, "automation_run_steps") != 2 {
		t.Fatalf("mixed admission did not commit all receipts and steps atomically")
	}
}

// Duplicate, stale, and busy siblings must not require Condition evidence.
func TestAdmitDeviceFactCoverageIgnoresIneligibleSiblings(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	conditional, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	// Hold the Automation busy with a bypassed manual Run so its Conditions are
	// never gathered or evaluated.
	admitted, err := repository.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: conditional.ID, BypassConditions: true},
		stateSnapshotWith(),
		admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Run == nil {
		t.Fatal("bypassed manual admission did not commit a Run")
	}

	fact := observationFactFor(t, trigger, admissionNow)
	first, err := repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), admissionNow, admissionNow.Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("busy sibling forced a coverage error: %v", err)
	}
	if first.Outcome.StartedRuns != 0 || first.Outcome.RecordedSkips != 1 {
		t.Fatalf("busy admission outcome = %#v, want one busy Skip", first.Outcome)
	}
	if first.Skips[0].Reason != automations.SkipBusy {
		t.Fatalf("busy Skip reason = %q", first.Skips[0].Reason)
	}
	entry := historyEntry(t, repository, conditional.ID, string(first.Skips[0].SkipID))
	if entry.Skip == nil || entry.Skip.ConditionDecision.DecisionMode() !=
		automations.ConditionDecisionNotEvaluated {
		t.Fatalf("busy Skip decision = %#v, want not_evaluated", entry.Skip)
	}

	// A duplicate redelivery is decided before Conditions too.
	second, err := repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), admissionNow, admissionNow.Add(-time.Minute),
	)
	if err != nil {
		t.Fatalf("duplicate redelivery forced a coverage error: %v", err)
	}
	if second.Outcome.DuplicateOutcomes != 1 {
		t.Fatalf("duplicate outcome = %#v, want one duplicate", second.Outcome)
	}
}

// A covered snapshot admits an evaluated Run and keeps the decision equal to the
// Run's full definition snapshot Conditions.
func TestAdmitDeviceFactEvaluatedRunCommitsWithSnapshotDecision(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	record, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, trigger, admissionNow)
	snapshot := stateSnapshotWith(presentStateEntry(t, conditionEntity, `{"level":10}`, admissionNow))
	result, err := repository.AdmitDeviceFact(ctx, fact, snapshot, admissionNow, admissionNow.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.StartedRuns != 1 || len(result.StartedRuns) != 1 {
		t.Fatalf("admission outcome = %#v", result.Outcome)
	}
	run := result.StartedRuns[0]
	if run.ConditionDecision.DecisionMode() != automations.ConditionDecisionEvaluated ||
		run.ConditionDecision.DecisionEvaluation() == nil ||
		run.ConditionDecision.DecisionEvaluation().Result != automations.ConditionTrue {
		t.Fatalf("Run decision = %#v, want an evaluated true decision", run.ConditionDecision)
	}
	entry := historyEntry(t, repository, record.ID, string(run.ID))
	if entry.Run == nil || entry.Run.ConditionDecision.DecisionEvaluation() == nil {
		t.Fatalf("stored Run decision = %#v", entry.Run)
	}
	if !slices.Equal(entry.Run.MatchedTriggerIDs, []automations.TriggerID{"occupied_and_warm"}) {
		t.Fatalf("stored matched triggers = %v", entry.Run.MatchedTriggerIDs)
	}
	if summary := firstHistorySummary(t, repository, record.ID); summary.ConditionMode !=
		automations.ConditionDecisionEvaluated || summary.Source != automations.RunSourceDeviceFact {
		t.Fatalf("history summary = %#v", summary)
	}
}

// Corrupt stored State is an error that commits nothing, never an unknown
// Condition result or a Condition Skip.
func TestAdmitDeviceFactCorruptStateWritesNothing(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	if _, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	)); err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, trigger, admissionNow)
	snapshot := stateSnapshotWith(presentStateEntry(t, conditionEntity, `{"level":`, admissionNow))
	_, err := repository.AdmitDeviceFact(ctx, fact, snapshot, admissionNow, admissionNow.Add(-time.Minute))
	if !errors.Is(err, devices.ErrEntityStateSnapshotCorrupt) {
		t.Fatalf("corrupt State error = %v, want ErrEntityStateSnapshotCorrupt", err)
	}
	for _, table := range []string{"automation_history", "automation_fact_receipts", "automation_run_steps"} {
		if count := rowCount(t, database, table); count != 0 {
			t.Fatalf("%s rows = %d, want 0 after corrupt State", table, count)
		}
	}
}

// An injected storage failure during fan-out must roll back every sibling write.
func TestAdmitDeviceFactStorageFailureRollsBackEveryWrite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	first, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(t, trigger, nil))
	if err != nil {
		t.Fatal(err)
	}
	second, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(t, trigger, nil))
	if err != nil {
		t.Fatal(err)
	}
	// Fail the higher Automation ID, which commits after the lower one.
	higher, lower := first, second
	if first.ID > second.ID {
		higher, lower = second, first
	}
	mustExec(t, database, fmt.Sprintf(`CREATE TRIGGER inject_storage_failure
		BEFORE INSERT ON automation_history
		WHEN NEW.kind = 'run' AND NEW.automation_id = '%s'
		BEGIN SELECT RAISE(ABORT, 'injected storage failure'); END`, higher.ID))

	fact := observationFactFor(t, trigger, admissionNow)
	if _, err = repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), admissionNow, admissionNow.Add(-time.Minute),
	); err == nil {
		t.Fatal("injected storage failure did not fail the admission")
	}
	for _, table := range []string{"automation_history", "automation_fact_receipts", "automation_run_steps"} {
		if count := rowCount(t, database, table); count != 0 {
			t.Fatalf("%s rows = %d, want 0 after a rolled-back fan-out", table, count)
		}
	}
	if history := listHistory(t, repository, lower.ID); len(history) != 0 {
		t.Fatalf("lower sibling history = %#v, want no partial outcome", history)
	}
}

// A pruned Condition Skip must still leave its matched-Fact receipt to block
// redelivery after State and definitions change.
func TestAdmitDeviceFactRedeliveryAfterPruningStaysDuplicate(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	record, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	fact := observationFactFor(t, trigger, admissionNow)
	snapshot := stateSnapshotWith(presentStateEntry(t, conditionEntity, `{"level":90}`, admissionNow))
	result, err := repository.AdmitDeviceFact(ctx, fact, snapshot, admissionNow, admissionNow.Add(-time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome.RecordedSkips != 1 || result.Skips[0].Reason != automations.SkipConditionsFalse {
		t.Fatalf("false-Condition outcome = %#v", result.Outcome)
	}
	pruned, err := repository.DeleteHistoryBefore(ctx, admissionNow.Add(time.Hour), 10)
	if err != nil {
		t.Fatal(err)
	}
	if pruned != 1 {
		t.Fatalf("pruned = %d, want the Condition Skip", pruned)
	}
	if rowCount(t, database, "automation_fact_receipts") != 1 {
		t.Fatalf("receipts after pruning = %d, want 1", rowCount(t, database, "automation_fact_receipts"))
	}
	// State is now true, but the retained receipt must still decide duplicate.
	replay, err := repository.AdmitDeviceFact(
		ctx,
		fact,
		stateSnapshotWith(presentStateEntry(t, conditionEntity, `{"level":5}`, admissionNow)),
		admissionNow,
		admissionNow.Add(-time.Minute),
	)
	if err != nil {
		t.Fatal(err)
	}
	if replay.Outcome.DuplicateOutcomes != 1 || replay.Outcome.StartedRuns != 0 {
		t.Fatalf("replay outcome = %#v, want a duplicate with no Run", replay.Outcome)
	}
	if history := listHistory(t, repository, record.ID); len(history) != 0 {
		t.Fatalf("replay history = %#v, want no new row", history)
	}
}

// requireManualSkipShape checks one committed manual Skip's provenance, absence
// of Fact/Step evidence, and stored decision.
func requireManualSkipShape(
	t *testing.T,
	database *sql.DB,
	repository *automationssqlite.AutomationRepository,
	record automations.Record,
	result automations.ManualAdmissionResult,
	wantReason automations.SkipReason,
) {
	t.Helper()
	skip := result.Skip
	if skip == nil || result.Run != nil {
		t.Fatalf("manual result = %#v, want exactly one Skip", result)
	}
	if skip.Source != automations.RunSourceManual || skip.Fact != nil ||
		len(skip.MatchedTriggers) != 0 || skip.Reason != wantReason {
		t.Fatalf("manual Skip = %#v", skip)
	}
	if rowCount(t, database, "automation_fact_receipts") != 0 {
		t.Fatal("manual Skip wrote a Fact receipt")
	}
	if rowCount(t, database, "automation_run_steps") != 0 {
		t.Fatal("manual Skip wrote a Step")
	}
	entry := historyEntry(t, repository, record.ID, string(skip.ID))
	if entry.Skip == nil || entry.Skip.Fact != nil ||
		entry.Skip.ConditionDecision.DecisionMode() != automations.ConditionDecisionEvaluated {
		t.Fatalf("stored manual Skip = %#v", entry.Skip)
	}
	summary := firstHistorySummary(t, repository, record.ID)
	if summary.Source != automations.RunSourceManual || summary.Fact != nil {
		t.Fatalf("manual Skip summary = %#v", summary)
	}
}

// requireManualRunShape checks one committed manual Run's provenance and
// evaluated decision.
func requireManualRunShape(t *testing.T, result automations.ManualAdmissionResult) {
	t.Helper()
	if result.Run == nil || result.Skip != nil {
		t.Fatalf("manual result = %#v, want exactly one Run", result)
	}
	if result.Run.Source != automations.RunSourceManual || result.Run.Fact != nil ||
		len(result.Run.MatchedTriggerIDs) != 0 {
		t.Fatalf("manual Run = %#v", result.Run)
	}
	if result.Run.ConditionDecision.DecisionMode() != automations.ConditionDecisionEvaluated ||
		result.Run.ConditionDecision.DecisionEvaluation().Result != automations.ConditionTrue {
		t.Fatalf("manual Run decision = %#v", result.Run.ConditionDecision)
	}
}

// False and unknown Conditions commit one manual Skip with no Fact, Step, or
// receipt, while a true Condition commits a manual Run.
func TestAdmitManualRunConditionOutcomesCommitOneOutcome(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		value      string
		wantReason automations.SkipReason
		wantSkip   bool
	}{
		{"false commits a Skip", `{"level":90}`, automations.SkipConditionsFalse, true},
		{"unknown commits a Skip", `{"other":1}`, automations.SkipConditionsUnknown, true},
		{"true commits a Run", `{"level":10}`, "", false},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			trigger := newEntityID(t)
			conditionEntity := newEntityID(t)
			record, err := repository.CreateAutomation(context.Background(), conditionalDefinitionFor(
				t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
			))
			if err != nil {
				t.Fatal(err)
			}
			result, err := repository.AdmitManualRun(
				context.Background(),
				automations.ManualRunInput{AutomationID: record.ID},
				stateSnapshotWith(presentStateEntry(t, conditionEntity, test.value, admissionNow)),
				admissionNow,
			)
			if err != nil {
				t.Fatal(err)
			}
			if !test.wantSkip {
				requireManualRunShape(t, result)
				return
			}
			requireManualSkipShape(t, database, repository, record, result, test.wantReason)
		})
	}
}

// An explicit bypass never reads State, is recorded on the admitted Run, and is
// classified not_configured when the definition omits Conditions.
func TestAdmitManualRunBypassNeverReadsState(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	configured, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	unconditioned, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(t, trigger, nil))
	if err != nil {
		t.Fatal(err)
	}
	bypassed, err := repository.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: configured.ID, BypassConditions: true},
		stateSnapshotWith(),
		admissionNow,
	)
	if err != nil {
		t.Fatalf("bypass requested a State read: %v", err)
	}
	decision := bypassed.Run.ConditionDecision
	if decision.DecisionMode() != automations.ConditionDecisionBypassed || !decision.BypassRequested() {
		t.Fatalf("bypassed Run decision = %#v", decision)
	}
	entry := historyEntry(t, repository, configured.ID, string(bypassed.Run.ID))
	if entry.Run == nil || entry.Run.ConditionDecision.DecisionMode() != automations.ConditionDecisionBypassed {
		t.Fatalf("stored bypassed Run = %#v", entry.Run)
	}

	requested, err := repository.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: unconditioned.ID, BypassConditions: true},
		stateSnapshotWith(),
		admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if requested.Run.ConditionDecision.DecisionMode() != automations.ConditionDecisionNotConfigured {
		t.Fatalf("unconditioned bypass decision = %#v", requested.Run.ConditionDecision)
	}
}

// Manual busy returns the busy class without writing a Skip, and manual coverage
// failure requests the complete required set before any write.
func TestAdmitManualRunBusyAndCoveragePrecedeWrites(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	trigger := newEntityID(t)
	conditionEntity := newEntityID(t)
	record, err := repository.CreateAutomation(ctx, conditionalDefinitionFor(
		t, trigger, conditionLeaf("dark", conditionEntity, automations.ComparisonLessThan, "30"),
	))
	if err != nil {
		t.Fatal(err)
	}
	_, err = repository.AdmitManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID}, stateSnapshotWith(), admissionNow,
	)
	var coverage *automations.ConditionSnapshotRequiredError
	if !errors.As(err, &coverage) {
		t.Fatalf("uncovered manual admission error = %v, want ConditionSnapshotRequiredError", err)
	}
	if !slices.Equal(coverage.RequiredEntityIDs, []devices.EntityID{conditionEntity}) {
		t.Fatalf("manual required set = %v", coverage.RequiredEntityIDs)
	}
	if rowCount(t, database, "automation_history") != 0 {
		t.Fatal("uncovered manual admission wrote history")
	}
	// Admit a Run, then prove busy precedes Conditions with no new Skip.
	admitted, err := repository.AdmitManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID, BypassConditions: true},
		stateSnapshotWith(), admissionNow,
	)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Run == nil {
		t.Fatal("bypassed manual admission did not commit a Run")
	}
	if _, err = repository.AdmitManualRun(
		ctx, automations.ManualRunInput{AutomationID: record.ID}, stateSnapshotWith(), admissionNow,
	); !errors.Is(err, automations.ErrAutomationBusy) {
		t.Fatalf("busy manual admission error = %v, want ErrAutomationBusy", err)
	}
	if count := rowCount(t, database, "automation_history"); count != 1 {
		t.Fatalf("history rows = %d, want only the admitted Run", count)
	}
}
