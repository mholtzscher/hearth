//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// migrationTimestamp is a fixed-width UTC stamp accepted by every automation
// table's TEXT columns.
const migrationTimestamp = "2026-09-01T00:00:00.000000000Z"

func literal(raw string) automations.ValueBinding {
	return automations.ValueBinding{Body: automations.LiteralValue{Value: json.RawMessage(raw)}}
}

// openAutomationDatabase opens one migrated Core database in a temporary
// directory, so persistence tests exercise the real automation schema.
func openAutomationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
	return database
}

// newAutomationRepository builds the SQLite Automation repository with
// production-default dependencies.
func newAutomationRepository(t *testing.T, database *sql.DB) *automationssqlite.AutomationRepository {
	t.Helper()
	return automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
}

// validDomainDefinition builds one strict definition fixture that normalization
// and persistence both accept.
func validDomainDefinition(t *testing.T) automations.Definition {
	t.Helper()
	return automations.Definition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.Trigger{{ID: "occupied_and_warm", Body: automations.ObservationTrigger{
			EntityID:     newEntityID(t),
			Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
			Comparisons: []automations.ObservationComparison{{
				Pointer:  "/temperature",
				Operator: automations.ComparisonGreaterThan,
				Operand:  json.RawMessage("20"),
			}},
		}}},
		Steps: []automations.Step{
			{
				ID: "light_on",
				Body: automations.CommandStep{
					EntityID:      newEntityID(t),
					OperationName: devices.OperationNameSet,
					Parameters:    literal(`{"value":true}`),
				},
			},
		},
	}
}

func newAutomationIDString(t *testing.T) string {
	t.Helper()
	id, err := automations.NewAutomationID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newRunIDString(t *testing.T) string {
	t.Helper()
	id, err := automations.NewRunID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newSkipIDString(t *testing.T) string {
	t.Helper()
	id, err := automations.NewSkipID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newFactIDString(t *testing.T) string {
	t.Helper()
	id, err := devices.NewDeviceFactID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newObservationIDString(t *testing.T) string {
	t.Helper()
	id, err := devices.NewObservationID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newCommandIDString(t *testing.T) string {
	t.Helper()
	id, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newCorrelationIDString(t *testing.T) string {
	t.Helper()
	id, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

// insertHistoryRunSQL inserts one valid Run with the full column set, so a test
// can control Run status and outcome independently of the SQL constraints. The
// literal Condition decision is the explicit envelope every row now requires.
const insertHistoryRunSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    definition_revision, run_source, run_status, run_failure_code, run_started_at,
    run_completed_at, run_matched_trigger_ids_json, condition_decision_json
) VALUES (?, ?, 'Office light', 'run', 1, '2026-09-01T00:00:00.000000000Z', 1, 'manual', ?, ?,
    '2026-09-01T00:00:00.000000000Z', ?, ?,
    '{"mode":"not_configured","bypass_requested":false}')`

// Historical fixtures deliberately use the pre-21 inline snapshot contract,
// never the current repository against an old schema.
const insertHistoricalHistoryRunSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    run_snapshot_json, run_source, run_status, run_failure_code, run_started_at,
    run_completed_at, run_matched_trigger_ids_json, condition_decision_json
) VALUES (?, ?, 'Office light', 'run', 1, '2026-09-01T00:00:00.000000000Z', '{}', 'manual', ?, ?,
    '2026-09-01T00:00:00.000000000Z', ?, ?,
    '{"mode":"not_configured","bypass_requested":false}')`

func insertHistoricalAutomation(t *testing.T, database *sql.DB, definition automations.Definition) automations.Record {
	t.Helper()
	raw, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	id := automations.AutomationID(newAutomationIDString(t))
	mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?)`, string(id), string(raw), migrationTimestamp, migrationTimestamp)
	return automations.Record{ID: id, Revision: 1, Definition: definition}
}

func insertHistoricalStorageRun(
	t *testing.T,
	database *sql.DB,
	definition automations.Definition,
	at time.Time,
) (automations.Record, automations.Run) {
	t.Helper()
	record := insertHistoricalAutomation(t, database, definition)
	id := automations.RunID(newRunIDString(t))
	mustExec(t, database, insertHistoricalHistoryRunSQL, string(id), string(record.ID), "running", nil, nil, "[]")
	mustExec(
		t,
		database,
		`UPDATE automation_history SET automation_name = ?, recorded_at = ?, run_started_at = ?,
		run_snapshot_json = (SELECT definition_json FROM automations WHERE id = ?) WHERE id = ?`,
		definition.Name,
		at.Format("2006-01-02T15:04:05.000000000Z"),
		at.Format("2006-01-02T15:04:05.000000000Z"),
		string(record.ID),
		string(id),
	)
	for position, step := range automations.CommandLeaves(definition.Steps) {
		mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
			VALUES (?, ?, ?, 'not_attempted')`, string(id), position, string(step.ID))
	}
	return record, automations.Run{ID: id, AutomationID: record.ID}
}

func insertDefinitionRevision(t *testing.T, database *sql.DB, id string, revision int, raw string) {
	t.Helper()
	mustExec(t, database, `INSERT INTO automation_definition_revisions (automation_id, revision, definition_json)
		VALUES (?, ?, ?)`, id, revision, raw)
}

// insertHistoricalScheduledOutcome supplies retained v9-v20 provenance without
// invoking today's scheduler queries on the old schema.
func insertHistoricalScheduledOutcome(
	t *testing.T,
	database *sql.DB,
	record automations.Record,
	kind string,
	at time.Time,
) {
	t.Helper()
	stamp := at.Format("2006-01-02T15:04:05.000000000Z")
	matched, err := automations.EncodeMatchedTriggers(record.Definition.Triggers)
	if err != nil {
		t.Fatal(err)
	}
	var id string
	if kind == "run" {
		id = newRunIDString(t)
		raw, encodeErr := automations.EncodeDefinition(record.Definition)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		triggerIDs, encodeErr := json.Marshal([]string{string(record.Definition.Triggers[0].ID)})
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		mustExec(t, database, `INSERT INTO automation_history
			(id, automation_id, automation_name, kind, revision, recorded_at, run_snapshot_json,
			run_source, run_status, run_started_at, run_matched_trigger_ids_json, condition_decision_json)
			VALUES (?, ?, ?, 'run', 1, ?, ?, 'schedule', 'running', ?, ?,
			'{"mode":"not_configured","bypass_requested":false}')`,
			id, string(record.ID), record.Definition.Name, stamp, string(raw), stamp, string(triggerIDs))
		for position, step := range automations.CommandLeaves(record.Definition.Steps) {
			mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
				VALUES (?, ?, ?, 'not_attempted')`, id, position, string(step.ID))
		}
	} else {
		id = newSkipIDString(t)
		mustExec(t, database, `INSERT INTO automation_history
			(id, automation_id, automation_name, kind, revision, recorded_at, skip_matched_triggers_json,
			skip_source, skip_reason, condition_decision_json)
			VALUES (?, ?, ?, 'skip', 1, ?, ?, 'schedule', 'automation_busy',
			'{"mode":"not_configured","bypass_requested":false}')`,
			id, string(record.ID), record.Definition.Name, stamp, string(matched))
	}
	mustExec(t, database, `INSERT INTO automation_schedule_watermarks (id, highwater_at) VALUES ('global', ?)
		ON CONFLICT(id) DO UPDATE SET highwater_at = excluded.highwater_at`, stamp)
}

// Corruption tests explicitly bypass only immutability to damage a stored
// artifact. JSON, size and foreign-key constraints remain enforced.
func allowDefinitionRevisionCorruption(t *testing.T, database *sql.DB) {
	t.Helper()
	mustExec(t, database, `DROP TRIGGER automation_definition_revision_immutable`)
}

// insertHistorySkipSQL inserts one valid device-fact Skip. The matched-Trigger
// snapshot and the Condition decision are supplied so provenance tests can
// exercise every accepted and rejected combination.
const insertHistorySkipSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    skip_matched_triggers_json, skip_reason, skip_source, condition_decision_json
) VALUES (?, ?, 'Office light', 'skip', 1, ?, ?, 'observation', ?, 'applied', ?, ?, ?, ?, 'automation_busy',
    'device_fact', ?)`

// matchedTriggerJSON renders one valid matched-Trigger snapshot so a stored Skip
// decodes instead of failing trigger validation.
func matchedTriggerJSON(t *testing.T) string {
	t.Helper()
	raw, err := automations.EncodeMatchedTriggers(
		[]automations.Trigger{{ID: "occupied_and_warm", Body: automations.ObservationTrigger{
			EntityID:     newEntityID(t),
			Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
		}}},
	)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func mustExec(t *testing.T, database *sql.DB, statement string, args ...any) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), statement, args...); err != nil {
		t.Fatalf("exec %s: %v", statement, err)
	}
}

func assertStoredAutomationJSON(t *testing.T, database *sql.DB, id automations.AutomationID) {
	t.Helper()
	var stored string
	if err := database.QueryRow(
		`SELECT definition_json FROM automation_definitions WHERE id = ?`, string(id),
	).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	var document map[string]any
	if err := json.Unmarshal([]byte(stored), &document); err != nil {
		t.Fatalf("stored definition is not JSON: %v", err)
	}
	for _, required := range []string{"name", "enabled", "triggers", "steps"} {
		if _, found := document[required]; !found {
			t.Fatalf("stored definition is missing %q: %s", required, stored)
		}
	}
}
