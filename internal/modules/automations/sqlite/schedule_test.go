package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

func scheduleTime(t *testing.T, text string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, text)
	if err != nil {
		t.Fatal(err)
	}
	return at
}

func scheduleDefinition(t *testing.T, expressions ...string) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Triggers = nil
	for i, expression := range expressions {
		definition.Triggers = append(
			definition.Triggers,
			automations.Trigger{
				ID:   automations.TriggerID(string(rune('a' + i))),
				Body: automations.CronTrigger{Expression: expression},
			},
		)
	}
	return definition
}

func scheduleRepo(database *sql.DB, at *time.Time) *automationssqlite.AutomationRepository {
	return automationssqlite.NewAutomationRepository(
		database,
		automations.Dependencies{Now: func() time.Time { return *at }},
	)
}

func scheduleTick(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	at time.Time,
	location *time.Location,
	snapshot devices.EntityStateSnapshot,
) automations.AdmissionResult {
	t.Helper()
	result, err := repository.AdmitDueSchedules(
		context.Background(),
		snapshot,
		automations.ScheduleTick{At: at, Location: location},
	)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

func assertWatermark(t *testing.T, database *sql.DB, want time.Time) {
	t.Helper()
	var got string
	if err := database.QueryRow(`SELECT highwater_at FROM automation_schedule_watermarks WHERE id = 'global'`).
		Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != encodeStoredTimestamp(want) {
		t.Fatalf("watermark = %s, want %s", got, encodeStoredTimestamp(want))
	}
}

// Independent repository inputs and persisted progress must fail closed.
func TestScheduleRejectsInvalidInputsAndCorruptWatermarks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	if err := repository.InitializeScheduleWatermark(
		ctx,
		time.Time{},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("zero activation = %v", err)
	}
	for _, tick := range []automations.ScheduleTick{{Location: time.UTC}, {At: admissionNow}} {
		if _, err := repository.AdmitDueSchedules(
			ctx,
			stateSnapshotWith(),
			tick,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("invalid tick = %v", err)
		}
	}
	for _, value := range []string{"2026-10-02T09:00:01.000000000Z", "2026-10-02T09:00:00.000000001Z", "xxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"} {
		mustExec(t, database, `INSERT INTO automation_schedule_watermarks VALUES ('global', ?)
			ON CONFLICT (id) DO UPDATE SET highwater_at = excluded.highwater_at`, value)
		if err := repository.InitializeScheduleWatermark(
			ctx,
			admissionNow,
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("corrupt initialization = %v", err)
		}
		if _, err := repository.AdmitDueSchedules(
			ctx,
			stateSnapshotWith(),
			automations.ScheduleTick{At: admissionNow, Location: time.UTC},
		); !errors.Is(
			err,
			automations.ErrInvalidAutomation,
		) {
			t.Fatalf("corrupt progress = %v", err)
		}
		var retained string
		if err := database.QueryRow(`SELECT highwater_at FROM automation_schedule_watermarks`).
			Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if retained != value {
			t.Fatalf("corrupt watermark was overwritten with %s", retained)
		}
	}
}

// A4/A6: matching alone cannot admit disabled, newly written, past or future schedules.
func TestScheduleOnlyCurrentEligibleDefinitionsNeedSnapshots(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-10-02T08:58:00Z")
	repository := scheduleRepo(database, &at)
	for _, expression := range []string{"0 8 * * *", "0 10 * * *"} {
		definition := scheduleDefinition(t, expression)
		definition.Conditions = conditionLeaf("uncovered", newEntityID(t), automations.ComparisonLessThan, "30")
		if _, err := repository.CreateAutomation(ctx, definition); err != nil {
			t.Fatal(err)
		}
	}
	disabled := scheduleDefinition(t, "* * * * *")
	disabled.Enabled = false
	disabled.Conditions = conditionLeaf("uncovered", newEntityID(t), automations.ComparisonLessThan, "30")
	if _, err := repository.CreateAutomation(ctx, disabled); err != nil {
		t.Fatal(err)
	}
	if err := repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	at = scheduleTime(t, "2026-10-02T09:00:20Z")
	if _, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "0 9 * * *")); err != nil {
		t.Fatal(err)
	}
	if _, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *")); err != nil {
		t.Fatal(err)
	}
	result := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if result.Outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("ineligible definitions = %#v", result)
	}
	assertWatermark(t, database, scheduleTime(t, "2026-10-02T09:00:00Z"))
	// At 09:01 the prior 09:00 match is never recovered; only the every-minute definition is eligible.
	result = scheduleTick(t, repository, scheduleTime(t, "2026-10-02T09:01:00Z"), time.UTC, stateSnapshotWith())
	if result.Outcome.StartedRuns != 1 ||
		!result.StartedRuns[0].StartedAt.Equal(scheduleTime(t, "2026-10-02T09:01:00Z")) {
		t.Fatalf("next minute = %#v", result)
	}
}

// A4/A5: grouped IDs, immutable history, busy precedence, and ordinary false/unknown evidence.
func TestScheduleGroupedAdmissionAndConditionSkips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-11-01T06:00:00Z")
	repository := scheduleRepo(database, &at)
	definition := scheduleDefinition(t, "* * * * *", "*/15 * * * *")
	record, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	at = scheduleTime(t, "2026-11-01T06:15:59Z")
	result := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if result.Outcome != (automations.AdmissionOutcome{MatchedAutomations: 1, StartedRuns: 1}) {
		t.Fatalf("outcome = %#v", result.Outcome)
	}
	run := assertGroupedScheduleHistory(t, repository, record.ID, result.StartedRuns[0].ID, at)
	entityID := newEntityID(t)
	definition.Conditions = conditionLeaf("dark", entityID, automations.ComparisonLessThan, "30")
	if _, err = repository.ReplaceAutomation(ctx, record.ID, record.Revision, definition); err != nil {
		t.Fatal(err)
	}
	// Busy must not demand missing snapshot coverage or evaluate the new tree.
	at = scheduleTime(t, "2026-11-01T06:30:20Z")
	busy := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if len(busy.Skips) != 1 || busy.Skips[0].Reason != automations.SkipBusy {
		t.Fatalf("busy = %#v", busy)
	}
	skip := skipEntry(historyEntry(t, repository, record.ID, string(busy.Skips[0].SkipID)))
	if skip.ConditionDecision.DecisionMode() != automations.ConditionDecisionNotEvaluated ||
		causeFact(skip.Cause) != nil || heldEvidence(skip.Cause) != nil || len(skip.MatchedTriggers) != 2 {
		t.Fatalf("Skip = %#v", skip)
	}
	if err = repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	at = scheduleTime(t, "2026-11-01T06:45:20Z")
	blocked := scheduleTick(
		t,
		repository,
		at,
		time.UTC,
		stateSnapshotWith(presentStateEntry(t, entityID, `{"level":90}`, at)),
	)
	if len(blocked.Skips) != 1 || blocked.Skips[0].Reason != automations.SkipConditionsFalse {
		t.Fatalf("false = %#v", blocked)
	}
	at = scheduleTime(t, "2026-11-01T07:00:20Z")
	unknown := scheduleTick(
		t,
		repository,
		at,
		time.UTC,
		stateSnapshotWith(absentEntityEntry(entityID)),
	)
	if len(unknown.Skips) != 1 || unknown.Skips[0].Reason != automations.SkipConditionsUnknown {
		t.Fatalf("unknown = %#v", unknown)
	}
	for _, outcome := range []automations.AdmissionResult{blocked, unknown} {
		entry := skipEntry(historyEntry(t, repository, record.ID, string(outcome.Skips[0].SkipID)))
		if automations.CauseSource(entry.Cause) != automations.RunSourceSchedule ||
			entry.ConditionDecision.DecisionMode() != automations.ConditionDecisionEvaluated ||
			causeFact(entry.Cause) != nil ||
			heldEvidence(entry.Cause) != nil {
			t.Fatalf("condition Skip = %#v", entry)
		}
	}
	var receipts int
	if err = database.QueryRow(`SELECT count(*) FROM automation_fact_receipts`).Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 0 {
		t.Fatalf("schedule wrote %d Fact receipts", receipts)
	}
}

func assertGroupedScheduleHistory(t *testing.T, repository *automationssqlite.AutomationRepository,
	automationID automations.AutomationID, runID automations.RunID, at time.Time,
) *automations.Run {
	t.Helper()
	run := runEntry(historyEntry(t, repository, automationID, string(runID)))
	if automations.CauseSource(run.Cause) != automations.RunSourceSchedule ||
		causeFact(run.Cause) != nil || heldEvidence(run.Cause) != nil ||
		!slices.Equal(run.MatchedTriggerIDs, []automations.TriggerID{"a", "b"}) ||
		!run.StartedAt.Equal(at) ||
		len(run.Steps) != 1 ||
		automations.StepAttemptStatus(run.Steps[0].State) != automations.StepNotAttempted {
		t.Fatalf("Run = %#v", run)
	}
	summary := firstHistorySummary(t, repository, automationID)
	if automations.CauseSource(summary.Cause) != automations.RunSourceSchedule || causeFact(summary.Cause) != nil ||
		heldEvidence(summary.Cause) != nil {
		t.Fatalf("summary = %#v", summary)
	}
	return run
}

// A3/A8: literal UTC fold fixtures must remain separate, including a restart between occurrences.
//
//nolint:gocognit // Keep each literal first/restart/second sequence together as the DST oracle.
func TestScheduleRepeatedDSTMinutesAdmitIndependently(t *testing.T) {
	t.Parallel()
	for _, fixture := range []struct{ zone, first, restart, second, expression string }{
		{"America/Chicago", "2026-11-01T06:30:00Z", "2026-11-01T07:00:20Z", "2026-11-01T07:30:00Z", "30 1 * * *"},
		{"Australia/Lord_Howe", "2026-04-04T14:45:00Z", "2026-04-04T15:00:20Z", "2026-04-04T15:15:00Z", "45 1 * * *"},
	} {
		t.Run(fixture.zone, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			first, second := scheduleTime(t, fixture.first), scheduleTime(t, fixture.second)
			at := first.Add(-time.Hour)
			repository := scheduleRepo(database, &at)
			if _, err := repository.CreateAutomation(ctx, scheduleDefinition(t, fixture.expression)); err != nil {
				t.Fatal(err)
			}
			if err := repository.InitializeScheduleWatermark(ctx, at); err != nil {
				t.Fatal(err)
			}
			location, err := time.LoadLocation(fixture.zone)
			if err != nil {
				t.Fatal(err)
			}
			for i, instant := range []time.Time{first, second} {
				if i == 1 {
					if err = repository.InitializeScheduleWatermark(ctx, scheduleTime(t, fixture.restart)); err != nil {
						t.Fatal(err)
					}
				}
				outcome := scheduleTick(t, repository, instant, location, stateSnapshotWith())
				if outcome.Outcome.StartedRuns != 1 {
					t.Fatalf("%s outcome = %#v", instant, outcome)
				}
				duplicate := scheduleTick(t, repository, instant.Add(59*time.Second), location, stateSnapshotWith())
				if duplicate.Outcome != (automations.AdmissionOutcome{}) {
					t.Fatalf("repeated minute = %#v", duplicate)
				}
				if err = repository.CompleteRun(
					ctx,
					automations.RunCompletion{RunID: outcome.StartedRuns[0].ID, Outcome: automations.SucceededRun{}},
				); err != nil {
					t.Fatal(err)
				}
			}
			assertWatermark(t, database, second)
		})
	}
}

// A6/A7/A8: no replay, future-only edits, no-match progress, pruning and durable monotonicity.
func TestScheduleProgressCurrentMinuteAndRestart(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := dbtest.OpenMigrated(t, path)
	at := scheduleTime(t, "2026-10-02T08:58:00Z")
	repository := scheduleRepo(database, &at)
	if _, err := repository.AdmitDueSchedules(
		ctx,
		stateSnapshotWith(),
		automations.ScheduleTick{At: at, Location: time.UTC},
	); !errors.Is(
		err,
		automations.ErrAdmissionUnavailable,
	) {
		t.Fatalf("missing initialization = %v", err)
	}
	if err := repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	// An empty definition set still advances progress.
	scheduleTick(t, repository, scheduleTime(t, "2026-10-02T08:59:59Z"), time.UTC, stateSnapshotWith())
	assertWatermark(t, database, scheduleTime(t, "2026-10-02T08:59:00Z"))
	record, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *"))
	if err != nil {
		t.Fatal(err)
	}
	at = scheduleTime(t, "2026-10-02T09:00:59Z")
	first := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if first.Outcome.StartedRuns != 1 {
		t.Fatalf("late current minute = %#v", first)
	}
	if err = repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: first.StartedRuns[0].ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	// Step-only replacement prevents admission in its current minute.
	at = scheduleTime(t, "2026-10-02T09:01:20Z")
	definition := record.Definition
	commandBody := definition.Steps[0].Body.(automations.CommandStep)
	commandBody.Parameters = devices.CommandParameters(`{"value":false}`)
	definition.Steps[0].Body = commandBody
	record, err = repository.ReplaceAutomation(ctx, record.ID, record.Revision, definition)
	if err != nil {
		t.Fatal(err)
	}
	if got := scheduleTick(
		t,
		repository,
		at,
		time.UTC,
		stateSnapshotWith(),
	); got.Outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("replacement current minute = %#v", got)
	}
	at = scheduleTime(t, "2026-10-02T09:11:00Z")
	stalled := scheduleTick(t, repository, at, time.UTC, stateSnapshotWith())
	if stalled.Outcome.StartedRuns != 1 || stalled.StartedRuns[0].Revision != record.Revision {
		t.Fatalf("stall = %#v", stalled)
	}
	if err = repository.CompleteRun(
		ctx,
		automations.RunCompletion{RunID: stalled.StartedRuns[0].ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	if rows, pruneErr := repository.DeleteHistoryBefore(ctx, at.Add(time.Hour), 100); pruneErr != nil || rows != 2 {
		t.Fatalf("prune = %d, %v", rows, pruneErr)
	}
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	database = dbtest.OpenMigrated(t, path)
	repository = scheduleRepo(database, &at)
	assertWatermark(t, database, at)
	// Restart consumes downtime and its activation minute, then allows the next minute.
	at = scheduleTime(t, "2026-10-02T09:20:20Z")
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	if got := scheduleTick(
		t,
		repository,
		at,
		time.UTC,
		stateSnapshotWith(),
	); got.Outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("activation = %#v", got)
	}
	future := scheduleTime(t, "2026-10-02T10:00:00Z")
	if err = repository.InitializeScheduleWatermark(ctx, future); err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	if got := scheduleTick(
		t,
		repository,
		scheduleTime(t, "2026-10-02T09:59:59Z"),
		time.UTC,
		stateSnapshotWith(),
	); got.Outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("backward clock = %#v", got)
	}
	assertWatermark(t, database, future)
	if got := scheduleTick(
		t,
		repository,
		future.Add(time.Minute),
		time.UTC,
		stateSnapshotWith(),
	); got.Outcome.StartedRuns != 1 {
		t.Fatalf("past future barrier = %#v", got)
	}
	if err = repository.DeleteAutomation(ctx, record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	scheduleTick(t, repository, future.Add(2*time.Minute), time.UTC, stateSnapshotWith())
	assertWatermark(t, database, future.Add(2*time.Minute))
}

// A7/A9: failures after the first history write must roll back every outcome and progress.
func TestScheduleTickRollsBackOutcomesAndWatermark(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-10-02T08:59:00Z")
	repository := scheduleRepo(database, &at)
	for range 2 {
		if _, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *")); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	// Real SQLite rejects the second initial Step, not a mocked transaction.
	mustExec(t, database, `CREATE TRIGGER fail_second_schedule BEFORE INSERT ON automation_run_steps
		WHEN (SELECT count(*) FROM automation_history) = 2 BEGIN SELECT RAISE(ABORT, 'second Step rejected'); END`)
	tick := automations.ScheduleTick{At: at.Add(time.Minute), Location: time.UTC}
	result, err := repository.AdmitDueSchedules(ctx, stateSnapshotWith(), tick)
	if err == nil || len(result.StartedRuns) != 0 || result.Outcome != (automations.AdmissionOutcome{}) {
		t.Fatalf("failure = %#v, %v", result, err)
	}
	assertWatermark(t, database, at)
	var count int
	if err = database.QueryRow(`SELECT count(*) FROM automation_history`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("rollback retained %d outcomes", count)
	}
	mustExec(t, database, `DROP TRIGGER fail_second_schedule`)
	committed := scheduleTick(t, repository, tick.At, time.UTC, stateSnapshotWith())
	if committed.Outcome.StartedRuns != 2 {
		t.Fatalf("retry = %#v", committed)
	}
	assertWatermark(t, database, tick.At)
}

// Transaction-local revisions and snapshot coverage win over stale preparation.
func TestScheduleCurrentDefinitionRequiresSnapshotCoverage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-10-02T08:58:00Z")
	repository := scheduleRepo(database, &at)
	record, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "0 10 * * *"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repository.ListEnabledAutomations(ctx); err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	at = scheduleTime(t, "2026-10-02T08:59:20Z")
	entityID := newEntityID(t)
	definition := scheduleDefinition(t, "0 9 * * *")
	definition.Conditions = conditionLeaf("dark", entityID, automations.ComparisonLessThan, "30")
	record, err = repository.ReplaceAutomation(ctx, record.ID, record.Revision, definition)
	if err != nil {
		t.Fatal(err)
	}
	tick := automations.ScheduleTick{At: scheduleTime(t, "2026-10-02T09:00:00Z"), Location: time.UTC}
	if _, err = repository.AdmitDueSchedules(
		ctx,
		stateSnapshotWith(),
		tick,
	); !errors.Is(
		err,
		automations.ErrConditionSnapshotRequired,
	) {
		t.Fatalf("uncovered current definition = %v", err)
	}
	assertWatermark(t, database, scheduleTime(t, "2026-10-02T08:58:00Z"))
	result := scheduleTick(
		t,
		repository,
		tick.At,
		time.UTC,
		stateSnapshotWith(presentStateEntry(t, entityID, `{"level":10}`, tick.At)),
	)
	if result.Outcome.StartedRuns != 1 || result.StartedRuns[0].Revision != record.Revision {
		t.Fatalf("current revision = %#v", result)
	}
}

// A9: all four admission sources serialize through Core's SQLite connection policy.
func TestScheduleFactHeldAndManualAdmissionConcurrency(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "hearth.db")
	database := dbtest.OpenMigrated(t, path)
	at := scheduleTime(t, "2026-10-02T08:59:00Z")
	repository := scheduleRepo(database, &at)
	entityID := newEntityID(t)
	definition := scheduleDefinition(t, "* * * * *")
	definition.Triggers = append(
		definition.Triggers,
		automations.Trigger{ID: "fact", Body: automations.ObservationTrigger{
			EntityID: entityID, Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
		}},
		automations.Trigger{ID: "held", Body: automations.HeldStateTrigger{
			EntityID:   entityID,
			ForSeconds: 1,
			Comparisons: []automations.ObservationComparison{
				{Operator: automations.ComparisonEqual, Operand: json.RawMessage(`true`)},
			},
		}},
	)
	record, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	fact, receiveOrder := heldObservationFact(t, entityID, at.Add(time.Minute), `true`, 1)
	seedHeldStateEntity(t, database, entityID, fact, receiveOrder)
	mustExec(t, database, `INSERT INTO automation_holds (
		automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at)
		VALUES (?, 1, 'held', 1, 'pending', ?, ?)`, string(record.ID), encodeStoredTimestamp(at), encodeStoredTimestamp(at.Add(time.Minute)))
	// Core's single-connection database policy is shared by all admission sources.
	manualRepo := scheduleRepo(database, &at)
	start := make(chan struct{})
	var wait sync.WaitGroup
	errorsSeen := make(chan error, 4)
	wait.Add(4)
	go func() {
		defer wait.Done()
		<-start
		_, admissionErr := manualRepo.AdmitManualRun(
			ctx,
			automations.ManualRunInput{AutomationID: record.ID},
			stateSnapshotWith(),
			at.Add(time.Minute),
		)
		errorsSeen <- admissionErr
	}()
	go func() {
		defer wait.Done()
		<-start
		_, admissionErr := repository.AdmitDueSchedules(
			ctx,
			stateSnapshotWith(),
			automations.ScheduleTick{At: at.Add(time.Minute), Location: time.UTC},
		)
		errorsSeen <- admissionErr
	}()
	go func() {
		defer wait.Done()
		<-start
		_, admissionErr := repository.AdmitDeviceFact(
			ctx,
			fact,
			stateSnapshotWith(),
			at.Add(time.Minute),
			at.Add(-time.Minute),
		)
		errorsSeen <- admissionErr
	}()
	go func() {
		defer wait.Done()
		<-start
		_, _, admissionErr := repository.AdmitDueHeldStates(
			ctx,
			stateSnapshotWith(),
			at.Add(time.Minute),
			at.Add(time.Minute),
			100,
		)
		errorsSeen <- admissionErr
	}()
	close(start)
	wait.Wait()
	close(errorsSeen)
	for admissionErr := range errorsSeen {
		if admissionErr != nil && !errors.Is(admissionErr, automations.ErrAutomationBusy) {
			t.Fatal(admissionErr)
		}
	}
	var active int
	if err = database.QueryRow(`SELECT count(*) FROM automation_history WHERE run_status = 'running'`).
		Scan(&active); err != nil {
		t.Fatal(err)
	}
	if active != 1 {
		t.Fatalf("concurrent admission left %d active Runs", active)
	}
	var outcomes int
	if err = database.QueryRow(`SELECT count(*) FROM automation_history`).Scan(&outcomes); err != nil {
		t.Fatal(err)
	}
	if outcomes < 3 || outcomes > 4 {
		t.Fatalf(
			"concurrent sources produced %d outcomes, want three automatic decisions and optional manual Run",
			outcomes,
		)
	}
	assertWatermark(t, database, at.Add(time.Minute))
	// Reopen through the real database owner, rather than retaining only an in-memory repository.
	if err = database.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := platformdb.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	assertWatermark(t, reopened, at.Add(time.Minute))
}
