//go:build integration

package sqlite_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	"github.com/mholtzscher/hearth/internal/modules/automations"
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

func seedRevisionMigrationRun(t *testing.T, database *sql.DB, id, raw, decision string) string {
	t.Helper()
	runID := newRunIDString(t)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at, run_completed_at,
		run_matched_trigger_ids_json, condition_decision_json)
		VALUES (?, ?, 'Office light', 'run', 7, ?, ?, 'manual', 'succeeded', ?, ?, '[]', ?)`,
		runID, id, migrationTimestamp, raw, migrationTimestamp, migrationTimestamp, decision)
	mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
		VALUES (?, 0, 'light_on', 'not_attempted')`, runID)
	return runID
}

func TestDefinitionRevisionMigrationSharesArtifactsAndPreservesHistory(t *testing.T) {
	t.Parallel()
	database, provider := revisionMigrationDatabase(t)
	id := newAutomationIDString(t)
	definition := validDomainDefinition(t)
	condition := conditionLeaf("admission", newEntityID(t), automations.ComparisonEqual, "true")
	definition.Conditions = condition
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := automations.EncodeConditionDecision(automations.BypassedDecision(*condition))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`INSERT INTO automations VALUES (?, 7, ?, ?, ?)`,
		id,
		string(raw),
		migrationTimestamp,
		migrationTimestamp,
	)
	first := seedRevisionMigrationRun(t, database, id, string(raw), string(decision))
	second := seedRevisionMigrationRun(t, database, id, string(raw), string(decision))
	// A deleted Automation's Run must also acquire an independent retained artifact.
	deletedID := newAutomationIDString(t)
	deleted := seedRevisionMigrationRun(t, database, deletedID, string(raw), string(decision))
	// Legacy Skips need no full definition and retain their exact inline bytes.
	skipID, skipAutomation := newSkipIDString(t), newAutomationIDString(t)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at, skip_source,
		skip_reason, skip_matched_triggers_json, condition_decision_json)
		VALUES (?, ?, 'Deleted Skip only', 'skip', 42, ?, 'manual', 'conditions_unknown', '[]', ?)`,
		skipID, skipAutomation, migrationTimestamp, string(decision))
	// All child families must survive even though dropping the parent would cascade.
	mustExec(t, database, `INSERT INTO automation_holds
		(automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 7, 'held', 11, 'idle')`, id)
	mustExec(t, database, `INSERT INTO automation_run_branch_decisions VALUES (?, 'branch', 0, '{}')`, second)
	mustExec(t, database, `INSERT INTO automation_run_delays
		(run_id, step_id, position, status, started_at, completed_at) VALUES (?, 'wait', 0, 'completed', ?, ?)`, second, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_notifications
		(run_id, step_id, position, attempt_id, status, started_at, completed_at, outcome)
		VALUES (?, 'notify', 0, 'retained-notification', 'accepted', ?, ?, 'accepted')`, second, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_functions
		(run_id, step_id, position, invocation_id, started_at, status, completed_at, catalog_revision, invocation_outcome)
		VALUES (?, 'function', 0, 'retained-invocation', ?, 'succeeded', ?, 'retained-catalog', 'succeeded')`, second, migrationTimestamp, migrationTimestamp)
	tables := snapshotAutomationCascadeRows(t, database)
	if _, err = provider.UpTo(t.Context(), 21); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		if table != "automations" && table != "automation_history" {
			assertMigrationRowsUnchanged(t, database, table)
		}
	}
	if count := rowCount(t, database, "automation_definition_revisions"); count != 2 {
		t.Fatalf("artifacts = %d, want current revision and deleted Run revision only", count)
	}
	repository := newAutomationRepository(t, database)
	for _, pair := range [][2]string{{id, first}, {deletedID, deleted}} {
		run := historyEntry(t, repository, automations.AutomationID(pair[0]), pair[1]).(automations.Run)
		got, encodeErr := automations.EncodeDefinition(run.Snapshot)
		if encodeErr != nil || string(got) != string(raw) || run.Revision != 7 ||
			run.ConditionDecision.DecisionMode() != automations.ConditionDecisionBypassed {
			t.Fatalf("retained Run = %#v, snapshot %s, error %v", run, got, encodeErr)
		}
	}
	var retainedSkip, retainedDecision string
	if err = database.QueryRow(`SELECT condition_decision_json FROM automation_history WHERE id = ?`, skipID).
		Scan(&retainedSkip); err != nil {
		t.Fatal(err)
	}
	if retainedSkip != string(decision) {
		t.Fatalf("legacy Skip changed: %s", retainedSkip)
	}
	if err = database.QueryRow(`SELECT condition_decision_json FROM automation_history WHERE id = ?`, first).
		Scan(&retainedDecision); err != nil {
		t.Fatal(err)
	}
	if retainedDecision != `{"mode":"bypassed","bypass_requested":true}` {
		t.Fatalf("Run repeats configuration: %s", retainedDecision)
	}
	assertScheduleMigrationIntegrity(t, database)
	if _, err = provider.Down(t.Context()); err == nil || !strings.Contains(err.Error(), "pre-upgrade backup") {
		t.Fatalf("unsafe Down = %v", err)
	}
	if count := rowCount(t, database, "automation_history"); count != 4 {
		t.Fatalf("Down changed history: %d", count)
	}
}

//nolint:gocognit // Each fault must exercise the same complete abort-and-preservation sequence.
func TestDefinitionRevisionMigrationRejectsConflictsAtomically(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"current disagreement", "Run disagreement", "missing Conditions", "mismatched Conditions", "extra Condition configuration", "missing snapshot", "large number disagreement", "duplicate definition members", "duplicate Condition members"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			database, provider := revisionMigrationDatabase(t)
			id := newAutomationIDString(t)
			definition := validDomainDefinition(t)
			definition.Conditions = conditionLeaf("admission", newEntityID(t), automations.ComparisonEqual, "true")
			raw, err := automations.EncodeDefinition(definition)
			if err != nil {
				t.Fatal(err)
			}
			decision, err := automations.EncodeConditionDecision(automations.BypassedDecision(*definition.Conditions))
			if err != nil {
				t.Fatal(err)
			}
			seedRevisionMigrationRun(t, database, id, string(raw), string(decision))
			switch fault {
			case "current disagreement":
				mustExec(
					t,
					database,
					`INSERT INTO automations VALUES (?, 7, json_set(?, '$.name', 'Conflicting'), ?, ?)`,
					id,
					string(raw),
					migrationTimestamp,
					migrationTimestamp,
				)
			case "Run disagreement":
				seedRevisionMigrationRun(
					t,
					database,
					id,
					strings.Replace(string(raw), "Office light", "Other name", 1),
					string(decision),
				)
			case "duplicate definition members":
				mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = replace(run_snapshot_json,
					'"value":true', '"value":1,"value":2')`)
				seedRevisionMigrationRun(
					t,
					database,
					id,
					strings.Replace(string(raw), `"value":true`, `"value":1,"value":3`, 1),
					string(decision),
				)
			case "duplicate Condition members":
				if !strings.Contains(string(decision), `"operand":true`) {
					t.Fatalf("fixture has no operand to duplicate: %s", decision)
				}
				mustExec(
					t,
					database,
					`UPDATE automation_history SET condition_decision_json = replace(condition_decision_json,
					'"operand":true', '"operand":true,"operand":false')`,
				)
			case "large number disagreement":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET run_snapshot_json = json_set(run_snapshot_json, '$.steps[0].parameters.value', json('9223372036854775808'))`,
				)
				seedRevisionMigrationRun(
					t,
					database,
					id,
					strings.Replace(string(raw), `"value":true`, `"value":9223372036854775809`, 1),
					string(decision),
				)
			case "missing Conditions":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET condition_decision_json = json_remove(condition_decision_json, '$.snapshot')`,
				)
			case "mismatched Conditions":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET condition_decision_json = json_set(condition_decision_json, '$.snapshot.id', 'different')`,
				)
			case "extra Condition configuration":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET condition_decision_json = json_set(condition_decision_json, '$.snapshot.extra', 1)`,
				)
			case "missing snapshot":
				mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
				mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = NULL`)
				mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
			}
			before := rowCount(t, database, "automation_history")
			//nolint:unqueryvet // Preserve every old history/child column on an aborted migration.
			mustExec(t, database, `CREATE TEMP TABLE expected_automation_history AS SELECT * FROM automation_history`)
			//nolint:unqueryvet // Preserve child evidence as well as the failed parent audit.
			mustExec(
				t,
				database,
				`CREATE TEMP TABLE expected_automation_run_steps AS SELECT * FROM automation_run_steps`,
			)
			if _, err = provider.UpTo(
				t.Context(),
				21,
			); err == nil ||
				!strings.Contains(err.Error(), "Automation revision migration:") {
				t.Fatalf("migration accepted %s: %v", fault, err)
			}
			var artifacts int
			if err = database.QueryRow(`SELECT count(*) FROM sqlite_schema WHERE name = 'automation_definition_revisions'`).
				Scan(&artifacts); err != nil ||
				artifacts != 0 {
				t.Fatalf("partial artifact schema = %d, %v", artifacts, err)
			}
			if count := rowCount(t, database, "automation_history"); count != before {
				t.Fatalf("partial history: %d want %d", count, before)
			}
			assertMigrationRowsUnchanged(t, database, "automation_history")
			assertMigrationRowsUnchanged(t, database, "automation_run_steps")
			var persisted sql.NullString
			if err = database.QueryRow(`SELECT run_snapshot_json FROM automation_history LIMIT 1`).
				Scan(&persisted); err != nil &&
				fault != "missing snapshot" {
				t.Fatal(err)
			}
		})
	}
}

func TestDefinitionRevisionMigrationComparesConfigurationLosslessly(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{"member order", "explicit null", "array order"} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			database, provider := revisionMigrationDatabase(t)
			id := newAutomationIDString(t)
			raw, err := automations.EncodeDefinition(validDomainDefinition(t))
			if err != nil {
				t.Fatal(err)
			}
			decision := `{"mode":"not_configured","bypass_requested":false}`
			seedRevisionMigrationRun(t, database, id, string(raw), decision)
			other := seedRevisionMigrationRun(t, database, id, string(raw), decision)
			switch mutation {
			case "member order":
				mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = json_object(
					'steps', json_extract(run_snapshot_json, '$.steps'),
					'triggers', json_extract(run_snapshot_json, '$.triggers'),
					'enabled', json('true'), 'name', json_extract(run_snapshot_json, '$.name')) WHERE id = ?`, other)
			case "explicit null":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET run_snapshot_json = json_set(run_snapshot_json, '$.steps[0].parameters.extra', NULL) WHERE id = ?`,
					other,
				)
			case "array order":
				mustExec(
					t,
					database,
					`UPDATE automation_history SET run_snapshot_json = json_set(run_snapshot_json, '$.steps[0].parameters.value', json('[1,2]'))`,
				)
				mustExec(
					t,
					database,
					`UPDATE automation_history SET run_snapshot_json = json_set(run_snapshot_json, '$.steps[0].parameters.value', json('[2,1]')) WHERE id = ?`,
					other,
				)
			}
			_, err = provider.UpTo(t.Context(), 21)
			if mutation == "member order" {
				if err != nil {
					t.Fatal(err)
				}
				if count := rowCount(t, database, "automation_definition_revisions"); count != 1 {
					t.Fatalf("equivalent documents stored %d artifacts", count)
				}
			} else if err == nil || !strings.Contains(err.Error(), "conflicting definitions") {
				t.Fatalf("lossy comparison for %s: %v", mutation, err)
			}
		})
	}
}
