//go:build integration

package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func revisionMigrationDatabase(t *testing.T) (*sql.DB, *goose.Provider) {
	t.Helper()
	database, err := platformdb.Open(t.Context(), filepath.Join(t.TempDir(), "revisions.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 20); err != nil {
		t.Fatal(err)
	}
	return database, provider
}

// seedAutomationResetRows populates every pre-cutover Automation table. Opaque
// legacy documents deliberately need no decoder, normalization, or conflict audit.
func seedAutomationResetRows(t *testing.T, database *sql.DB) []string {
	t.Helper()
	id, runID := newAutomationIDString(t), newRunIDString(t)
	mustExec(
		t,
		database,
		`INSERT INTO automations VALUES (?, 7, '{"legacy":"definition"}', ?, ?)`,
		id,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at, run_snapshot_json,
		run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json)
		VALUES (?, ?, 'Discarded Run', 'run', 7, ?, '{"different":"snapshot"}', 'manual', 'running', ?, '[]', '{}')`, runID, id, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, insertHistorySkipSQL, newSkipIDString(t), id, migrationTimestamp,
		newFactIDString(t), string(newEntityID(t)), newObservationIDString(t), "true", migrationTimestamp,
		matchedTriggerJSON(t), `{"mode":"not_configured","bypass_requested":false}`)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_steps (run_id, position, step_id, status) VALUES (?, 0, 'command', 'not_attempted')`,
		runID,
	)
	mustExec(t, database, `INSERT INTO automation_run_branch_decisions VALUES (?, 'branch', 0, '{}')`, runID)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_delays (run_id, step_id, position, status, started_at) VALUES (?, 'wait', 0, 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_notifications (run_id, step_id, position, attempt_id, status, started_at) VALUES (?, 'notify', 0, 'linked-attempt', 'running', ?)`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO automation_run_functions (run_id, step_id, position, invocation_id, started_at, status) VALUES (?, 'function', 0, 'linked-invocation', ?, 'running')`,
		runID,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO automation_holds (automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 7, 'held', 1, 'idle')`,
		id,
	)
	mustExec(t, database, `INSERT INTO automation_fact_receipts VALUES (?, ?, 'run', ?)`, newFactIDString(t), id, runID)
	mustExec(t, database, `INSERT INTO automation_schedule_watermarks VALUES ('global', ?)`, migrationTimestamp)
	return snapshotAutomationCascadeRows(t, database)
}

func seedOtherModuleResetRows(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExec(
		t,
		database,
		`INSERT INTO devices VALUES ('dev_retained', 'light', 'Retained Device', ?, ?, NULL)`,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(t, database, `INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_retained', 'dev_retained', 'Retained Entity', 'hearth.power/v1', '{}', ?, ?)`, migrationTimestamp, migrationTimestamp)
	mustExec(
		t,
		database,
		`INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at, completed_at)
		VALUES ('cmd_retained', 'ent_retained', 'retained', 'set', '{"value":true}', 'cor_retained', 'dispatched', ?, ?, ?)`,
		migrationTimestamp,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO adapter_instances (adapter_id, health_status, health_reason_code, health_source, health_since, health_evidence_at)
		VALUES ('retained', 'unknown', 'core.starting', 'core', ?, ?)`,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO notification_attempts (id, destination_id, destination_name, adapter_id, local_key, runtime_id, title, message, started_at, deadline)
		VALUES ('linked-attempt', 'retained-destination', 'Retained', 'retained', 'alerts', 'retained-runtime', 'Title', 'Message', ?, ?)`,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(
		t,
		database,
		`INSERT INTO adapter_function_invocations (id, adapter_id, runtime_id, catalog_revision, function_key, descriptor_json, arguments_json, started_at, deadline, status)
		VALUES ('linked-invocation', 'retained', 'retained-runtime', 'retained-catalog', 'inspect', '{}', '{}', ?, ?, 'running')`,
		migrationTimestamp,
		migrationTimestamp,
	)
	mustExec(t, database, `INSERT INTO agent_conversations VALUES ('aconv_retained', ?)`, migrationTimestamp)
}
