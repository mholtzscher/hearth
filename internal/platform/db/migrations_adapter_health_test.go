package db //nolint:testpackage // Migration tests use the real embedded migration set.

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

//nolint:gocognit // One real upgrade/downgrade timeline preserves the legacy evidence being asserted.
func TestAdapterHealthMigrationPreservesHistoryAndOnlyBackfillsEligibleReports(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-10-18T00:00:00Z"
	const runtime = "run_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const oldRuntime = "run_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	const entity = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const secondEntity = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	if _, err = database.ExecContext(ctx, `INSERT INTO adapter_instances
		(adapter_id, health_status, health_reason_code, health_source, health_since, health_evidence_at)
		VALUES ('simulator','unknown','hearth.awaiting_health','core',?,?)`, stamp, stamp); err != nil {
		t.Fatal(err)
	}
	insertLegacyAdapterRuntime(t, database, runtime, stamp, false)
	insertLegacyAdapterRuntime(t, database, oldRuntime, stamp, true)
	if _, err = database.ExecContext(ctx, `UPDATE adapter_instances SET active_runtime_id = ?, health_runtime_id = ?,
		health_status = 'healthy', health_reason_code = NULL, health_source = 'adapter', health_source_observed_at = ?`, runtime, runtime, stamp); err != nil {
		t.Fatal(err)
	}
	const device = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	if _, err = database.ExecContext(
		ctx,
		`INSERT INTO devices (id,kind,name,created_at,updated_at) VALUES (?,'light','Device',?,?)`,
		device,
		stamp,
		stamp,
	); err != nil {
		t.Fatal(err)
	}
	if _, err = database.ExecContext(
		ctx,
		`INSERT INTO adapter_bindings (adapter_id,binding_key,device_id,created_at,updated_at) VALUES ('simulator','light',?,?,?)`,
		device,
		stamp,
		stamp,
	); err != nil {
		t.Fatal(err)
	}
	for index, id := range []string{entity, secondEntity} {
		if _, err = database.ExecContext(
			ctx,
			`INSERT INTO entities (id,device_id,name,name_override,type_id,support_json,created_at,updated_at)
			VALUES (?,?,'Adapter name','Household name','hearth.power/v1','{}',?,?)`,
			id,
			device,
			stamp,
			stamp,
		); err != nil {
			t.Fatal(err)
		}
		if _, err = database.ExecContext(ctx, `INSERT INTO adapter_entity_mappings
			(adapter_id,binding_key,entity_key,entity_id,external_entity_id,created_at,updated_at)
			VALUES ('simulator','light',?,?,?,?,?)`, id, id, id, stamp, stamp); err != nil {
			t.Fatal(err)
		}
		reportRuntime := runtime
		if index == 1 {
			reportRuntime = oldRuntime
		}
		if _, err = database.ExecContext(ctx, `INSERT INTO entity_availability_current
			(entity_id,adapter_id,runtime_id,status,source_observed_at,evidence_at,current_since)
			VALUES (?,'simulator',?,'available',?,?,?)`, id, reportRuntime, stamp, stamp, stamp); err != nil {
			t.Fatal(err)
		}
	}
	if _, err = database.ExecContext(ctx, `INSERT INTO health_transitions
		(receive_order,resource_kind,adapter_id,entity_id,runtime_id,status,source,observed_at)
		VALUES (123,'entity','simulator',?,?,'available','entity_report',?)`, entity, runtime, stamp); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 15); err != nil {
		t.Fatal(err)
	}
	assertNoForeignKeyViolations(ctx, t, database)
	var revision int64
	if err = database.QueryRowContext(ctx, "SELECT health_revision FROM entity_availability_current WHERE entity_id = ?", entity).
		Scan(&revision); err != nil ||
		revision != 1 {
		t.Fatalf("eligible generation = %d, %v", revision, err)
	}
	if err = database.QueryRowContext(ctx, "SELECT health_revision FROM entity_availability_current WHERE entity_id = ?", secondEntity).
		Scan(&revision); err != nil ||
		revision != 0 {
		t.Fatalf("stale generation = %d, %v", revision, err)
	}
	var observed string
	var position int64
	if err = database.QueryRowContext(ctx, "SELECT receive_order, observed_at FROM health_transitions").
		Scan(&position, &observed); err != nil || position != 123 ||
		observed != stamp {
		t.Fatalf("retained history = %d / %s, %v", position, observed, err)
	}
	var name, adapterName string
	var availability sql.NullString
	if err = database.QueryRowContext(ctx, "SELECT name, adapter_name, availability_status FROM entity_read_projection WHERE id = ?", entity).
		Scan(&name, &adapterName, &availability); err != nil {
		t.Fatal(err)
	}
	if name != "Household name" || adapterName != "Adapter name" || availability.Valid {
		t.Fatalf("upgrade read = %q / %q / %v", name, adapterName, availability)
	}
	if _, err = provider.DownTo(ctx, 14); err != nil {
		t.Fatal(err)
	}
	assertNoForeignKeyViolations(ctx, t, database)
	var status string
	if err = database.QueryRowContext(ctx, "SELECT name, adapter_health_status FROM entity_read_projection WHERE id = ?", entity).
		Scan(&name, &status); err != nil {
		t.Fatal(err)
	}
	if name != "Household name" || status != "healthy" {
		t.Fatalf("restored view = %q / %q", name, status)
	}
}

func insertLegacyAdapterRuntime(t *testing.T, database *sql.DB, id, stamp string, ended bool) {
	t.Helper()
	var endedAt, endReason sql.NullString
	if ended {
		endedAt = sql.NullString{String: stamp, Valid: true}
		endReason = sql.NullString{String: "stopped", Valid: true}
	}
	if _, err := database.ExecContext(t.Context(), `INSERT INTO adapter_runtimes
		(runtime_id, adapter_id, software_name, software_version, claimed_at, lease_expires_at, ended_at, end_reason)
		VALUES (?, 'simulator','hearth-simulator','1',?,?,?,?)`, id, stamp, stamp, endedAt, endReason); err != nil {
		t.Fatal(err)
	}
}
