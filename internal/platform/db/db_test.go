package db

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	receiptsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/receipts"
	"github.com/pressly/goose/v3"
)

func TestMigrateEmptySQLiteDatabase(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
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
		"adapter_bindings", "adapter_entity_mappings", "observation_receipts", "entity_states",
	} {
		var found string
		err := database.QueryRowContext(ctx,
			"SELECT name FROM sqlite_master WHERE type = 'table' AND name = ?", table,
		).Scan(&found)
		if err != nil {
			t.Fatalf("table %s: %v", table, err)
		}
	}

	assertIndexColumns(t, database, "entities_device_id_idx", "device_id,id")
	assertIndexColumns(t, database, "commands_entity_requested_idx", "entity_id,requested_at,id")

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

func TestResourceReadIndexMigrationReversesAndReapplies(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	migrations, err := fs.Sub(migrationFiles, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	assertIndexColumns(t, database, "entities_device_id_idx", "")
	assertIndexColumns(t, database, "commands_entity_requested_idx", "entity_id,requested_at")
	if _, err := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_migration', 'light', 'Migration', '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_migration', 'dev_migration', 'Power', 'hearth.power/v1', '{}',
		        '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	for _, command := range []struct {
		id          string
		requestedAt string
	}{
		{id: "cmd_exact", requestedAt: "2026-08-26T12:00:00Z"},
		{id: "cmd_fraction", requestedAt: "2026-08-26T12:00:00.1Z"},
	} {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO commands (
				id, entity_id, adapter_id, operation, parameters_json, correlation_id,
				status, requested_at, deadline_at
			) VALUES (?, 'ent_migration', 'adapter', 'set', '{}', 'cor_migration',
			          'requested', ?, '2026-08-26T12:00:10Z')`, command.id, command.requestedAt); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	assertIndexColumns(t, database, "entities_device_id_idx", "device_id,id")
	assertIndexColumns(t, database, "commands_entity_requested_idx", "entity_id,requested_at,id")

	rows, err := database.QueryContext(ctx, "SELECT id, requested_at FROM commands ORDER BY requested_at DESC")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for index, want := range []struct {
		id          string
		requestedAt string
	}{
		{id: "cmd_fraction", requestedAt: "2026-08-26T12:00:00.100000000Z"},
		{id: "cmd_exact", requestedAt: "2026-08-26T12:00:00.000000000Z"},
	} {
		if !rows.Next() {
			t.Fatalf("missing migrated command %d", index)
		}
		var id, requestedAt string
		if err := rows.Scan(&id, &requestedAt); err != nil {
			t.Fatal(err)
		}
		if id != want.id || requestedAt != want.requestedAt {
			t.Fatalf("migrated command %d = (%q, %q), want (%q, %q)", index, id, requestedAt, want.id, want.requestedAt)
		}
	}
	if rows.Next() {
		t.Fatal("migration returned an unexpected command")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func assertIndexColumns(t *testing.T, database *sql.DB, name, want string) {
	t.Helper()
	rows, err := database.Query("SELECT name FROM pragma_index_info(?) ORDER BY seqno", name)
	if err != nil {
		t.Fatal(err)
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
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}

	assertWriteRejected(t, database, `INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES ('devXbad', 'light', 'Bad', 'now', 'now')`)
	if _, err := database.ExecContext(ctx, `INSERT INTO devices (id, kind, name, created_at, updated_at) VALUES ('dev_valid', 'light', 'Valid', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	assertWriteRejected(t, database, `INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES ('entXbad', 'dev_valid', 'Bad', 'test/v1', '{}', 'now', 'now')`)
	if _, err := database.ExecContext(ctx, `INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at) VALUES ('ent_valid', 'dev_valid', 'Valid', 'test/v1', '{}', 'now', 'now')`); err != nil {
		t.Fatal(err)
	}
	assertWriteRejected(t, database, `INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at) VALUES ('cmdXbad', 'ent_valid', 'adapter', 'set', '{}', 'cor_valid', 'requested', 'now', 'later')`)
	assertWriteRejected(t, database, `INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at) VALUES ('cmd_correlation', 'ent_valid', 'adapter', 'set', '{}', 'corXbad', 'requested', 'now', 'later')`)
	assertWriteRejected(t, database, `INSERT INTO commands (id, entity_id, adapter_id, operation, parameters_json, correlation_id, status, requested_at, deadline_at, completed_at, outcome_observation_id) VALUES ('cmd_outcome', 'ent_valid', 'adapter', 'set', '{}', 'cor_valid', 'satisfied', 'now', 'later', 'now', 'obsXbad')`)
	assertWriteRejected(t, database, `INSERT INTO observation_receipts (observation_id, adapter_id, entity_id, disposition, adapter_received_at, observed_at, expires_at) VALUES ('obsXbad', 'adapter', 'ent_valid', 'applied', 'now', 'now', 'later')`)
}

func TestDeleteExpiredObservationReceiptsComparesTimestampsChronologically(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}

	for _, receipt := range []struct {
		id        string
		expiresAt string
	}{
		{id: "obs_past", expiresAt: "2026-08-22T11:59:59.5Z"},
		{id: "obs_future", expiresAt: "2026-08-22T12:00:00.5Z"},
	} {
		_, err := database.ExecContext(ctx, `
			INSERT INTO observation_receipts (
				observation_id, adapter_id, entity_id, disposition,
				adapter_received_at, observed_at, expires_at
			) VALUES (?, 'adapter', 'ent_entity', 'applied', ?, ?, ?)`,
			receipt.id, "2026-08-22T11:00:00Z", "2026-08-22T11:00:00Z", receipt.expiresAt,
		)
		if err != nil {
			t.Fatal(err)
		}
	}

	deleted, err := receiptsqlc.New(database).DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
		ExpiresAt: "2026-08-22T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted receipts = %d, want 1", deleted)
	}
	var remaining string
	if err := database.QueryRowContext(ctx, `SELECT observation_id FROM observation_receipts`).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != "obs_future" {
		t.Fatalf("remaining receipt = %q, want obs_future", remaining)
	}
}

func assertWriteRejected(t *testing.T, database *sql.DB, query string) {
	t.Helper()
	if _, err := database.ExecContext(context.Background(), query); err == nil {
		t.Fatalf("write unexpectedly passed: %s", query)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(context.Background(), " "); err == nil {
		t.Fatal("Open accepted an empty path")
	}
}
