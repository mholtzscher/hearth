//go:build integration

package sqlite_test

import (
	"database/sql"
	"strings"
	"testing"
)

// assertHistoricalCommandMetadata reads the v20 schema directly. Current
// repositories use v21 storage, whose migration intentionally resets Automations.
func assertHistoricalCommandMetadata(t *testing.T, database *sql.DB, id, runID string, steps []string) {
	t.Helper()
	var revision, enabled int
	var created, updated string
	if err := database.QueryRow(`SELECT revision, json_extract(definition_json, '$.enabled'), created_at, updated_at
		FROM automations WHERE id = ?`, id).Scan(&revision, &enabled, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if revision != 4 || enabled != 0 || created != migrationTimestamp || updated != created {
		t.Fatalf("upgraded definition metadata = %d, %d, %q, %q", revision, enabled, created, updated)
	}
	var status string
	var count int
	if err := database.QueryRow(`SELECT run_status FROM automation_history WHERE id = ?`, runID).
		Scan(&status); err != nil ||
		status != "succeeded" {
		t.Fatalf("upgraded Run status = %q, %v", status, err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM automation_run_steps WHERE run_id = ?`, runID).
		Scan(&count); err != nil ||
		count != len(steps) {
		t.Fatalf("upgraded Command count = %d, %v", count, err)
	}
	for position, want := range steps {
		var stepID string
		if err := database.QueryRow(`SELECT step_id, status FROM automation_run_steps
			WHERE run_id = ? AND position = ?`, runID, position).
			Scan(&stepID, &status); err != nil || stepID != want ||
			status != "not_attempted" {
			t.Fatalf("upgraded Command at %d = %q, %q, %v", position, stepID, status, err)
		}
	}
}

// snapshotAutomationCascadeRows discovers preservation obligations from the real
// pre-migration FK graph, not a second list of Step kinds. Every discovered table
// must contain a fixture row so omitted preservation cannot pass vacuously.
func snapshotAutomationCascadeRows(t *testing.T, database *sql.DB) []string {
	t.Helper()
	rows, err := database.Query(`WITH RECURSIVE preserved(name) AS (
		VALUES ('automations'), ('automation_history')
		UNION
		SELECT schema.name FROM sqlite_schema AS schema
		JOIN pragma_foreign_key_list(schema.name) AS fk
		JOIN preserved AS parent ON fk."table" = parent.name
		WHERE schema.type = 'table' AND fk.on_delete = 'CASCADE'
	) SELECT name FROM preserved ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var tables []string
	for rows.Next() {
		var table string
		if err = rows.Scan(&table); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, table)
	}
	if err = rows.Err(); err != nil {
		t.Fatal(err)
	}
	if err = rows.Close(); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		quoted := `"` + strings.ReplaceAll(table, `"`, `""`) + `"`
		var count int
		if err = database.QueryRow(`SELECT count(*) FROM ` + quoted).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 0 {
			t.Fatalf("preservation fixture must seed cascading table %s", table)
		}
		//nolint:unqueryvet // Exact whole-row comparison protects every retained column.
		mustExec(
			t,
			database,
			`CREATE TEMP TABLE "expected_`+strings.ReplaceAll(table, `"`, `""`)+`" AS SELECT * FROM `+quoted,
		)
	}
	return tables
}
