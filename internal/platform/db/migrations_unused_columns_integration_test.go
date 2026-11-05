//go:build integration

package db //nolint:testpackage // Exercises the embedded upgrade against retained rows.

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestUnusedColumnsMigrationPreservesRetainedData(t *testing.T) {
	t.Parallel()
	database, err := Open(t.Context(), filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(t.Context(), 28); err != nil {
		t.Fatal(err)
	}
	seedUnusedColumnsMigrationRows(t, database)
	projections := map[string]string{
		"automation_fact_receipts": "fact_id, automation_id",
		"entity_availability_current": "entity_id, adapter_id, runtime_id, status, reason_code, " +
			"source_observed_at, evidence_at, current_since, health_revision",
		"devices":                       "*",
		"entities":                      "*",
		"adapter_bindings":              "*",
		"adapter_entity_mappings":       "*",
		"adapter_function_catalogs":     "*",
		"entity_availability_effective": "*",
		"health_transitions":            "*",
	}
	for table, columns := range projections {
		if _, err = database.ExecContext(t.Context(),
			`CREATE TEMP TABLE expected_`+table+` AS SELECT `+columns+` FROM `+table,
		); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertUnusedColumnsRetainedData(t, database, projections)
	for table, columns := range map[string][]string{
		"automation_fact_receipts":    {"outcome_kind", "history_id"},
		"entity_availability_current": {"latest_transition_receive_order"},
	} {
		for _, column := range columns {
			var count int
			if err = database.QueryRowContext(t.Context(),
				`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
			).Scan(&count); err != nil || count != 0 {
				t.Fatalf("removed column %s.%s count = %d, %v", table, column, count, err)
			}
		}
	}
	// The identity pair remains unique even though its old history row is absent.
	if _, err = database.ExecContext(t.Context(), `INSERT INTO automation_fact_receipts (fact_id, automation_id)
		SELECT fact_id, automation_id FROM automation_fact_receipts`); err == nil ||
		!strings.Contains(err.Error(), "UNIQUE constraint failed") {
		t.Fatalf("duplicate receipt should fail its identity constraint: %v", err)
	}
	if _, err = provider.Down(t.Context()); err == nil || !strings.Contains(err.Error(), "pre-upgrade backup") {
		t.Fatalf("lossy downgrade should require a backup: %v", err)
	}
	assertUnusedColumnsRetainedData(t, database, projections)
	if version, versionErr := provider.GetDBVersion(t.Context()); versionErr != nil || version != 29 {
		t.Fatalf("version after refused downgrade = %d, %v", version, versionErr)
	}
	if _, err = provider.Up(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertUnusedColumnsRetainedData(t, database, projections)
}

func assertUnusedColumnsRetainedData(t *testing.T, database *sql.DB, projections map[string]string) {
	t.Helper()
	for table, columns := range projections {
		for _, pair := range [][2]string{{table, "expected_" + table}, {"expected_" + table, table}} {
			var differences int
			if err := database.QueryRowContext(t.Context(), `SELECT count(*) FROM (
				SELECT `+columns+` FROM `+pair[0]+` EXCEPT SELECT `+columns+` FROM `+pair[1]+`)`).
				Scan(&differences); err != nil || differences != 0 {
				t.Fatalf("retained %s differences = %d, %v", table, differences, err)
			}
		}
	}
	assertNoForeignKeyViolations(t.Context(), t, database)
}

func seedUnusedColumnsMigrationRows(t *testing.T, database *sql.DB) {
	t.Helper()
	const created = "2026-11-01T12:00:00.000000000Z"
	const updated = "2026-11-02T12:00:00.000000000Z"
	const runtime = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := database.ExecContext(t.Context(), query, args...); err != nil {
			t.Fatal(err)
		}
	}
	exec(`INSERT INTO adapter_instances
		(adapter_id, health_status, health_reason_code, health_source, health_since, health_evidence_at)
		VALUES ('simulator', 'unknown', 'hearth.awaiting_health', 'core', ?, ?)`, created, updated)
	insertLegacyAdapterRuntime(t, database, runtime, created, false)
	exec(`INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_cleanup', 'light', 'Light', ?, ?)`, created, updated)
	exec(`INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_cleanup', 'dev_cleanup', 'Power', 'hearth.power/v1', '{}', ?, ?)`, created, updated)
	exec(`INSERT INTO adapter_bindings (adapter_id, binding_key, device_id, external_device_id, created_at, updated_at)
		VALUES ('simulator', 'light', 'dev_cleanup', 'external-light', ?, ?)`, created, updated)
	exec(`INSERT INTO adapter_entity_mappings
		(adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at)
		VALUES ('simulator', 'light', 'power', 'ent_cleanup', 'external-power', ?, ?)`, created, updated)
	exec(
		`INSERT INTO adapter_function_catalogs (adapter_id, catalog_revision, runtime_id, registered_at, functions_json)
		VALUES ('simulator', 'afc_cleanup', ?, ?, '[]')`,
		runtime,
		created,
	)
	exec(`INSERT INTO health_transitions
		(receive_order, resource_kind, adapter_id, entity_id, runtime_id, status, source, observed_at,
		 source_health_revision, source_health_update_id)
		VALUES (123, 'entity', 'simulator', 'ent_cleanup', ?, 'available', 'entity_report', ?, 3, 'ahu_cleanup')`, runtime, updated)
	exec(`INSERT INTO entity_availability_current
		(entity_id, adapter_id, runtime_id, status, source_observed_at, evidence_at, current_since,
		 latest_transition_receive_order, health_revision)
		VALUES ('ent_cleanup', 'simulator', ?, 'available', ?, ?, ?, 123, 3)`, runtime, created, updated, created)
	exec(`INSERT INTO entity_availability_effective
		(entity_id, status, source, current_since, evidence_at, source_observed_at, latest_transition_receive_order)
		VALUES ('ent_cleanup', 'available', 'entity_report', ?, ?, ?, 123)`, created, updated, created)
	exec(`INSERT INTO automation_fact_receipts (fact_id, automation_id, outcome_kind, history_id) VALUES
		('fct_01890f47-7a6b-7c4d-8e9f-0123456789ab', 'aut_01890f47-7a6b-7c4d-8e9f-0123456789ab',
		 'skip', 'ask_01890f47-7a6b-7c4d-8e9f-0123456789ab'),
		('fct_01890f47-7a6b-7c4d-8e9f-0123456789ac', 'aut_01890f47-7a6b-7c4d-8e9f-0123456789ab',
		 'run', 'arn_01890f47-7a6b-7c4d-8e9f-0123456789ac')`)
}
