package sqlite_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// migrationTimestamp is a fixed-width UTC stamp accepted by every automation
// table's TEXT columns.
const migrationTimestamp = "2026-09-01T00:00:00.000000000Z"

// openAutomationDatabase opens one migrated Core database in a temporary
// directory, so persistence tests exercise the real automation schema.
func openAutomationDatabase(t *testing.T) *sql.DB {
	t.Helper()
	database, err := platformdb.Open(context.Background(), filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(context.Background(), database); err != nil {
		t.Fatal(err)
	}
	return database
}

// newAutomationRepository builds the SQLite Automation repository with
// production-default dependencies.
func newAutomationRepository(t *testing.T, database *sql.DB) *automationssqlite.AutomationRepository {
	t.Helper()
	return automationssqlite.NewAutomationRepository(database, automations.AutomationDependencies{})
}

// validDomainDefinition builds one strict definition fixture that normalization
// and persistence both accept.
func validDomainDefinition(t *testing.T) automations.AutomationDefinition {
	t.Helper()
	return automations.AutomationDefinition{
		Name:    "Office light",
		Enabled: true,
		Triggers: []automations.AutomationTrigger{{
			ID:   "occupied_and_warm",
			Kind: automations.TriggerKindObservation,
			Observation: &automations.ObservationTrigger{
				EntityID:     newEntityID(t),
				Dispositions: []devices.ObservationDisposition{devices.DispositionApplied},
				Comparisons: []automations.ObservationComparison{{
					Pointer:  "/temperature",
					Operator: automations.ComparisonGreaterThan,
					Operand:  json.RawMessage("20"),
				}},
			},
		}},
		Steps: []automations.AutomationStep{{
			ID:            "light_on",
			EntityID:      newEntityID(t),
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
		}},
	}
}

// newEntityID mints one canonical Entity identity for a fixture.
func newEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	id, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
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
	id, err := automations.NewAutomationRunID()
	if err != nil {
		t.Fatal(err)
	}
	return string(id)
}

func newSkipIDString(t *testing.T) string {
	t.Helper()
	id, err := automations.NewAutomationSkipID()
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

const insertHistoryRunSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    run_snapshot_json, run_source, run_status, run_failure_code, run_started_at,
    run_completed_at, run_matched_trigger_ids_json
) VALUES (?, ?, 'Office light', 'run', 1, '2026-09-01T00:00:00.000000000Z', '{}', 'manual', ?, ?,
    '2026-09-01T00:00:00.000000000Z', ?, ?)`

const insertHistorySkipSQL = `INSERT INTO automation_history (
    id, automation_id, automation_name, kind, revision, recorded_at,
    fact_id, fact_family, fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
    skip_matched_triggers_json, skip_reason
) VALUES (?, ?, 'Office light', 'skip', 1, ?, ?, 'observation', ?, 'applied', ?, ?, ?, '[]', 'automation_busy')`

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
		`SELECT definition_json FROM automations WHERE id = ?`, string(id),
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
