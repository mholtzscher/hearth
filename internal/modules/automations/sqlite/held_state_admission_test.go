package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// This test protects receive-order idempotency and due admission; it fails if a
// redelivery restarts a hold or if an expired matching State does not commit one
// held-state Run and consume the hold in the same transaction.
func TestHeldStateFactCursorAndDueAdmissionAreAtomic(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	dependencies := automations.Dependencies{Now: func() time.Time { return at }}
	repository := automationssqlite.NewAutomationRepository(database, dependencies)
	entityID := newEntityID(t)
	definition := validDomainDefinition(t)
	definition.Triggers = []automations.Trigger{{
		ID: "held", Kind: automations.TriggerKindHeldState,
		HeldState: &automations.HeldStateTrigger{
			EntityID: entityID, ForSeconds: 10,
			Comparisons: []automations.ObservationComparison{{
				Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
			}},
		},
	}}
	if _, err := repository.CreateAutomation(ctx, definition); err != nil {
		t.Fatal(err)
	}
	fact, receiveOrder := heldObservationFact(t, entityID, at.Add(time.Second), `true`, 1)
	seedHeldStateEntity(t, database, entityID, fact, receiveOrder)
	if _, err := repository.AdmitDeviceFact(ctx, fact, stateSnapshotWith(), at.Add(2*time.Second), at); err != nil {
		t.Fatal(err)
	}
	var start, due string
	var phase string
	if err := database.QueryRowContext(ctx, `SELECT phase, started_at, due_at FROM automation_holds`).Scan(
		&phase, &start, &due,
	); err != nil {
		t.Fatal(err)
	}
	if phase != "pending" || start != encodeStoredTimestamp(fact.Observation.EmittedAt) ||
		due != encodeStoredTimestamp(fact.Observation.EmittedAt.Add(10*time.Second)) {
		t.Fatalf("first hold = phase %q, start %q, due %q", phase, start, due)
	}
	if _, err := repository.AdmitDeviceFact(ctx, fact, stateSnapshotWith(), at.Add(5*time.Second), at); err != nil {
		t.Fatal(err)
	}
	var unchangedDue string
	if err := database.QueryRowContext(ctx, `SELECT due_at FROM automation_holds`).Scan(&unchangedDue); err != nil {
		t.Fatal(err)
	}
	if unchangedDue != due {
		t.Fatalf("redelivery moved due_at from %s to %s", due, unchangedDue)
	}

	verifyDueHeldStateAdmission(t, repository, database, entityID, fact, at)
}

// This test protects receive-order monotonicity; it fails if a delayed older
// Fact rewinds the cursor or replaces the pending hold's original deadline.
func TestHeldStateOlderOutOfOrderFactDoesNotRewindHold(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Truncate(time.Second)
	repository := automationssqlite.NewAutomationRepository(
		database,
		automations.Dependencies{Now: func() time.Time { return at }},
	)
	entityID := newEntityID(t)
	definition := heldStateDefinition(t, entityID, nil)
	if _, err := repository.CreateAutomation(ctx, definition); err != nil {
		t.Fatal(err)
	}
	firstAt := at.Add(time.Second)
	first, _ := heldObservationFact(t, entityID, firstAt, `true`, 10)
	seedHeldStateEntity(t, database, entityID, first, 10)
	if _, err := repository.AdmitDeviceFact(
		ctx, first, stateSnapshotWith(), firstAt.Add(time.Second), at.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	older, _ := heldObservationFact(t, entityID, at.Add(3*time.Second), `false`, 9)
	seedHeldStateEntity(t, database, entityID, older, 9)
	if _, err := repository.AdmitDeviceFact(
		ctx, older, stateSnapshotWith(), at.Add(4*time.Second), at.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	var phase string
	var cursor int64
	var start, due string
	if err := database.QueryRowContext(ctx, `SELECT phase, last_receive_order, COALESCE(started_at, ''),
		COALESCE(due_at, '') FROM automation_holds`).Scan(&phase, &cursor, &start, &due); err != nil {
		t.Fatal(err)
	}
	if phase != "pending" || cursor != 10 || start != encodeStoredTimestamp(firstAt) ||
		due != encodeStoredTimestamp(firstAt.Add(10*time.Second)) {
		t.Fatalf("older Fact changed hold: phase=%q cursor=%d start=%q due=%q", phase, cursor, start, due)
	}
}

// This test protects consumption against a delayed off/on Fact backlog: State
// already incorporates those reports at expiry, so neither may re-arm the hold.
func TestDueHeldStateConsumesCurrentStateReceiveOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	repository := automationssqlite.NewAutomationRepository(
		database, automations.Dependencies{Now: func() time.Time { return at }},
	)
	entityID := newEntityID(t)
	if _, err := repository.CreateAutomation(ctx, heldStateDefinition(t, entityID, nil)); err != nil {
		t.Fatal(err)
	}
	first, _ := heldObservationFact(t, entityID, at.Add(time.Second), `true`, 1)
	seedHeldStateEntity(t, database, entityID, first, 1)
	if _, err := repository.AdmitDeviceFact(
		ctx, first, stateSnapshotWith(), at.Add(2*time.Second), at,
	); err != nil {
		t.Fatal(err)
	}

	delayedOff, _ := heldObservationFact(t, entityID, at.Add(2*time.Second), `false`, 2)
	seedHeldStateEntity(t, database, entityID, delayedOff, 2)
	delayedOn, _ := heldObservationFact(t, entityID, at.Add(3*time.Second), `true`, 3)
	seedHeldStateEntity(t, database, entityID, delayedOn, 3)
	dueAt := at.Add(11 * time.Second)
	result, processed, err := repository.AdmitDueHeldStates(ctx, stateSnapshotWith(), dueAt, 100)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || result.Outcome.StartedRuns != 1 {
		t.Fatalf("due admission processed=%d outcome=%#v, want one Run", processed, result.Outcome)
	}
	assertHeldState(t, database, "consumed", 3, "", "")

	for _, fact := range []automations.DeviceFact{delayedOff, delayedOn} {
		if _, err = repository.AdmitDeviceFact(
			ctx, fact, stateSnapshotWith(), dueAt.Add(time.Second), at,
		); err != nil {
			t.Fatal(err)
		}
		assertHeldState(t, database, "consumed", 3, "", "")
	}
	result, processed, err = repository.AdmitDueHeldStates(ctx, stateSnapshotWith(), dueAt.Add(20*time.Second), 100)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || result.Outcome.StartedRuns != 0 || result.Outcome.RecordedSkips != 0 {
		t.Fatalf("backlogged Facts created a second outcome: processed=%d outcome=%#v", processed, result.Outcome)
	}
}

// This test protects the due-time State recheck; it fails if an expired hold
// starts a Run or records a Skip after current State has become nonmatching.
func TestDueHeldStateCurrentNonmatchCancelsWithoutSkip(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	repository := automationssqlite.NewAutomationRepository(
		database,
		automations.Dependencies{Now: func() time.Time { return at }},
	)
	entityID := newEntityID(t)
	definition := heldStateDefinition(t, entityID, nil)
	record, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	firstAt := at.Add(time.Second)
	fact, _ := heldObservationFact(t, entityID, firstAt, `true`, 1)
	seedHeldStateEntity(t, database, entityID, fact, 1)
	if _, err = repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), firstAt.Add(time.Second), at.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	nonmatch, _ := heldObservationFact(t, entityID, at.Add(time.Second), `false`, 2)
	seedHeldStateEntity(t, database, entityID, nonmatch, 2)
	dueAt := firstAt.Add(10 * time.Second)
	result, processed, err := repository.AdmitDueHeldStates(ctx, stateSnapshotWith(), dueAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || result.Outcome.StartedRuns != 0 || result.Outcome.RecordedSkips != 0 ||
		len(result.StartedRuns) != 0 || len(result.Skips) != 0 {
		t.Fatalf("nonmatching current State produced outcome: processed=%d result=%#v", processed, result)
	}
	var phase string
	var cursor int64
	if err = database.QueryRowContext(ctx, `SELECT phase, last_receive_order FROM automation_holds
		WHERE automation_id = ?`, string(record.ID)).Scan(&phase, &cursor); err != nil {
		t.Fatal(err)
	}
	if phase != "idle" || cursor != 2 {
		t.Fatalf("cancelled hold phase=%q cursor=%d, want idle at current order 2", phase, cursor)
	}
	var history int
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM automation_history WHERE automation_id = ?`,
		string(record.ID)).Scan(&history); err != nil {
		t.Fatal(err)
	}
	if history != 0 {
		t.Fatalf("nonmatching due hold wrote %d history rows, want none", history)
	}
}

// This test protects three-valued Condition admission for due holds; it fails
// if false or unknown Conditions start Runs, disappear, or create duplicate Skips.
func TestDueHeldStateFalseAndUnknownConditionsRecordOneSkip(t *testing.T) {
	t.Parallel()
	for _, test := range []dueHeldStateConditionCase{
		{
			name: "false",
			snapshot: func(t *testing.T, entityID devices.EntityID, at time.Time) devices.EntityStateSnapshot {
				return stateSnapshotWith(presentStateEntry(t, entityID, `{"level":5}`, at))
			},
			wantResult: automations.ConditionFalse,
			wantReason: automations.SkipConditionsFalse,
		},
		{
			name: "unknown",
			snapshot: func(_ *testing.T, entityID devices.EntityID, _ time.Time) devices.EntityStateSnapshot {
				return stateSnapshotWith(absentEntityEntry(entityID))
			},
			wantResult: automations.ConditionUnknown,
			wantReason: automations.SkipConditionsUnknown,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertDueHeldStateConditionSkip(t, test)
		})
	}
}

type dueHeldStateConditionCase struct {
	name       string
	snapshot   func(*testing.T, devices.EntityID, time.Time) devices.EntityStateSnapshot
	wantResult automations.ConditionResult
	wantReason automations.SkipReason
}

func assertDueHeldStateConditionSkip(t *testing.T, test dueHeldStateConditionCase) {
	t.Helper()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	repository := automationssqlite.NewAutomationRepository(
		database,
		automations.Dependencies{Now: func() time.Time { return at }},
	)
	entityID := newEntityID(t)
	conditionEntity := newEntityID(t)
	condition := &automations.Condition{
		ID: "level_above_ten", Kind: automations.ConditionEntityState,
		EntityState: &automations.EntityStateCondition{
			EntityID: conditionEntity, Pointer: "/level", Operator: automations.ComparisonGreaterThan,
			Operand: json.RawMessage(`10`),
		},
	}
	record, err := repository.CreateAutomation(ctx, heldStateDefinition(t, entityID, condition))
	if err != nil {
		t.Fatal(err)
	}
	firstAt := at.Add(time.Second)
	fact, _ := heldObservationFact(t, entityID, firstAt, `true`, 1)
	seedHeldStateEntity(t, database, entityID, fact, 1)
	if _, err = repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), firstAt.Add(time.Second), at.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	dueAt := firstAt.Add(10 * time.Second)
	snapshot := test.snapshot(t, conditionEntity, dueAt)
	result, processed, err := repository.AdmitDueHeldStates(ctx, snapshot, dueAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || result.Outcome.StartedRuns != 0 || result.Outcome.RecordedSkips != 1 ||
		len(result.Skips) != 1 || result.Skips[0].Reason != test.wantReason {
		t.Fatalf("due condition outcome processed=%d result=%#v", processed, result)
	}
	entry, err := repository.GetHistoryEntry(ctx, record.ID, string(result.Skips[0].SkipID))
	if err != nil {
		t.Fatal(err)
	}
	evaluation := entry.Skip.ConditionDecision.DecisionEvaluation()
	if evaluation == nil || evaluation.Result != test.wantResult {
		t.Fatalf("stored condition result = %#v, want %q", evaluation, test.wantResult)
	}
	repeated, processed, err := repository.AdmitDueHeldStates(ctx, snapshot, dueAt, 10)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 0 || repeated.Outcome.RecordedSkips != 0 {
		t.Fatalf("consumed condition hold repeated outcome: processed=%d result=%#v", processed, repeated)
	}
}

// This test protects restart semantics; it fails if startup preserves a pending
// deadline or clears a consumed hold, or if a post-start matching report does not
// begin a fresh full-duration hold.
func TestResetPendingHeldStatesPreservesConsumedAndRestartsPendingDuration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	installHeldStateAdmissionSchema(t, database)
	at := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	repository := automationssqlite.NewAutomationRepository(
		database,
		automations.Dependencies{Now: func() time.Time { return at }},
	)
	entityID := newEntityID(t)
	definition := heldStateDefinition(t, entityID, nil)
	if _, err := repository.CreateAutomation(ctx, definition); err != nil {
		t.Fatal(err)
	}
	firstAt := at.Add(time.Second)
	fact, _ := heldObservationFact(t, entityID, firstAt, `true`, 1)
	seedHeldStateEntity(t, database, entityID, fact, 1)
	if _, err := repository.AdmitDeviceFact(
		ctx, fact, stateSnapshotWith(), firstAt.Add(time.Second), at.Add(-time.Minute),
	); err != nil {
		t.Fatal(err)
	}
	if err := repository.ResetPendingHeldStates(ctx); err != nil {
		t.Fatal(err)
	}
	assertHeldState(t, database, "idle", 1, "", "")

	postStartup := at.Add(20 * time.Second)
	unchanged, _ := heldObservationFact(t, entityID, postStartup, `true`, 2)
	seedHeldStateEntity(t, database, entityID, unchanged, 2)
	if _, err := repository.AdmitDeviceFact(
		ctx, unchanged, stateSnapshotWith(), postStartup, at.Add(10*time.Second),
	); err != nil {
		t.Fatal(err)
	}
	due := postStartup.Add(10 * time.Second)
	assertHeldState(t, database, "pending", 2, encodeStoredTimestamp(postStartup), encodeStoredTimestamp(due))
	if _, _, err := repository.AdmitDueHeldStates(ctx, stateSnapshotWith(), due, 10); err != nil {
		t.Fatal(err)
	}
	if err := repository.ResetPendingHeldStates(ctx); err != nil {
		t.Fatal(err)
	}
	assertHeldState(t, database, "consumed", 2, "", "")
}

func heldStateDefinition(
	t *testing.T,
	entityID devices.EntityID,
	conditions *automations.Condition,
) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Conditions = conditions
	definition.Triggers = []automations.Trigger{{
		ID: "held", Kind: automations.TriggerKindHeldState,
		HeldState: &automations.HeldStateTrigger{
			EntityID: entityID, ForSeconds: 10,
			Comparisons: []automations.ObservationComparison{{
				Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`),
			}},
		},
	}}
	return definition
}

func assertHeldState(
	t *testing.T,
	database *sql.DB,
	phase string,
	cursor int64,
	startedAt string,
	dueAt string,
) {
	t.Helper()
	var gotPhase, gotStartedAt, gotDueAt string
	var gotCursor int64
	if err := database.QueryRowContext(context.Background(), `SELECT phase, last_receive_order,
		COALESCE(started_at, ''), COALESCE(due_at, '') FROM automation_holds`).Scan(
		&gotPhase, &gotCursor, &gotStartedAt, &gotDueAt,
	); err != nil {
		t.Fatal(err)
	}
	if gotPhase != phase || gotCursor != cursor || gotStartedAt != startedAt || gotDueAt != dueAt {
		t.Fatalf("hold = phase %q cursor %d started %q due %q, want %q %d %q %q",
			gotPhase, gotCursor, gotStartedAt, gotDueAt, phase, cursor, startedAt, dueAt)
	}
}

func verifyDueHeldStateAdmission(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	database *sql.DB,
	entityID devices.EntityID,
	fact automations.DeviceFact,
	at time.Time,
) {
	t.Helper()
	ctx := context.Background()
	dueAt := fact.Observation.EmittedAt.Add(10 * time.Second)
	var phase, start string
	candidates, err := repository.ListDueHeldStates(ctx, dueAt, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidates) != 1 {
		t.Fatalf("due candidates = %d, want one", len(candidates))
	}
	result, processed, err := repository.AdmitDueHeldStates(ctx, stateSnapshotWith(), dueAt, 100)
	if err != nil {
		t.Fatal(err)
	}
	if processed != 1 || result.Outcome.StartedRuns != 1 || len(result.StartedRuns) != 1 {
		t.Fatalf("due admission processed=%d outcome=%#v", processed, result.Outcome)
	}
	run := result.StartedRuns[0]
	if run.Source != automations.RunSourceHeldState || run.Fact != nil || run.HeldState == nil ||
		run.HeldState.TriggerID != "held" || !run.HeldState.DueAt.Equal(dueAt) {
		t.Fatalf("held Run evidence = %#v", run)
	}
	if err = database.QueryRowContext(ctx, `SELECT phase FROM automation_holds`).Scan(&phase); err != nil {
		t.Fatal(err)
	}
	if phase != "consumed" {
		t.Fatalf("hold phase = %q, want consumed", phase)
	}
	repeated, err := repository.ListDueHeldStates(ctx, dueAt, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(repeated) != 0 {
		t.Fatalf("consumed hold reappeared as due: %#v", repeated)
	}
	clearingFact, clearingOrder := heldObservationFact(t, entityID, dueAt.Add(time.Second), `false`, 2)
	seedHeldStateEntity(t, database, entityID, clearingFact, clearingOrder)
	if _, err = repository.AdmitDeviceFact(
		ctx, clearingFact, stateSnapshotWith(), dueAt.Add(2*time.Second), at,
	); err != nil {
		t.Fatal(err)
	}
	var lastOrder int64
	if err = database.QueryRowContext(ctx, `SELECT phase, last_receive_order FROM automation_holds`).Scan(
		&phase, &lastOrder,
	); err != nil {
		t.Fatal(err)
	}
	if phase != "idle" || lastOrder != clearingOrder {
		t.Fatalf("new nonmatch left phase=%q cursor=%d, want idle at %d", phase, lastOrder, clearingOrder)
	}
	rearmingFact, rearmingOrder := heldObservationFact(t, entityID, dueAt.Add(3*time.Second), `true`, 3)
	seedHeldStateEntity(t, database, entityID, rearmingFact, rearmingOrder)
	if _, err = repository.AdmitDeviceFact(
		ctx, rearmingFact, stateSnapshotWith(), dueAt.Add(4*time.Second), at,
	); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRowContext(ctx, `SELECT phase, started_at FROM automation_holds`).
		Scan(&phase, &start); err != nil {
		t.Fatal(err)
	}
	if phase != "pending" || start != encodeStoredTimestamp(rearmingFact.Observation.EmittedAt) {
		t.Fatalf("rearmed hold phase=%q start=%q", phase, start)
	}
}

func installHeldStateAdmissionSchema(t *testing.T, database *sql.DB) {
	t.Helper()
	if _, err := database.Exec(`DROP TABLE automation_holds`); err != nil {
		t.Fatal(err)
	}
	_, err := database.Exec(`CREATE TABLE automation_holds (
		automation_id TEXT NOT NULL REFERENCES automations(id) ON DELETE CASCADE,
		revision INTEGER NOT NULL CHECK (revision >= 1),
		trigger_id TEXT NOT NULL,
		last_receive_order INTEGER NOT NULL CHECK (last_receive_order > 0),
		phase TEXT NOT NULL CHECK (phase IN ('idle', 'pending', 'consumed')),
		started_at TEXT,
		due_at TEXT,
		PRIMARY KEY (automation_id, trigger_id),
		CHECK ((phase = 'pending' AND started_at IS NOT NULL AND due_at IS NOT NULL)
			OR (phase <> 'pending' AND started_at IS NULL AND due_at IS NULL))
	)`)
	if err != nil {
		t.Fatal(err)
	}
}

func heldObservationFact(
	t *testing.T,
	entityID devices.EntityID,
	emittedAt time.Time,
	value string,
	receiveOrder int64,
) (automations.DeviceFact, int64) {
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
			FactID: factID, ObservationID: observationID, EntityID: entityID,
			Disposition: devices.DispositionApplied, Value: devices.Value(value), EmittedAt: emittedAt,
		},
	}, receiveOrder
}

func seedHeldStateEntity(
	t *testing.T,
	database *sql.DB,
	entityID devices.EntityID,
	fact automations.DeviceFact,
	receiveOrder int64,
) {
	t.Helper()
	ctx := context.Background()
	const deviceID = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if _, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO devices (id, kind, name, created_at, updated_at)
		VALUES (?, 'fixture', 'Fixture', ?, ?)`, deviceID, migrationTimestamp, migrationTimestamp); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `INSERT OR IGNORE INTO entities (
		id, device_id, name, type_id, support_json, created_at, updated_at
	) VALUES (?, ?, 'Fixture', 'fixture.v1', '{}', ?, ?)`, string(entityID), deviceID,
		migrationTimestamp, migrationTimestamp); err != nil {
		t.Fatal(err)
	}
	emittedAt := encodeStoredTimestamp(fact.Observation.EmittedAt)
	_, err := database.ExecContext(ctx, `INSERT INTO observations (
		receive_order, observation_id, adapter_id, entity_id, disposition, state_value_json,
		adapter_received_at, observed_at
	) VALUES (?, ?, 'fixture', ?, 'applied', ?, ?, ?)`,
		receiveOrder,
		string(fact.Observation.ObservationID), string(entityID), string(fact.Observation.Value),
		emittedAt, emittedAt,
	)
	if err != nil {
		t.Fatal(err)
	}
	if _, execErr := database.ExecContext(ctx, `INSERT INTO entity_states (
		entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
	) VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(entity_id) DO UPDATE SET
		observation_id = excluded.observation_id, value_json = excluded.value_json,
		adapter_received_at = excluded.adapter_received_at, observed_at = excluded.observed_at,
		receive_order = excluded.receive_order`, string(entityID), string(fact.Observation.ObservationID),
		string(fact.Observation.Value), emittedAt, emittedAt, receiveOrder); execErr != nil {
		t.Fatal(execErr)
	}
}
