package db //nolint:testpackage // Migration tests require the package-private embedded migration set.

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

func TestMigrateEmptySQLiteDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })

	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	if err := Migrate(ctx, database); err != nil {
		t.Fatalf("second migration run: %v", err)
	}

	var foreignKeys int
	if err := database.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Fatalf("foreign_keys = %d, want 1", foreignKeys)
	}
	var journalMode string
	if err := database.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatal(err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", journalMode)
	}

	for _, table := range []string{
		"devices", "entities", "commands",
		"adapter_bindings", "adapter_entity_mappings", "observations", "entity_states",
		"adapter_instances", "adapter_runtimes", "entity_availability_current", "health_transitions",
	} {
		var found string
		err := database.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&found)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	for _, table := range []string{"adapter_instances", "entity_availability_current"} {
		var epochColumns int
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM pragma_table_info(?) WHERE name = 'availability_epoch'", table,
		).Scan(&epochColumns); err != nil {
			t.Fatal(err)
		}
		if epochColumns != 0 {
			t.Fatalf("%s still contains availability_epoch", table)
		}
	}
	var archivedColumns int
	if err := database.QueryRowContext(ctx,
		"SELECT count(*) FROM pragma_table_info('adapter_instances') WHERE name = 'archived_at'",
	).Scan(&archivedColumns); err != nil {
		t.Fatal(err)
	}
	if archivedColumns != 0 {
		t.Fatal("adapter_instances still contains archived_at")
	}

	assertIndexColumns(t, database, "entities_device_id_idx", "device_id,id")
	assertIndexColumns(t, database, "commands_entity_requested_idx", "entity_id,requested_at,id")
	assertIndexColumns(t, database, "adapter_runtimes_one_active_idx", "adapter_id")
	assertIndexColumns(t, database, "adapter_runtimes_adapter_idx", "adapter_id")
	assertIndexColumns(t, database, "observations_entity_history_idx", "entity_id,receive_order")
	assertIndexColumns(
		t,
		database,
		"observations_entity_disposition_history_idx",
		"entity_id,disposition,receive_order",
	)
	assertIndexColumns(
		t,
		database,
		"observations_entity_updates_history_idx",
		"entity_id,receive_order",
	)

	var supportColumn string
	if err := database.QueryRowContext(ctx, `
		SELECT name FROM pragma_table_info('entities') WHERE name = 'support_json'
	`).Scan(&supportColumn); err != nil {
		t.Fatalf("entities.support_json: %v", err)
	}
	var operationTableCount int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'entity_operations'
	`).Scan(&operationTableCount); err != nil {
		t.Fatal(err)
	}
	if operationTableCount != 0 {
		t.Fatal("entity_operations table still exists")
	}
}

func assertIndexColumns(t *testing.T, database *sql.DB, name, want string) {
	t.Helper()
	rows, queryErr := database.Query("SELECT name FROM pragma_index_info(?) ORDER BY seqno", name)
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			t.Fatal(err)
		}
		columns = append(columns, column)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(columns, ","); got != want {
		t.Fatalf("index %s columns = %q, want %q", name, got, want)
	}
}

func TestIDPrefixConstraintsRequireLiteralUnderscore(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}

	assertWriteRejected(
		t,
		database,
		`INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES ('devXbad', 'light', 'Bad', 'now', 'now')`,
	)
	if _, insertErr := database.ExecContext(
		ctx,
		`INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES ('dev_valid', 'light', 'Valid', 'now', 'now')`,
	); insertErr != nil {
		t.Fatal(insertErr)
	}
	assertWriteRejected(
		t,
		database,
		`INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES ('entXbad', 'dev_valid', 'Bad', 'test/v1', '{}', 'now', 'now')`,
	)
	if _, insertErr := database.ExecContext(
		ctx,
		`INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES ('ent_valid', 'dev_valid', 'Valid', 'test/v1', '{}', 'now', 'now')`,
	); insertErr != nil {
		t.Fatal(insertErr)
	}
	assertWriteRejected(
		t,
		database,
		`INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at) VALUES ('cmdXbad', 'ent_valid', 'adapter', 'set', '{}', 'cor_valid', 'requested', 'now', 'later')`,
	)
	assertWriteRejected(
		t,
		database,
		`INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at) VALUES ('cmd_correlation', 'ent_valid', 'adapter', 'set', '{}', 'corXbad', 'requested', 'now', 'later')`,
	)
	assertWriteRejected(
		t,
		database,
		`INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at, completed_at, outcome_observation_id) VALUES ('cmd_outcome', 'ent_valid', 'adapter', 'set', '{}', 'cor_valid', 'satisfied', 'now', 'later', 'now', 'obsXbad')`,
	)
	assertWriteRejected(
		t,
		database,
		`INSERT INTO observations (observation_id, adapter_id, entity_id, disposition, state_value_json, adapter_received_at, observed_at, expires_at) VALUES ('obsXbad', 'adapter', 'ent_valid', 'applied', 'true', 'now', 'now', 'later')`,
	)
}

func TestDeleteExpiredObservationsComparesTimestampsChronologically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}

	for _, observation := range []struct {
		id        string
		expiresAt string
	}{
		{id: "obs_past", expiresAt: "2026-08-22T11:59:59.5Z"},
		{id: "obs_future", expiresAt: "2026-08-22T12:00:00.5Z"},
	} {
		_, err := database.ExecContext(ctx, `
			INSERT INTO observations (
				observation_id, adapter_id, entity_id, disposition, state_value_json,
				adapter_received_at, observed_at, expires_at
			) VALUES (?, 'adapter', 'ent_entity', 'applied', 'true', ?, ?, ?)`,
			observation.id, "2026-08-22T11:00:00Z", "2026-08-22T11:00:00Z", observation.expiresAt,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := dbsqlc.New(database).
		DeleteExpiredObservations(ctx, dbsqlc.DeleteExpiredObservationsParams{
			ExpiresAt: "2026-08-22T12:00:00Z",
		})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted observations = %d, want 1", deleted)
	}
	var remaining string
	if scanErr := database.QueryRowContext(ctx, `SELECT observation_id FROM observations`).
		Scan(&remaining); scanErr != nil {
		t.Fatal(scanErr)
	}
	if remaining != "obs_future" {
		t.Fatalf("remaining observation = %q, want obs_future", remaining)
	}
}

func assertWriteRejected(t *testing.T, database *sql.DB, query string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), query); err == nil {
		t.Fatalf("write unexpectedly passed: %s", query)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	t.Parallel()
	if _, err := Open(context.Background(), " "); err == nil {
		t.Fatal("Open accepted an empty path")
	}
}
