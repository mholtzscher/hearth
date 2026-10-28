//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"

	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// Historical JSON is written directly: using today's encoder here would hide
// the upgrade fault. Upgrade must preserve definitions and retained Run evidence
// while making both readable by the strict current codec.
// Historical preservation stops at v20; v21 intentionally resets Automations.
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
	mustExec(t, database, insertHistoricalHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`, legacy, runID)
	for position, stepID := range []string{"first", "second"} {
		mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
			VALUES (?, ?, ?, 'not_attempted')`, runID, position, stepID)
	}
	if _, err = provider.UpTo(ctx, 20); err != nil {
		t.Fatal(err)
	}
	assertHistoricalCommandMetadata(t, database, id, runID, []string{"first", "second"})
	// Independent JSON comparison permits only the new discriminator; neither
	// parameter values nor other fields may be rewritten by the migration.
	var migrated string
	if err = database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, id).
		Scan(&migrated); err != nil {
		t.Fatal(err)
	}
	var want, got map[string]any
	if err = json.Unmarshal([]byte(legacy), &want); err != nil {
		t.Fatal(err)
	}
	for _, step := range want["steps"].([]any) {
		step.(map[string]any)["kind"] = "command"
	}
	if err = json.Unmarshal([]byte(migrated), &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("migration changed legacy document beyond command kinds: %s", migrated)
	}
	var snapshot string
	if err = database.QueryRow(`SELECT run_snapshot_json FROM automation_history WHERE id = ?`, runID).
		Scan(&snapshot); err != nil ||
		snapshot != migrated {
		t.Fatalf("upgraded snapshot = %q, %v", snapshot, err)
	}
	validateHistoricalDefinition(t, migrated)
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
	// Exercise only historical migrations; v21 intentionally resets Automations.
	if _, err = provider.UpTo(ctx, 20); err != nil {
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
	if _, err = provider.UpTo(ctx, 20); err != nil {
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
	validateHistoricalDefinition(t, repeated)
}

func validateHistoricalDefinition(t *testing.T, raw string) {
	t.Helper()
	schemaBytes, err := os.ReadFile("../../../platform/db/automation-definition-v2.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(schemaBytes)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("urn:fixture:v2", doc); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("urn:fixture:v2")
	if err != nil {
		t.Fatal(err)
	}
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err = schema.Validate(value); err != nil {
		t.Fatalf("upgraded v2 definition: %v", err)
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
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 13); err != nil {
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
	mustExec(t, database, insertHistoricalHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(
		t,
		database,
		`UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`,
		oversizedAfterUpgrade,
		runID,
	)
	if _, err = provider.UpTo(ctx, 20); err == nil {
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
