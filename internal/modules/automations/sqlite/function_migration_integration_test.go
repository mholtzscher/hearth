//go:build integration

package sqlite_test

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Upgrade/Down preserve all older rows; the new logical link has only a history FK.
func TestFunctionMigrationPreservesVersionSeventeenData(t *testing.T) {
	t.Parallel()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "v17.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 17); err != nil {
		t.Fatal(err)
	}
	id, runID := newAutomationIDString(t), newRunIDString(t)
	definition, err := automations.EncodeDefinition(validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`INSERT INTO automations (id, revision, definition_json, created_at, updated_at) VALUES (?, 1, ?, ?, ?)`,
		id,
		string(definition),
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(t, database, insertHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`, string(definition), runID)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_steps (run_id, step_id, position, status) VALUES (?, 'light_on', 0, 'not_attempted')`,
		runID,
	)
	tables := []string{
		"automations",
		"automation_history",
		"automation_run_steps",
		"automation_run_branch_decisions",
		"automation_run_delays",
		"automation_run_notifications",
	}
	for _, table := range tables {
		//nolint:unqueryvet // Complete-row independent oracle protects old migration data.
		mustExec(t, database, `CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table)
	}
	if _, err = provider.UpTo(t.Context(), 18); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	repository := newAutomationRepository(t, database)
	entry, err := repository.GetHistoryEntry(t.Context(), automations.AutomationID(id), runID)
	if err != nil {
		t.Fatal(err)
	}
	old := entry.(automations.Run)
	if old.Functions == nil || len(old.Functions) != 0 ||
		automations.RunStateStatus(old.State) != automations.RunSucceeded {
		t.Fatalf("old history=%#v", old)
	}
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status) VALUES (?, 'logical-link', 0, 'reserved-only', ?, 'running')`,
		runID,
		migrationTimestamp,
	)
	if _, err = provider.Down(t.Context()); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	var objects int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name IN ('automation_run_functions', 'automation_run_functions_one_running_idx')`).
		Scan(&objects); err != nil ||
		objects != 0 {
		t.Fatalf("Down objects=%d %v", objects, err)
	}
	if _, err = provider.UpTo(t.Context(), 18); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	assertScheduleMigrationIntegrity(t, database)
}

func TestFunctionMigrationRejectsContradictoryEvidence(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"orphan", "negative position", "position overflow", "running revision", "running outcome", "running reason", "running completion", "success no revision", "success no outcome", "success reason", "success wrong outcome", "failure no completion", "failure no reason", "failure partial evidence", "failure invented admission", "failure success", "interruption revision", "interruption outcome", "interruption reason", "duplicate invocation", "duplicate Step", "duplicate position", "two running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			_, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			runID, stepID, position, invocation, status := string(run.ID), "first", 0, "reserved-only", "running"
			var completed, revision, outcome, reason any
			switch fault {
			case "orphan":
				runID = newRunIDString(t)
			case "negative position":
				position = -1
			case "position overflow":
				position = 32
			case "running revision":
				revision = string(functionStorageCatalog)
			case "running outcome":
				outcome = "succeeded"
			case "running reason":
				reason = "bad"
			case "running completion":
				completed = migrationTimestamp
			case "success no revision":
				status, completed, outcome = "succeeded", migrationTimestamp, "succeeded"
			case "success no outcome":
				status, completed, revision = "succeeded", migrationTimestamp, string(functionStorageCatalog)
			case "success reason":
				status, completed, revision, outcome, reason = "succeeded", migrationTimestamp, string(
					functionStorageCatalog,
				), "succeeded", "bad"
			case "success wrong outcome":
				status, completed, revision, outcome = "succeeded", migrationTimestamp, string(
					functionStorageCatalog,
				), "unknown"
			case "failure no completion":
				status, revision, outcome, reason = "failed", string(
					functionStorageCatalog,
				), "unknown", "provider_error"
			case "failure no reason":
				status, completed, revision, outcome = "failed", migrationTimestamp, string(
					functionStorageCatalog,
				), "unknown"
			case "failure partial evidence":
				status, completed, revision, reason = "failed", migrationTimestamp, string(
					functionStorageCatalog,
				), "provider_error"
			case "failure invented admission":
				status, completed, reason = "failed", migrationTimestamp, "invented"
			case "failure success":
				status, completed, revision, outcome, reason = "failed", migrationTimestamp, string(
					functionStorageCatalog,
				), "succeeded", "provider_error"
			case "interruption revision":
				status, completed, revision, reason = "interrupted", migrationTimestamp, string(
					functionStorageCatalog,
				), "core_restarted"
			case "interruption outcome":
				status, completed, outcome, reason = "interrupted", migrationTimestamp, "unknown", "core_restarted"
			case "interruption reason":
				status, completed, reason = "interrupted", migrationTimestamp, "invented"
			default:
				mustExec(
					t,
					database,
					`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status, completed_at, catalog_revision, invocation_outcome) VALUES (?, 'first', 0, 'reserved-only', ?, 'succeeded', ?, ?, 'succeeded')`,
					runID,
					migrationTimestamp,
					migrationTimestamp,
					string(functionStorageCatalog),
				)
				stepID, position, invocation = "second", 1, "other-reservation"
				switch fault {
				case "duplicate invocation":
					invocation = "reserved-only"
				case "duplicate Step":
					stepID = "first"
				case "duplicate position":
					position = 0
				case "two running":
					mustExec(
						t,
						database,
						`UPDATE automation_run_functions SET status = 'running', completed_at = NULL, catalog_revision = NULL, invocation_outcome = NULL`,
					)
				}
			}
			if _, err := database.Exec(
				`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status, completed_at, catalog_revision, invocation_outcome, reason_code) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
				runID,
				stepID,
				position,
				invocation,
				migrationTimestamp,
				status,
				completed,
				revision,
				outcome,
				reason,
			); err == nil {
				t.Fatal("contradictory SQL evidence accepted")
			}
		})
	}
}
