package sqlite_test

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// A7: the additive migration preserves every v9 row and schema object through
// Up/Down/Up, including schedule guards and retained held/scheduled provenance.
//
//nolint:gocognit // One ordered migration sequence checks data and schema across both directions.
func TestBranchMigrationPreservesVersionNineDataAndSchema(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "v9.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 9); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	repository := scheduleRepo(database, &at)
	definition := validDomainDefinition(t)
	observation := definition.Triggers[0].Body.(automations.ObservationTrigger)

	definition.Triggers[0].Body = automations.HeldStateTrigger{
		EntityID: observation.EntityID, Comparisons: observation.Comparisons, ForSeconds: 60,
	}
	record, run := admitStorageRun(t, repository, definition, at)
	// Seed the old schema directly; current completion queries require migration 11.
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_status = 'succeeded', run_completed_at = run_started_at WHERE id = ?`,
		string(run.ID),
	)
	// Populate a retained held-state Run from the valid flat snapshot, plus its
	// durable cursor. These records have no dependency on live Devices rows.
	heldID := newRunIDString(t)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at, run_snapshot_json,
		run_source, run_status, run_started_at, run_completed_at, run_matched_trigger_ids_json,
		hold_trigger_id, hold_started_at, hold_due_at, condition_decision_json)
		SELECT ?, automation_id, automation_name, kind, revision, recorded_at, run_snapshot_json,
		'held_state', 'succeeded', run_started_at, run_completed_at, '["occupied_and_warm"]',
		'occupied_and_warm', run_started_at, ?, condition_decision_json FROM automation_history WHERE id = ?`,
		heldID, at.Add(time.Minute).Format("2006-01-02T15:04:05.000000000Z"), string(run.ID))
	mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
		SELECT ?, position, step_id, status FROM automation_run_steps WHERE run_id = ?`, heldID, string(run.ID))
	mustExec(
		t,
		database,
		`INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at)
		VALUES (?, 1, 'occupied_and_warm', 7, 'pending', ?, ?)`,
		string(record.ID),
		migrationTimestamp,
		"2026-09-01T00:01:00.000000000Z",
	)
	mustExec(t, database, insertHistorySkipSQL, newSkipIDString(t), string(record.ID), migrationTimestamp,
		newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), "true", migrationTimestamp,
		matchedTriggerJSON(t), `{"mode":"not_configured","bypass_requested":false}`)
	// Real schedule admission exercises both the retained Run and busy Skip,
	// as well as the global watermark, before the branching table exists.
	scheduled, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	result := scheduleTick(t, repository, at.Add(time.Minute), time.UTC, stateSnapshotWith())
	if len(result.StartedRuns) != 1 || result.StartedRuns[0].AutomationID != scheduled.ID {
		t.Fatalf("schedule Run = %#v", result)
	}
	if result = scheduleTick(
		t,
		repository,
		at.Add(2*time.Minute),
		time.UTC,
		stateSnapshotWith(),
	); len(
		result.Skips,
	) != 1 {
		t.Fatalf("schedule Skip = %#v", result)
	}
	tables := []string{
		"automations",
		"automation_history",
		"automation_run_steps",
		"automation_holds",
		"automation_schedule_watermarks",
		"automation_fact_receipts",
	}
	for _, table := range tables {
		//nolint:unqueryvet // Full row snapshots independently guard all migration columns.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
	}
	mustExec(
		t,
		database,
		`CREATE TEMP TABLE expected_schema AS SELECT type, name, tbl_name, sql FROM sqlite_master WHERE name NOT LIKE 'goose%'`,
	)
	for _, direction := range []string{"up", "down", "up again"} {
		if direction == "down" {
			_, err = provider.Down(ctx)
		} else {
			_, err = provider.UpTo(ctx, 10)
		}
		if err != nil {
			t.Fatalf("%s: %v", direction, err)
		}
		for _, table := range tables {
			assertMigrationRowsUnchanged(t, database, table)
		}
		var missingObjects int
		if err = database.QueryRow(`SELECT count(*) FROM (SELECT type, name, tbl_name, sql FROM expected_schema EXCEPT SELECT type, name, tbl_name, sql FROM sqlite_master)`).
			Scan(&missingObjects); err != nil ||
			missingObjects != 0 {
			t.Fatalf("%s changed schema objects = %d, %v", direction, missingObjects, err)
		}
		assertScheduleMigrationIntegrity(t, database)
		var branchTables int
		if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'automation_run_branch_decisions'`).
			Scan(&branchTables); err != nil {
			t.Fatal(err)
		}
		if direction == "down" {
			if branchTables != 0 {
				t.Fatal("Down retained branch table")
			}
		} else {
			if branchTables != 1 {
				t.Fatal("Up did not create branch table")
			}
			assertBranchOrphanRejected(t, database)
		}
	}
	// Retained reads use the current schema, after the independent v10 migration checks.
	if _, err = provider.UpTo(ctx, 11); err != nil {
		t.Fatal(err)
	}
	legacy := retainedStorageRun(t, repository, record, run)
	if legacy.BranchDecisions == nil || len(legacy.BranchDecisions) != 0 || len(legacy.Steps) != 1 ||
		legacy.Steps[0].Position != 0 || legacy.Steps[0].StepID != "light_on" {
		t.Fatalf("legacy history = %#v", legacy)
	}
}

func assertMigrationRowsUnchanged(t *testing.T, database *sql.DB, table string) {
	t.Helper()
	for _, pair := range [][2]string{{table, "expected_" + table}, {"expected_" + table, table}} {
		var differences int
		if err := database.QueryRow(`SELECT count(*) FROM (SELECT * FROM ` + pair[0] + ` EXCEPT SELECT * FROM ` + pair[1] + `)`).
			Scan(&differences); err != nil ||
			differences != 0 {
			t.Fatalf("%s changed rows = %d, %v", table, differences, err)
		}
	}
}

// Fresh migration exposes JSON, position, uniqueness, and foreign-key guards.
// Run-only parent eligibility belongs to the repository, not the child table.
func TestBranchMigrationFreshConstraintsAndSkipBoundary(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, branchingStorageDefinition(t), at)
	assertBranchOrphanRejected(t, database)
	for _, fixture := range []struct {
		position int
		payload  string
	}{{-1, "{}"}, {64, "{}"}, {0, "[]"}, {0, "null"}, {0, "{"}} {
		if _, err := database.Exec(
			`INSERT INTO automation_run_branch_decisions (run_id, step_id, position, decision_json) VALUES (?, 'route', ?, ?)`,
			string(run.ID),
			fixture.position,
			fixture.payload,
		); err == nil {
			t.Fatalf("invalid SQL row accepted: %#v", fixture)
		}
	}
	if err := repository.RecordBranchDecision(ctx, run.ID, storageDecision("route", 0, at)); err != nil {
		t.Fatal(err)
	}
	for _, fixture := range []struct {
		stepID   string
		position int
	}{{"route", 1}, {"fallback", 0}} {
		if _, err := database.Exec(
			`INSERT INTO automation_run_branch_decisions (run_id, step_id, position, decision_json) VALUES (?, ?, ?, '{}')`,
			string(run.ID),
			fixture.stepID,
			fixture.position,
		); err == nil {
			t.Fatalf("duplicate SQL row accepted: %#v", fixture)
		}
	}
	skipID := newSkipIDString(t)
	mustExec(t, database, insertHistorySkipSQL, skipID, string(record.ID), migrationTimestamp,
		newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), "true", migrationTimestamp,
		matchedTriggerJSON(t), `{"mode":"not_configured","bypass_requested":false}`)
	// A canonical Run ID on a Skip row tests the parent-kind boundary without
	// merely failing ParseRunID on a Skip ID prefix.
	skipRunID := automations.RunID(newRunIDString(t))
	mustExec(t, database, `UPDATE automation_history SET id = ? WHERE id = ?`, string(skipRunID), skipID)
	if err := repository.RecordBranchDecision(ctx, skipRunID, storageDecision("route", 0, at)); err == nil {
		t.Fatal("Skip accepted branch evidence")
	}
	var decisions int
	if err := database.QueryRow(`SELECT count(*) FROM automation_run_branch_decisions WHERE run_id = ?`, string(skipRunID)).
		Scan(&decisions); err != nil ||
		decisions != 0 {
		t.Fatalf("Skip evidence = %d, %v", decisions, err)
	}
}
