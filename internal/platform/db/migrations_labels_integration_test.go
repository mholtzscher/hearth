//go:build integration

package db //nolint:testpackage // Migration tests use the real embedded migration set.

import (
	"context"
	"path/filepath"
	"testing"
)

func TestLabelsMigrationRoundTripPreservesDevicesAndEntities(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "labels.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := newGooseProvider(t, database)
	if _, err = provider.UpTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	const stamp = "2026-11-25T00:00:00Z"
	const device = "dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const entity = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const area = "lbl_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	const tag = "lbl_01890f47-7a6b-7c4d-8e9f-0123456789ac"
	mustExec := func(query string, args ...any) {
		t.Helper()
		if _, execErr := database.ExecContext(ctx, query, args...); execErr != nil {
			t.Fatalf("%s: %v", query, execErr)
		}
	}
	mustExec(`INSERT INTO devices (id,kind,name,created_at,updated_at) VALUES (?,'light','Lamp',?,?)`,
		device, stamp, stamp)
	mustExec(`INSERT INTO entities (id,device_id,name,type_id,support_json,created_at,updated_at)
		VALUES (?,?,'Power','hearth.power/v1','{}',?,?)`, entity, device, stamp, stamp)

	if _, err = provider.UpTo(ctx, 34); err != nil {
		t.Fatal(err)
	}
	mustExec(`INSERT INTO labels (id,name,name_key,kind,created_at,updated_at)
		VALUES (?,'Kitchen','kitchen','area',?,?), (?,'Bedtime','bedtime','tag',?,?)`,
		area, stamp, stamp, tag, stamp, stamp)
	mustExec(`UPDATE devices SET area_id = ? WHERE id = ?`, area, device)
	mustExec(`INSERT INTO device_tags (device_id,label_id) VALUES (?,?)`, device, tag)
	mustExec(`INSERT INTO entity_tags (entity_id,label_id) VALUES (?,?)`, entity, tag)
	var areaRows, tagRows int
	if err = database.QueryRowContext(ctx, `SELECT
		SUM(kind = 'area' AND source = 'device' AND label_id = ?), SUM(kind = 'tag')
		FROM entity_effective_labels WHERE entity_id = ?`, area, entity).Scan(&areaRows, &tagRows); err != nil {
		t.Fatal(err)
	}
	if areaRows != 1 || tagRows != 2 {
		t.Fatalf("effective labels: area rows %d, tag rows %d; want 1 and 2 (one per source)", areaRows, tagRows)
	}
	// Deleting a Label clears the Areas that name it and its tag assignments.
	mustExec(`DELETE FROM labels WHERE id = ?`, area)
	var deviceArea *string
	if err = database.QueryRowContext(ctx, `SELECT area_id FROM devices WHERE id = ?`, device).
		Scan(&deviceArea); err != nil || deviceArea != nil {
		t.Fatalf("device area after label delete = %v, %v", deviceArea, err)
	}

	if _, err = provider.DownTo(ctx, 33); err != nil {
		t.Fatal(err)
	}
	var names string
	if err = database.QueryRowContext(ctx, `SELECT d.name || '/' || e.name FROM devices AS d
		JOIN entities AS e ON e.device_id = d.id WHERE d.id = ?`, device).Scan(&names); err != nil {
		t.Fatal(err)
	}
	if names != "Lamp/Power" {
		t.Fatalf("rows after downgrade = %q", names)
	}
	var labelTables int
	if err = database.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema
		WHERE name IN ('labels','device_tags','entity_tags','entity_effective_labels')`).Scan(&labelTables); err != nil {
		t.Fatal(err)
	}
	if labelTables != 0 {
		t.Fatalf("%d label schema objects remain after downgrade", labelTables)
	}
	if _, err = provider.UpTo(ctx, 34); err != nil {
		t.Fatalf("re-upgrade: %v", err)
	}
}
