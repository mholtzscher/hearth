//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Historical JSON is written directly: using today's encoder here would hide
// the upgrade fault. Upgrade must preserve definitions and retained Run evidence
// while making both readable by the strict current codec.
func TestLegacyCommandDefinitionsSurviveUpgrade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "legacy.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 7); err != nil {
		t.Fatal(err)
	}
	id := newAutomationIDString(t)
	runID := newRunIDString(t)
	entityID := newEntityID(t)
	legacy := fmt.Sprintf(`{
  "name":"Legacy automation","enabled":false,
  "triggers":[{"id":"press","kind":"entity_event","entity_id":%q,"event_name":"single_press"}],
  "steps":[
    {"id":"first","entity_id":%q,"operation":"set","parameters":{"value":true}},
    {"id":"second","entity_id":%q,"operation":"set","parameters":{"value":false}}
  ]
}`, entityID, entityID, entityID)
	mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
		VALUES (?, 4, ?, ?, ?)`, id, legacy, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, insertHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`, legacy, runID)
	for position, stepID := range []string{"first", "second"} {
		mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
			VALUES (?, ?, ?, 'not_attempted')`, runID, position, stepID)
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	repository := scheduleRepo(database, &at)
	record, err := repository.GetAutomation(ctx, automations.AutomationID(id))
	if err != nil {
		t.Fatalf("read upgraded legacy definition: %v", err)
	}
	if record.Revision != 4 || record.Definition.Enabled || len(record.Definition.Steps) != 2 ||
		record.CreatedAt.Format("2006-01-02T15:04:05.000000000Z") != migrationTimestamp ||
		record.UpdatedAt != record.CreatedAt {
		t.Fatalf("upgrade changed definition metadata: %#v", record)
	}
	entry, err := repository.GetHistoryEntry(ctx, automations.AutomationID(id), runID)
	if err != nil {
		t.Fatalf("read upgraded retained Run: %v", err)
	}
	run, ok := entry.(automations.Run)
	if !ok || len(run.Steps) != 2 || run.Steps[0].StepID != "first" ||
		run.Steps[1].StepID != "second" {
		t.Fatalf("upgrade changed retained Step order: %#v", entry)
	}
	// Disabled definitions are still decoded on every scheduler pre-read.
	deviceService := devices.NewService(devices.Stores{}, nil, nil, devices.Dependencies{})
	service := automations.NewService(repository, deviceService, automations.Dependencies{
		HouseholdLocation: time.UTC, Now: func() time.Time { return at },
	})
	if err = service.InitializeSchedules(ctx, at); err != nil {
		t.Fatal(err)
	}
	if _, err = service.ProcessDueSchedules(ctx); err != nil {
		t.Fatalf("first scheduler tick after upgrade: %v", err)
	}
	// Independent JSON comparison permits only the new discriminator; neither
	// parameter values nor other fields may be rewritten by the migration.
	var migrated string
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, id).Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err = json.Unmarshal([]byte(legacy), &want); err != nil {
		t.Fatal(err)
	}
	for _, step := range want["steps"].([]any) {
		step.(map[string]any)["kind"] = "command"
		step.(map[string]any)["parameters"] = map[string]any{"literal": step.(map[string]any)["parameters"]}
	}
	if err = json.Unmarshal([]byte(migrated), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration changed legacy document beyond command kinds: %s", migrated)
	}
}

// Nested prerelease definitions use the same implicit Command shape. Upgrade
// only executable Steps, never lookalike objects inside Command parameters,
// and leave documents already using v2 byte-for-byte unchanged.
func TestCommandKindMigrationPreservesNestedStepsAndCurrentDocuments(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "nested.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	entityID := newEntityID(t)
	expected := fmt.Sprintf(`{
  "name":"Nested legacy","enabled":true,
  "triggers":[{"id":"press","kind":"entity_event","entity_id":%q,"event_name":"single_press"}],
  "steps":[
    {"id":"if","kind":"if","conditions":{"id":"condition","kind":"trigger","trigger_ids":["press"]},
      "then":[{"id":"then","kind":"command","entity_id":%q,"operation":"set","parameters":{
        "steps":[{"id":"parameter","entity_id":"unchanged","operation":"set","parameters":{}}],
        "precise":9007199254740993}}],
      "else":[{"id":"choose","kind":"choose","branches":[
        {"id":"branch","conditions":{"id":"branch-condition","kind":"trigger","trigger_ids":["press"]},
          "steps":[{"id":"chosen","kind":"command","entity_id":%q,"operation":"set","parameters":{"value":true}}]}
      ],"default":[{"id":"fallback","kind":"command","entity_id":%q,"operation":"set","parameters":{"value":false}}]}]},
    {"id":"wait","kind":"delay","duration_ms":1}
  ]
}`, entityID, entityID, entityID, entityID)
	legacy := strings.ReplaceAll(expected, `"kind":"command",`, "")
	legacyID, currentID := newAutomationIDString(t), newAutomationIDString(t)
	for id, raw := range map[string]string{legacyID: legacy, currentID: expected} {
		mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
			VALUES (?, 1, ?, ?, ?)`, id, raw, migrationTimestamp, migrationTimestamp)
	}
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	var upgraded, unchanged string
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, legacyID).
		Scan(&upgraded); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, currentID).
		Scan(&unchanged); err != nil {
		t.Fatal(err)
	}
	if unchanged != expected {
		t.Fatal("migration rewrote an already-current document")
	}
	var want, got any
	if err = json.Unmarshal([]byte(expected), &want); err != nil {
		t.Fatal(err)
	}
	if err = json.Unmarshal([]byte(upgraded), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || !strings.Contains(upgraded, "9007199254740993") {
		t.Fatalf("nested upgrade changed Step meaning or parameter precision: %s", upgraded)
	}
	// Down deliberately retains canonical data; Up must then be a no-op.
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, 19); err != nil {
		t.Fatal(err)
	}
	var repeated string
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, legacyID).
		Scan(&repeated); err != nil {
		t.Fatal(err)
	}
	if repeated != upgraded {
		t.Fatal("repeated migration changed canonical data")
	}
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if _, err = scheduleRepo(database, &at).GetAutomation(ctx, automations.AutomationID(legacyID)); err != nil {
		t.Fatalf("read upgraded nested definition: %v", err)
	}
}

// A failed snapshot rewrite must roll back earlier definition rewrites rather
// than partially upgrading the database or dropping the existing size guard.
func TestCommandKindMigrationRollsBackWhenSnapshotExceedsSizeBound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "bounded.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	if _, err = scheduleMigrationProvider(t, database).UpTo(ctx, 13); err != nil {
		t.Fatal(err)
	}
	id, runID := newAutomationIDString(t), newRunIDString(t)
	entityID := newEntityID(t)
	legacy := fmt.Sprintf(
		`{"name":"Bounded","enabled":false,"triggers":[{"id":"press","kind":"entity_event","entity_id":%q,"event_name":"single_press"}],"steps":[{"id":"set","entity_id":%q,"operation":"set","parameters":{"padding":""}}]}`,
		entityID,
		entityID,
	)
	// This deliberately corrupt legacy snapshot exceeds the 32-leaf bound.
	// It fits the old bytes but 33 added discriminators exceed the new bound.
	oversizedAfterUpgrade := maximumLegacyCommandDefinition(t, 33)
	mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
		VALUES (?, 1, ?, ?, ?)`, id, legacy, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, insertHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`,
		oversizedAfterUpgrade,
		runID,
	)
	if err = platformdb.Migrate(ctx, database); err == nil {
		t.Fatal("migration bypassed the snapshot size constraint")
	}
	var definition, snapshot string
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, id).
		Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if err = database.QueryRow(`SELECT run_snapshot_json FROM automation_history WHERE id = ?`, runID).
		Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	if definition != legacy || snapshot != oversizedAfterUpgrade {
		t.Fatal("failed migration partially rewrote stored data")
	}
	var widened int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master
		WHERE name IN ('automations', 'automation_history') AND sql LIKE '%66080%'`).Scan(&widened); err != nil || widened != 0 {
		t.Fatalf("failed migration retained widened schema: %d, %v", widened, err)
	}
	assertScheduleMigrationIntegrity(t, database)
}
