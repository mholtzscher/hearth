//go:build integration

package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Additive migration preserves old snapshots/history and notification history across Up/Down/Up.
func TestNotificationMigrationPreservesVersionTwelveData(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "v12.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 12); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	_, run := insertHistoricalStorageRun(t, database, validDomainDefinition(t), at)
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_status = 'succeeded', run_completed_at = run_started_at WHERE id = ?`,
		string(run.ID),
	)
	// Standalone attempt IDs are logical links, not foreign keys or migration rewrite targets.
	mustExec(
		t,
		database,
		`INSERT INTO notification_attempts (id, destination_id, destination_name, adapter_id, local_key, runtime_id, title, message, started_at, deadline, completed_at, outcome)
		VALUES ('ntf_01980000-0000-7000-8000-000000000001', 'ndst_01980000-0000-7000-8000-000000000001', 'Home', 'alerts', 'home', 'run_01980000-0000-7000-8000-000000000001', '', 'old', ?, ?, ?, 'accepted')`,
		migrationTimestamp,
		migrationTimestamp,
		migrationTimestamp,
	)
	tables := []string{
		"automations",
		"automation_history",
		"automation_run_steps",
		"automation_run_delays",
		"automation_run_branch_decisions",
		"notification_attempts",
		"notification_destinations",
	}
	for _, table := range tables {
		//nolint:unqueryvet // Independent complete-row snapshots protect migration preservation.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
	}
	if _, err = provider.UpTo(t.Context(), 13); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	_, reached := insertHistoricalStorageRun(t, database, notificationStorageDefinition(t), at)
	// Build version-13 evidence with its historical SQL. The current repository
	// requires the complete current schema, including later reached-Step tables.
	start := notificationStorageStart(t, reached)
	mustExec(t, database, `INSERT INTO automation_run_notifications
		(run_id, step_id, position, attempt_id, status, started_at) VALUES (?, ?, 0, ?, 'running', ?)`,
		string(reached.ID), string(start.StepID), string(start.AttemptID), migrationTimestamp)
	for _, table := range tables {
		mustExec(t, database, `DELETE FROM expected_`+table)
		//nolint:unqueryvet // Preserve new definitions and immutable snapshots even on feature rollback.
		mustExec(t, database, `INSERT INTO expected_`+table+` SELECT * FROM `+table)
	}
	if _, err = provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	var objects int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('automation_run_notifications', 'automation_run_notifications_one_running_idx')`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("Down objects=%d %v", objects, err)
	}
	if _, err = provider.UpTo(t.Context(), 13); err != nil {
		t.Fatal(err)
	}
	// Historical preservation ends at v20; v21 intentionally resets Automations.
	if _, err = provider.UpTo(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	if err = database.QueryRow(`SELECT count(*) FROM automation_run_notifications`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("re-upgraded notification evidence = %d, %v", objects, err)
	}
}

// SQL constraints independently reject contradictory status/outcome shapes, duplicate links and orphan evidence.
func TestNotificationMigrationEvidenceConstraints(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"orphan", "negative position", "position overflow", "running outcome", "running completion", "accepted no outcome", "accepted wrong outcome", "accepted reason", "failed no completion", "failed no reason", "failed no outcome bad reason", "interrupted outcome", "interrupted bad reason", "duplicate attempt", "duplicate Step", "duplicate position", "two running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			database, _ := openAutomationMigrationDatabase(t, 13)
			_, run := insertHistoricalStorageRun(t, database, notificationStorageDefinition(t), time.Now().UTC())
			runID, stepID, position, attemptID, status := string(
				run.ID,
			), "first", 0, "ntf_01980000-0000-7000-8000-000000000001", "running"
			var completed, outcome, reason any
			switch fault {
			case "orphan":
				runID = newRunIDString(t)
			case "negative position":
				position = -1
			case "position overflow":
				position = 32
			case "running outcome":
				outcome = "accepted"
			case "running completion":
				completed = migrationTimestamp
			case "accepted no outcome":
				status, completed = "accepted", migrationTimestamp
			case "accepted wrong outcome":
				status, completed, outcome = "accepted", migrationTimestamp, "unknown"
			case "accepted reason":
				status, completed, outcome, reason = "accepted", migrationTimestamp, "accepted", "bad"
			case "failed no completion":
				status, outcome, reason = "failed", "unknown", "provider_error"
			case "failed no reason":
				status, completed, outcome = "failed", migrationTimestamp, "unknown"
			case "failed no outcome bad reason":
				status, completed, reason = "failed", migrationTimestamp, "provider_error"
			case "interrupted outcome":
				status, completed, outcome, reason = "interrupted", migrationTimestamp, "unknown", "core_restarted"
			case "interrupted bad reason":
				status, completed, reason = "interrupted", migrationTimestamp, "bad"
			default:
				mustExec(
					t,
					database,
					`INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at, completed_at, outcome) VALUES (?, 'first', 0, ?, 'accepted', ?, ?, 'accepted')`,
					runID,
					attemptID,
					migrationTimestamp,
					migrationTimestamp,
				)
				stepID, position, attemptID = "second", 1, "ntf_01980000-0000-7000-8000-000000000002"
				switch fault {
				case "duplicate attempt":
					attemptID = "ntf_01980000-0000-7000-8000-000000000001"
				case "duplicate Step":
					stepID = "first"
				case "duplicate position":
					position = 0
				case "two running":
					mustExec(
						t,
						database,
						`UPDATE automation_run_notifications SET status = 'running', completed_at = NULL, outcome = NULL`,
					)
				}
			}
			if _, err := database.Exec(
				`INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at, completed_at, outcome, reason_code) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				runID,
				stepID,
				position,
				attemptID,
				status,
				migrationTimestamp,
				completed,
				outcome,
				reason,
			); err == nil {
				t.Fatal("contradictory SQL evidence accepted")
			}
		})
	}
}
