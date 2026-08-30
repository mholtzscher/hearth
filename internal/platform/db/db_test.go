package db //nolint:testpackage,cyclop // Migration tests require the package-private embedded migration set.

import (
	"context"
	"database/sql"
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"

	receiptsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/receipts"
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
		"adapter_bindings", "adapter_entity_mappings", "observation_receipts", "entity_states",
		"adapter_instances", "adapter_runtimes", "entity_availability_current",
		"health_transitions", "entity_ownership_intervals",
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
	assertIndexColumns(t, database, "adapter_runtimes_one_active_idx", "adapter_id")
	assertIndexColumns(t, database, "entity_ownership_intervals_one_open_idx", "entity_id")

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

//nolint:gocognit // Migration round-trip assertions are intentionally kept together.
func TestResourceReadIndexMigrationReversesAndReapplies(t *testing.T) {
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
	migrations, migrationsErr := fs.Sub(migrationFiles, "migrations")
	if migrationsErr != nil {
		t.Fatal(migrationsErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
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

	rows, queryErr := database.QueryContext(ctx, "SELECT id, requested_at FROM commands ORDER BY requested_at DESC")
	if queryErr != nil {
		t.Fatal(queryErr)
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

//nolint:gocognit // Migration round-trip assertions are intentionally kept together.
func TestEntityEnablementMigrationPreservesPopulatedDatabaseAndMapsDown(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	migrations, migrationsErr := fs.Sub(migrationFiles, "migrations")
	if migrationsErr != nil {
		t.Fatal(migrationsErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if _, err := provider.UpTo(ctx, 2); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_migration', 'light', 'Migration', '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z');
		INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_migration', 'dev_migration', 'Power', 'hearth.power/v1', '{}',
		        '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z');
		INSERT INTO adapter_bindings (
			adapter_id, binding_key, device_id, created_at, updated_at
		) VALUES ('simulator', 'migration', 'dev_migration', '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z');
		INSERT INTO adapter_entity_mappings (
			adapter_id, binding_key, entity_key, entity_id, external_entity_id, created_at, updated_at
		) VALUES ('simulator', 'migration', 'power', 'ent_migration', 'external.power',
		          '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z');
		INSERT INTO observation_receipts (
			receive_order, observation_id, adapter_id, entity_id, disposition,
			adapter_received_at, observed_at, expires_at
		) VALUES (41, 'obs_migration', 'simulator', 'ent_migration', 'applied',
		          '2026-08-26T12:00:00Z', '2026-08-26T12:00:00Z', '2026-09-03T12:00:00Z');
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) VALUES ('ent_migration', 'obs_migration', 'true', '2026-08-26T12:00:00Z',
		          '2026-08-26T12:00:00Z', 41);
	`); err != nil {
		t.Fatal(err)
	}
	statuses := []struct {
		status, acceptedAt, completedAt, outcomeID, failureCode any
	}{
		{"requested", nil, nil, nil, nil},
		{"accepted", "2026-08-26T12:00:01Z", nil, nil, nil},
		{"satisfied", "2026-08-26T12:00:01Z", "2026-08-26T12:00:02Z", "obs_outcome", nil},
		{"rejected", nil, "2026-08-26T12:00:02Z", nil, "upstream_rejected"},
		{"adapter_unavailable", nil, "2026-08-26T12:00:02Z", nil, "adapter_unavailable"},
		{"outcome_timeout", "2026-08-26T12:00:01Z", "2026-08-26T12:00:10Z", nil, "outcome_timeout"},
		{"internal_failure", nil, "2026-08-26T12:00:02Z", nil, "internal_error"},
		{"interrupted", "2026-08-26T12:00:01Z", "2026-08-26T12:00:02Z", nil, "core_restarted"},
	}
	for index, status := range statuses {
		if _, err := database.ExecContext(ctx, `
			INSERT INTO commands (
				id, entity_id, adapter_id, operation, parameters_json, correlation_id,
				status, requested_at, deadline_at, accepted_at, completed_at,
				outcome_observation_id, failure_code
			) VALUES (?, 'ent_migration', 'simulator', 'set', '{"value":true}', 'cor_migration',
			          ?, '2026-08-26T12:00:00.000000000Z', '2026-08-26T12:00:10Z', ?, ?, ?, ?)`,
			"cmd_migration_"+string(rune('a'+index)), status.status, status.acceptedAt,
			status.completedAt, status.outcomeID, status.failureCode,
		); err != nil {
			t.Fatalf("insert %s Command: %v", status.status, err)
		}
	}

	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}
	var enabled, receiptOrder, commandCount int
	if scanErr := database.QueryRowContext(ctx, "SELECT enabled FROM entities WHERE id = 'ent_migration'").
		Scan(&enabled); scanErr != nil {
		t.Fatal(scanErr)
	}
	if scanErr := database.QueryRowContext(ctx, "SELECT receive_order FROM observation_receipts WHERE observation_id = 'obs_migration'").
		Scan(&receiptOrder); scanErr != nil {
		t.Fatal(scanErr)
	}
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM commands").Scan(&commandCount); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || receiptOrder != 41 || commandCount != len(statuses) {
		t.Fatalf("upgrade = enabled %d, receipt order %d, Commands %d", enabled, receiptOrder, commandCount)
	}
	assertForeignKeyCheckEmpty(t, database)
	assertIndexColumns(t, database, "commands_entity_requested_idx", "entity_id,requested_at,id")

	if _, err := database.ExecContext(ctx, `
		INSERT INTO commands (
			id, entity_id, adapter_id, operation, parameters_json, correlation_id,
			status, requested_at, deadline_at, completed_at, failure_code
		) VALUES ('cmd_disabled', 'ent_migration', 'simulator', 'set', '{"value":false}',
		          'cor_disabled', 'entity_disabled', '2026-08-26T12:01:00.000000000Z',
		          '2026-08-26T12:01:10Z', '2026-08-26T12:01:00Z', 'entity_disabled');
		INSERT INTO observation_receipts (
			observation_id, adapter_id, entity_id, disposition, rejection_code,
			adapter_received_at, observed_at, expires_at
		) VALUES ('obs_disabled', 'simulator', 'ent_migration', 'rejected', 'entity_disabled',
		          '2026-08-26T12:01:00Z', '2026-08-26T12:01:00Z', '2026-09-03T12:01:00Z');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	var status, failureCode string
	if scanErr := database.QueryRowContext(ctx, "SELECT status, failure_code FROM commands WHERE id = 'cmd_disabled'").
		Scan(&status, &failureCode); scanErr != nil {
		t.Fatal(scanErr)
	}
	if status != "internal_failure" || failureCode != "internal_error" {
		t.Fatalf("down-mapped Command = %q/%q", status, failureCode)
	}
	var disabledReceipts int
	if scanErr := database.QueryRowContext(ctx, "SELECT count(*) FROM observation_receipts WHERE observation_id = 'obs_disabled'").
		Scan(&disabledReceipts); scanErr != nil {
		t.Fatal(scanErr)
	}
	if disabledReceipts != 0 {
		t.Fatalf("disabled receipts after down = %d", disabledReceipts)
	}
	var enabledColumns int
	if scanErr := database.QueryRowContext(ctx, "SELECT count(*) FROM pragma_table_info('entities') WHERE name = 'enabled'").
		Scan(&enabledColumns); scanErr != nil {
		t.Fatal(scanErr)
	}
	if enabledColumns != 0 {
		t.Fatal("entities.enabled remains after down migration")
	}
	assertForeignKeyCheckEmpty(t, database)
}

func TestEntityEnablementDownFailsForUnexpectedCurrentStateReceipt(t *testing.T) {
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
	if _, err := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_unexpected', 'light', 'Unexpected', 'now', 'now');
		INSERT INTO entities (id, device_id, name, type_id, support_json, created_at, updated_at)
		VALUES ('ent_unexpected', 'dev_unexpected', 'Unexpected', 'hearth.power/v1', '{}', 'now', 'now');
		INSERT INTO observation_receipts (
			observation_id, adapter_id, entity_id, disposition, rejection_code,
			adapter_received_at, observed_at, expires_at
		) VALUES ('obs_unexpected', 'simulator', 'ent_unexpected', 'rejected', 'entity_disabled',
		          'now', 'now', 'later');
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) SELECT 'ent_unexpected', observation_id, 'true', 'now', 'now', receive_order
		  FROM observation_receipts WHERE observation_id = 'obs_unexpected';
	`); err != nil {
		t.Fatal(err)
	}
	migrations, migrationsErr := fs.Sub(migrationFiles, "migrations")
	if migrationsErr != nil {
		t.Fatal(migrationsErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err == nil {
		t.Fatal("down migration unexpectedly deleted a receipt referenced by current State")
	}
	var count int
	if scanErr := database.QueryRowContext(ctx, "SELECT count(*) FROM observation_receipts WHERE observation_id = 'obs_unexpected'").
		Scan(&count); scanErr != nil {
		t.Fatal(scanErr)
	}
	if count != 1 {
		t.Fatalf("unexpected receipt count after failed down = %d", count)
	}
}

//nolint:gocognit,gocyclo,cyclop // The migration round trip checks related data and constraints together.
func TestAdapterHealthMigrationPreservesAndMapsPopulatedDatabase(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, openErr := Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if openErr != nil {
		t.Fatal(openErr)
	}
	t.Cleanup(func() { _ = database.Close() })
	migrations, migrationsErr := fs.Sub(migrationFiles, "migrations")
	if migrationsErr != nil {
		t.Fatal(migrationsErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if _, err := provider.UpTo(ctx, 3); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_health', 'light', 'Health', '2026-08-29T12:00:00Z', '2026-08-29T12:00:00Z');
		INSERT INTO entities (
			id, device_id, name, type_id, support_json, enabled, created_at, updated_at
		) VALUES ('ent_health', 'dev_health', 'Power', 'hearth.power/v1', '{}', 1,
		          '2026-08-29T12:00:00Z', '2026-08-29T12:00:00Z');
		INSERT INTO adapter_bindings (
			adapter_id, binding_key, device_id, created_at, updated_at
		) VALUES ('simulator', 'health', 'dev_health',
		          '2026-08-29T12:00:00Z', '2026-08-29T12:00:01Z');
		INSERT INTO adapter_entity_mappings (
			adapter_id, binding_key, entity_key, entity_id, external_entity_id,
			created_at, updated_at
		) VALUES ('simulator', 'health', 'power', 'ent_health', 'external.health',
		          '2026-08-29T12:00:00Z', '2026-08-29T12:00:01Z');
		INSERT INTO observation_receipts (
			receive_order, observation_id, adapter_id, entity_id, disposition,
			adapter_received_at, observed_at, expires_at
		) VALUES (51, 'obs_health', 'simulator', 'ent_health', 'applied',
		          '2026-08-29T12:00:00Z', '2026-08-29T12:00:00Z', '2026-09-05T12:00:00Z');
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) VALUES ('ent_health', 'obs_health', 'true', '2026-08-29T12:00:00Z',
		          '2026-08-29T12:00:00Z', 51);
		INSERT INTO commands (
			id, entity_id, adapter_id, operation, parameters_json, correlation_id,
			status, requested_at, deadline_at, completed_at, failure_code
		) VALUES ('cmd_health', 'ent_health', 'simulator', 'set', '{}', 'cor_health',
		          'adapter_unavailable', '2026-08-29T12:00:00.000000000Z',
		          '2026-08-29T12:00:10Z', '2026-08-29T12:00:00Z', 'adapter_unavailable');
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Up(ctx); err != nil {
		t.Fatal(err)
	}

	var healthStatus, healthReason string
	if err := database.QueryRowContext(ctx, `
		SELECT health_status, health_reason_code
		FROM adapter_instances WHERE adapter_id = 'simulator'
	`).Scan(&healthStatus, &healthReason); err != nil {
		t.Fatal(err)
	}
	if healthStatus != "unknown" || healthReason != "hearth.awaiting_runtime" {
		t.Fatalf("seeded Adapter health = %q/%q", healthStatus, healthReason)
	}
	var owner string
	var startOrder int64
	if err := database.QueryRowContext(ctx, `
		SELECT adapter_id, starting_receive_order
		FROM entity_ownership_intervals WHERE entity_id = 'ent_health'
	`).Scan(&owner, &startOrder); err != nil {
		t.Fatal(err)
	}
	if owner != "simulator" || startOrder != 0 {
		t.Fatalf("seeded ownership = %q/%d", owner, startOrder)
	}
	var status, failureCode string
	var runtimeID sql.NullString
	if err := database.QueryRowContext(ctx, `
		SELECT status, failure_code, runtime_id FROM commands WHERE id = 'cmd_health'
	`).Scan(&status, &failureCode, &runtimeID); err != nil {
		t.Fatal(err)
	}
	if status != "adapter_unhealthy" || failureCode != "adapter_unhealthy" || runtimeID.Valid {
		t.Fatalf("up-mapped Command = %q/%q/%#v", status, failureCode, runtimeID)
	}
	var stateOrder int64
	if err := database.QueryRowContext(ctx, `
		SELECT receive_order FROM entity_states WHERE entity_id = 'ent_health'
	`).Scan(&stateOrder); err != nil {
		t.Fatal(err)
	}
	if stateOrder != 51 {
		t.Fatalf("preserved State receive order = %d", stateOrder)
	}
	assertForeignKeyCheckEmpty(t, database)

	if _, err := database.ExecContext(ctx, `
		INSERT INTO adapter_runtimes (
			runtime_id, claim_id, adapter_id, software_name, software_version,
			claimed_at, lease_expires_at
		) VALUES (
			'run_01890f47-7a6b-7c4d-8e9f-0123456789ab',
			'clm_01890f47-7a6b-7c4d-8e9f-0123456789ab',
			'simulator', 'hearth-simulator', '0.1.0',
			'2026-08-29T12:01:00Z', '2026-08-29T12:01:15Z'
		);
		UPDATE adapter_instances
		SET active_runtime_id = 'run_01890f47-7a6b-7c4d-8e9f-0123456789ab',
		    health_runtime_id = 'run_01890f47-7a6b-7c4d-8e9f-0123456789ab'
		WHERE adapter_id = 'simulator';
		INSERT INTO commands (
			id, entity_id, adapter_id, runtime_id, operation, parameters_json,
			correlation_id, status, requested_at, deadline_at, completed_at, failure_code
		) VALUES (
			'cmd_entity_unavailable', 'ent_health', 'simulator',
			'run_01890f47-7a6b-7c4d-8e9f-0123456789ab', 'set', '{}',
			'cor_unavailable', 'entity_unavailable', '2026-08-29T12:01:00.000000000Z',
			'2026-08-29T12:01:10Z', '2026-08-29T12:01:00Z', 'entity_unavailable'
		);
		INSERT INTO observation_receipts (
			observation_id, adapter_id, runtime_id, entity_id, disposition,
			rejection_code, adapter_received_at, observed_at, expires_at
		) VALUES (
			'obs_stale', 'simulator', 'run_01890f47-7a6b-7c4d-8e9f-0123456789ab',
			'ent_health', 'rejected', 'stale_runtime', '2026-08-29T12:01:00Z',
			'2026-08-29T12:01:00Z', '2026-09-05T12:01:00Z'
		);
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT status, failure_code FROM commands WHERE id = 'cmd_health'
	`).Scan(&status, &failureCode); err != nil {
		t.Fatal(err)
	}
	if status != "adapter_unavailable" || failureCode != "adapter_unavailable" {
		t.Fatalf("down-mapped unhealthy Command = %q/%q", status, failureCode)
	}
	if err := database.QueryRowContext(ctx, `
		SELECT status, failure_code FROM commands WHERE id = 'cmd_entity_unavailable'
	`).Scan(&status, &failureCode); err != nil {
		t.Fatal(err)
	}
	if status != "rejected" || failureCode != "upstream_rejected" {
		t.Fatalf("down-mapped unavailable Entity Command = %q/%q", status, failureCode)
	}
	var staleReceipts int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*) FROM observation_receipts WHERE observation_id = 'obs_stale'
	`).Scan(&staleReceipts); err != nil {
		t.Fatal(err)
	}
	if staleReceipts != 0 {
		t.Fatalf("stale runtime receipts after down = %d", staleReceipts)
	}
	for table, column := range map[string]string{"commands": "runtime_id", "observation_receipts": "runtime_id"} {
		var columns int
		if err := database.QueryRowContext(ctx,
			"SELECT count(*) FROM pragma_table_info(?) WHERE name = ?", table, column,
		).Scan(&columns); err != nil {
			t.Fatal(err)
		}
		if columns != 0 {
			t.Fatalf("%s.%s remains after down", table, column)
		}
	}
	var healthTables int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*) FROM sqlite_master
		WHERE type = 'table' AND name IN (
			'adapter_instances', 'adapter_runtimes', 'entity_availability_current',
			'health_transitions', 'entity_ownership_intervals'
		)
	`).Scan(&healthTables); err != nil {
		t.Fatal(err)
	}
	if healthTables != 0 {
		t.Fatalf("health tables after down = %d", healthTables)
	}
	assertForeignKeyCheckEmpty(t, database)
}

func TestAdapterHealthDownRejectsStaleRuntimeReceiptBackingCurrentState(t *testing.T) {
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
	if _, err := database.ExecContext(ctx, `
		INSERT INTO devices (id, kind, name, created_at, updated_at)
		VALUES ('dev_stale', 'light', 'Stale', 'now', 'now');
		INSERT INTO entities (
			id, device_id, name, type_id, support_json, enabled, created_at, updated_at
		) VALUES ('ent_stale', 'dev_stale', 'Stale', 'hearth.power/v1', '{}', 1, 'now', 'now');
		INSERT INTO observation_receipts (
			observation_id, adapter_id, entity_id, disposition, rejection_code,
			adapter_received_at, observed_at, expires_at
		) VALUES ('obs_stale_state', 'simulator', 'ent_stale', 'rejected', 'stale_runtime',
		          'now', 'now', 'later');
		INSERT INTO entity_states (
			entity_id, observation_id, value_json, adapter_received_at, observed_at, receive_order
		) SELECT 'ent_stale', observation_id, 'true', 'now', 'now', receive_order
		  FROM observation_receipts WHERE observation_id = 'obs_stale_state';
	`); err != nil {
		t.Fatal(err)
	}
	migrations, migrationsErr := fs.Sub(migrationFiles, "migrations")
	if migrationsErr != nil {
		t.Fatal(migrationsErr)
	}
	provider, providerErr := goose.NewProvider(goose.DialectSQLite3, database, migrations)
	if providerErr != nil {
		t.Fatal(providerErr)
	}
	if _, err := provider.Down(ctx); err == nil {
		t.Fatal("down migration unexpectedly removed a stale-runtime receipt backing current State")
	}
	var count int
	if err := database.QueryRowContext(ctx, `
		SELECT count(*) FROM observation_receipts WHERE observation_id = 'obs_stale_state'
	`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("stale current-State receipt count after failed down = %d", count)
	}
}

func assertForeignKeyCheckEmpty(t *testing.T, database *sql.DB) {
	t.Helper()
	rows, queryErr := database.Query("PRAGMA foreign_key_check")
	if queryErr != nil {
		t.Fatal(queryErr)
	}
	defer rows.Close()
	if rows.Next() {
		t.Fatal("PRAGMA foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
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
		`INSERT INTO observation_receipts (observation_id, adapter_id, entity_id, disposition, adapter_received_at, observed_at, expires_at) VALUES ('obsXbad', 'adapter', 'ent_valid', 'applied', 'now', 'now', 'later')`,
	)
}

func TestDeleteExpiredObservationReceiptsComparesTimestampsChronologically(t *testing.T) {
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

	deleted, err := receiptsqlc.New(database).
		DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
			ExpiresAt: "2026-08-22T12:00:00Z",
		})
	if err != nil {
		t.Fatal(err)
	}
	if deleted != 1 {
		t.Fatalf("deleted receipts = %d, want 1", deleted)
	}
	var remaining string
	if scanErr := database.QueryRowContext(ctx, `SELECT observation_id FROM observation_receipts`).
		Scan(&remaining); scanErr != nil {
		t.Fatal(scanErr)
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
	t.Parallel()
	if _, err := Open(context.Background(), " "); err == nil {
		t.Fatal("Open accepted an empty path")
	}
}
