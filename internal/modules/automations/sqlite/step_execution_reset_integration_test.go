//go:build integration

package sqlite_test

import (
	"database/sql"
	"strings"
	"testing"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func TestStepExecutionMigrationResetsAutomationDataAndPreservesOtherModules(t *testing.T) {
	t.Parallel()
	db, provider := revisionMigrationDatabase(t)
	if _, err := provider.UpTo(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	automationID, runID := newAutomationIDString(t), newRunIDString(t)
	mustExec(t, db, `INSERT INTO automation_definition_revisions VALUES (?, 1, '{"name":"old"}')`, automationID)
	mustExec(t, db, `INSERT INTO automations VALUES (?, 1, ?, ?)`, automationID, migrationTimestamp, migrationTimestamp)
	mustExec(
		t,
		db,
		`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, definition_revision, run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json) VALUES (?, ?, 'Old', 'run', 1, ?, 1, 'manual', 'running', ?, '[]', '{}')`,
		runID,
		automationID,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json) VALUES (?, ?, 'Old', 'skip', 1, ?, '[]', 'conditions_false', 'manual', '{}')`,
		newSkipIDString(t),
		automationID,
		migrationTimestamp,
	)
	// Seed all five v21 evidence tables, as well as hold and module-owned linked rows.
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, 0, 'command', 'not_attempted')`,
		runID,
	)
	mustExec(t, db, `INSERT INTO automation_run_branch_decisions VALUES (?, 'branch', 0, '{}')`, runID)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at) VALUES (?, 'wait', 0, 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at) VALUES (?, 'notify', 0, 'linked-attempt', 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status) VALUES (?, 'function', 0, 'linked-invocation', ?, 'running')`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 1, 'held', 1, 'idle')`,
		automationID,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_fact_receipts VALUES (?, ?, 'run', ?)`,
		newFactIDString(t),
		automationID,
		runID,
	)
	mustExec(t, db, `INSERT INTO automation_schedule_watermarks VALUES ('global', ?)`, migrationTimestamp)
	oldTables := []string{
		"automation_definition_revisions",
		"automations",
		"automation_history",
		"automation_holds",
		"automation_fact_receipts",
		"automation_schedule_watermarks",
		"automation_run_steps",
		"automation_run_branch_decisions",
		"automation_run_delays",
		"automation_run_notifications",
		"automation_run_functions",
	}
	seedOtherModuleResetRows(t, db)
	other := snapshotOtherModuleRows(t, db)
	if _, err := provider.UpTo(t.Context(), 22); err != nil {
		t.Fatal(err)
	}
	for _, table := range oldTables {
		if table == "automation_run_steps" || table == "automation_run_branch_decisions" ||
			table == "automation_run_delays" ||
			table == "automation_run_notifications" ||
			table == "automation_run_functions" {
			assertTableAbsent(t, db, table)
		} else if n := rowCount(
			t,
			db,
			table,
		); n != 0 {
			t.Errorf("%s retained %d rows", table, n)
		}
	}
	for _, table := range other {
		assertMigrationRowsUnchanged(t, db, table)
	}
	assertMigrationRowsUnchanged(t, db, "nonautomation_schema")
	var fk int
	if err := db.QueryRow(`PRAGMA foreign_keys`).Scan(&fk); err != nil || fk != 1 {
		t.Fatalf("foreign_keys=%d err=%v", fk, err)
	}
	if _, err := provider.Down(t.Context()); err == nil || !strings.Contains(err.Error(), "pre-upgrade backup") {
		t.Fatalf("unsafe Down = %v", err)
	}
}

func TestStepExecutionRepeatedStartupPreservesNewEvidenceAndHistoryCascade(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	newID, newRun := newAutomationIDString(t), newRunIDString(t)
	mustExec(t, db, `INSERT INTO automation_definition_revisions VALUES (?, 1, '{"name":"new","triggers":[]}')`, newID)
	mustExec(t, db, `INSERT INTO automations VALUES (?, 1, ?, ?)`, newID, migrationTimestamp, migrationTimestamp)
	mustExec(
		t,
		db,
		`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, definition_revision, run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json) VALUES (?, ?, 'New', 'run', 1, ?, 1, 'manual', 'running', ?, '[]', '{}')`,
		newRun,
		newID,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_step_executions VALUES (?, 'wait', 0, 'delay', 'running', ?, NULL, '{}')`,
		newRun,
		migrationTimestamp,
	)
	if err := platformdb.Migrate(t.Context(), db); err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{`SELECT count(*) FROM automation_definition_revisions WHERE automation_id = ? AND revision = 1`, `SELECT count(*) FROM automations WHERE id = ?`, `SELECT count(*) FROM automation_history WHERE id = ? AND run_status = 'running'`, `SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND step_id = 'wait' AND sequence = 0 AND kind = 'delay' AND phase = 'running' AND evidence_json = '{}'`} {
		arg := newID
		if strings.Contains(query, "automation_history") || strings.Contains(query, "automation_step_executions") {
			arg = newRun
		}
		var n int
		if err := db.QueryRow(query, arg).Scan(&n); err != nil || n != 1 {
			t.Fatalf("repeat migration lost post-22 row: %s count=%d err=%v", query, n, err)
		}
	}
	mustExec(t, db, `DELETE FROM automations WHERE id = ?`, newID)
	if n := rowCount(t, db, "automation_step_executions"); n != 1 {
		t.Fatalf("head deletion removed retained execution, count=%d", n)
	}
	if n := rowCount(t, db, "automation_definition_revisions"); n != 1 {
		t.Fatalf("head delete removed artifact, count=%d", n)
	}
	mustExec(t, db, `DELETE FROM automation_history WHERE id = ?`, newRun)
	if n := rowCount(t, db, "automation_step_executions"); n != 0 {
		t.Fatalf("history cascade left %d executions", n)
	}
}

func TestStepExecutionMigrationRollsBackOnSchemaCollision(t *testing.T) {
	t.Parallel()
	db, provider := revisionMigrationDatabase(t)
	if _, err := provider.UpTo(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	id := newAutomationIDString(t)
	runID := newRunIDString(t)
	mustExec(t, db, `INSERT INTO automation_definition_revisions VALUES (?, 1, '{"name":"old"}')`, id)
	mustExec(t, db, `INSERT INTO automations VALUES (?, 1, ?, ?)`, id, migrationTimestamp, migrationTimestamp)
	mustExec(
		t,
		db,
		`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, definition_revision, run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json) VALUES (?, ?, 'Old', 'run', 1, ?, 1, 'manual', 'running', ?, '[]', '{}')`,
		runID,
		id,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json) VALUES (?, ?, 'Old', 'skip', 1, ?, '[]', 'conditions_false', 'manual', '{}')`,
		newSkipIDString(t),
		id,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, 0, 'command', 'not_attempted')`,
		runID,
	)
	mustExec(t, db, `INSERT INTO automation_run_branch_decisions VALUES (?, 'branch', 0, '{}')`, runID)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at) VALUES (?, 'wait', 0, 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at) VALUES (?, 'notify', 0, 'linked-attempt', 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status) VALUES (?, 'function', 0, 'linked-invocation', ?, 'running')`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		db,
		`INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 1, 'held', 1, 'idle')`,
		id,
	)
	mustExec(t, db, `INSERT INTO automation_fact_receipts VALUES (?, ?, 'run', ?)`, newFactIDString(t), id, runID)
	mustExec(t, db, `INSERT INTO automation_schedule_watermarks VALUES ('global', ?)`, migrationTimestamp)
	for _, table := range []string{"automation_definition_revisions", "automations", "automation_history", "automation_holds", "automation_fact_receipts", "automation_schedule_watermarks", "automation_run_steps", "automation_run_branch_decisions", "automation_run_delays", "automation_run_notifications", "automation_run_functions"} {
		mustExec(
			t,
			db,
			`CREATE TEMP TABLE expected_`+table+` AS SELECT * FROM `+table, //nolint:unqueryvet // Migration rollback compares every legacy column verbatim.
		)
	}
	mustExec(
		t,
		db,
		`CREATE TEMP TABLE expected_automation_schema AS SELECT type, name, tbl_name, sql FROM sqlite_schema WHERE (tbl_name LIKE 'automation%' OR name LIKE 'automation%') AND name NOT LIKE 'sqlite_%'`,
	)
	mustExec(t, db, `CREATE TABLE automation_step_executions (collision TEXT)`)
	if _, err := provider.UpTo(t.Context(), 22); err == nil {
		t.Fatal("migration succeeded despite table-name collision")
	}
	for _, table := range []string{"automation_definition_revisions", "automations", "automation_history", "automation_holds", "automation_fact_receipts", "automation_schedule_watermarks", "automation_run_steps", "automation_run_branch_decisions", "automation_run_delays", "automation_run_notifications", "automation_run_functions"} {
		assertMigrationRowsUnchanged(t, db, table)
	}
	var version int64
	if err := db.QueryRow(`SELECT max(version_id) FROM goose_db_version WHERE is_applied`).
		Scan(&version); err != nil ||
		version != 21 {
		t.Fatalf("version=%d err=%v", version, err)
	}
	mustExec(t, db, `DROP TABLE automation_step_executions`)
	mustExec(
		t,
		db,
		`CREATE TEMP TABLE automation_schema AS SELECT type, name, tbl_name, sql FROM sqlite_schema WHERE (tbl_name LIKE 'automation%' OR name LIKE 'automation%') AND name NOT LIKE 'sqlite_%'`,
	)
	assertMigrationRowsUnchanged(t, db, "automation_schema")
	if _, err := provider.UpTo(t.Context(), 22); err != nil {
		t.Fatalf("retry migration: %v", err)
	}
}

func TestUnifiedStepExecutionSQLiteConstraints(t *testing.T) {
	t.Parallel()
	db, provider := revisionMigrationDatabase(t)
	if _, err := provider.UpTo(t.Context(), 22); err != nil {
		t.Fatal(err)
	}
	runs := []string{newRunIDString(t), newRunIDString(t)}
	for _, id := range runs {
		automationID := newAutomationIDString(t)
		mustExec(
			t,
			db,
			`INSERT INTO automation_definition_revisions VALUES (?, 1, '{"name":"constraint","triggers":[]}')`,
			automationID,
		)
		mustExec(
			t,
			db,
			`INSERT INTO automations VALUES (?, 1, ?, ?)`,
			automationID,
			migrationTimestamp,
			migrationTimestamp,
		)
		mustExec(
			t,
			db,
			`INSERT INTO automation_history (id, automation_id, automation_name, kind, revision, recorded_at, definition_revision, run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json) VALUES (?, ?, 'Constraint', 'run', 1, ?, 1, 'manual', 'running', ?, '[]', '{}')`,
			id,
			automationID,
			migrationTimestamp,
			migrationTimestamp,
		)
	}
	insert := func(run, step string, seq int, kind, phase, completed, evidence string) error {
		var completion any
		if completed != "" {
			completion = completed
		}
		_, err := db.Exec(
			`INSERT INTO automation_step_executions VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			run,
			step,
			seq,
			kind,
			phase,
			migrationTimestamp,
			completion,
			evidence,
		)
		return err
	}
	if err := insert(
		runs[0],
		"branch",
		0,
		"if",
		"succeeded",
		migrationTimestamp,
		`{"outcome":"no_match"}`,
	); err != nil {
		t.Fatalf("legal terminal branch: %v", err)
	}
	if err := insert(
		runs[1],
		"obsolete",
		0,
		"if",
		"decided",
		migrationTimestamp,
		`{"outcome":"no_match"}`,
	); err == nil {
		t.Fatal("obsolete branch phase accepted")
	}
	if err := insert(runs[0], "delay", 1, "delay", "succeeded", migrationTimestamp, `{}`); err != nil {
		t.Fatalf("legal delay with timestamp regression: %v", err)
	}
	if err := insert(
		runs[0],
		"branch",
		2,
		"if",
		"succeeded",
		migrationTimestamp,
		`{"outcome":"no_match"}`,
	); err == nil {
		t.Fatal("duplicate (run, step) accepted")
	}
	if err := insert(runs[0], "other", 0, "if", "succeeded", migrationTimestamp, `{"outcome":"no_match"}`); err == nil {
		t.Fatal("duplicate (run, sequence) accepted")
	}
	if err := insert(runs[0], "active-delay", 2, "delay", "running", "", `{}`); err != nil {
		t.Fatalf("first running execution: %v", err)
	}
	if err := insert(
		runs[0],
		"running",
		3,
		"command",
		"running",
		"",
		`{"command_id":"cmd_018f47c2-7b3d-7abc-8abc-123456789abc","correlation_id":"cor_018f47c2-7b3d-7abc-8abc-123456789abc"}`,
	); err == nil {
		t.Fatal("second running execution accepted")
	}
	if err := insert(runs[0], "bad-phase", 4, "delay", "failed", migrationTimestamp, `{}`); err == nil {
		t.Fatal("invalid kind/phase accepted")
	}
	if err := insert(
		runs[1],
		"bad-notification",
		0,
		"notification",
		"succeeded",
		migrationTimestamp,
		`{"attempt_id":"bad"}`,
	); err == nil {
		t.Fatal("invalid attempt ID accepted")
	}
	if err := insert(
		runs[0],
		"notify",
		5,
		"notification",
		"succeeded",
		migrationTimestamp,
		`{"attempt_id":"ntf_018f47c2-7b3d-7abc-8abc-123456789abc","outcome":"accepted"}`,
	); err != nil {
		t.Fatalf("valid notification: %v", err)
	}
	if err := insert(
		runs[1],
		"notify",
		0,
		"notification",
		"succeeded",
		migrationTimestamp,
		`{"attempt_id":"ntf_018f47c2-7b3d-7abc-8abc-123456789abc","outcome":"accepted"}`,
	); err == nil {
		t.Fatal("duplicate notification attempt accepted")
	}
	if err := insert(
		runs[0],
		"function",
		6,
		"function",
		"succeeded",
		migrationTimestamp,
		`{"invocation_id":"afi_018f47c2-7b3d-7abc-8abc-123456789abc","catalog_revision":"afc_018f47c2-7b3d-7abc-8abc-123456789abc","invocation_outcome":"succeeded"}`,
	); err != nil {
		t.Fatalf("valid function: %v", err)
	}
	if err := insert(
		runs[1],
		"function",
		1,
		"function",
		"succeeded",
		migrationTimestamp,
		`{"invocation_id":"afi_018f47c2-7b3d-7abc-8abc-123456789abc","catalog_revision":"afc_018f47c2-7b3d-7abc-8abc-123456789abc","invocation_outcome":"succeeded"}`,
	); err == nil {
		t.Fatal("duplicate function invocation accepted")
	}
	if err := insert(newRunIDString(t), "orphan", 0, "delay", "succeeded", migrationTimestamp, `{}`); err == nil {
		t.Fatal("unknown parent accepted")
	}
}

func assertTableAbsent(t *testing.T, db *sql.DB, table string) {
	t.Helper()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE type = 'table' AND name = ?`, table).
		Scan(&count); err != nil ||
		count != 0 {
		t.Fatalf("table %s remains (count=%d): %v", table, count, err)
	}
}
