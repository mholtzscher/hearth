//go:build integration

package sqlite_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

// A valid legacy document at the old byte limit must remain readable after
// adding every required Command discriminator, including its retained snapshot.
func TestCommandKindUpgradePreservesMaximumSizeDefinitions(t *testing.T) {
	t.Parallel()
	for _, commands := range []int{1, 32} {
		t.Run(strconv.Itoa(commands), func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "maximum.db"))
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = database.Close() })
			provider := scheduleMigrationProvider(t, database)
			if _, err = provider.UpTo(ctx, 13); err != nil {
				t.Fatal(err)
			}
			id, runID := newAutomationIDString(t), newRunIDString(t)
			legacy := maximumLegacyCommandDefinition(t, commands)
			mustExec(t, database, `INSERT INTO automations (id, revision, definition_json, created_at, updated_at)
				VALUES (?, 4, ?, ?, ?)`, id, legacy, migrationTimestamp, migrationTimestamp)
			mustExec(t, database, insertHistoryRunSQL, runID, id, "succeeded", nil, migrationTimestamp, "[]")
			mustExec(t, database, `UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`, legacy, runID)
			for position := range commands {
				mustExec(t, database, `INSERT INTO automation_run_steps (run_id, position, step_id, status)
					VALUES (?, ?, ?, 'not_attempted')`, runID, position, fmt.Sprintf("set%d", position))
			}
			if err = platformdb.Migrate(ctx, database); err != nil {
				t.Fatalf("upgrade valid maximum-size legacy document: %v", err)
			}
			assertMaximumCommandUpgrade(t, database, commands, id, runID, legacy)
			assertScheduleMigrationIntegrity(t, database)
			if err = platformdb.Migrate(ctx, database); err != nil {
				t.Fatalf("repeat upgrade: %v", err)
			}
		})
	}
}

func assertMaximumCommandUpgrade(t *testing.T, database *sql.DB, commands int, id, runID, legacy string) {
	t.Helper()
	var definition, snapshot string
	if err := database.QueryRow(`SELECT definition_json FROM automations WHERE id = ?`, id).
		Scan(&definition); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT run_snapshot_json FROM automation_history WHERE id = ?`, runID).
		Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	// SQLite adds only these fields; stripping them must recover exact bytes,
	// including parameter numbers larger than JavaScript's safe integer range.
	if len(definition) != 65536+17*commands || snapshot != definition ||
		strings.ReplaceAll(definition, `,"kind":"command"`, "") != legacy {
		t.Fatal("upgrade changed more than Command discriminators")
	}
	at := time.Now().UTC()
	repository := scheduleRepo(database, &at)
	record, err := repository.GetAutomation(t.Context(), automations.AutomationID(id))
	if err != nil || record.Revision != 4 || record.Definition.Enabled || record.CreatedAt != record.UpdatedAt {
		t.Fatalf("read upgraded definition metadata: %#v, %v", record, err)
	}
	entry, err := repository.GetHistoryEntry(t.Context(), automations.AutomationID(id), runID)
	if err != nil {
		t.Fatalf("read upgraded snapshot: %v", err)
	}
	run, ok := entry.(automations.Run)
	if !ok || len(run.Steps) != commands || len(automations.CommandLeaves(run.Snapshot.Steps)) != commands {
		t.Fatalf("upgrade lost retained Command evidence: %#v", entry)
	}
	if commands == 32 {
		oversized := strings.Replace(definition, `"padding":"`, `"padding":"x`, 1)
		if _, err = database.Exec(
			`UPDATE automations SET definition_json = ? WHERE id = ?`,
			oversized,
			id,
		); err == nil {
			t.Fatal("definition storage accepted one byte beyond the new bound")
		}
		if _, err = database.Exec(
			`UPDATE automation_history SET run_snapshot_json = ? WHERE id = ?`,
			oversized,
			runID,
		); err == nil {
			t.Fatal("snapshot storage accepted one byte beyond the new bound")
		}
	}
}

func maximumLegacyCommandDefinition(t *testing.T, commands int) string {
	t.Helper()
	entity := newEntityID(t)
	steps := make([]string, 0, commands)
	for position := range commands {
		steps = append(steps, fmt.Sprintf(
			`{"id":"set%d","entity_id":%q,"operation":"set","parameters":{"precise":9007199254740993,"padding":""}}`,
			position, entity,
		))
	}
	raw := fmt.Sprintf(
		`{"name":"Maximum legacy","enabled":false,"triggers":[{"id":"press","kind":"entity_event","entity_id":%q,"event_name":"single_press"}],"steps":[%s]}`,
		entity,
		strings.Join(steps, ","),
	)
	return strings.Replace(raw, `"padding":""`, `"padding":"`+strings.Repeat("x", 65536-len(raw))+`"`, 1)
}

// Both parent rebuilds preserve every cascading child present in their input schema.
func TestAutomationParentRebuildsPreserveCascadingEvidence(t *testing.T) {
	t.Parallel()
	for _, version := range []int64{14, 17} {
		t.Run(strconv.FormatInt(version, 10), func(t *testing.T) {
			t.Parallel()
			assertAutomationParentRebuild(t, version)
		})
	}
}

func assertAutomationParentRebuild(t *testing.T, version int64) {
	t.Helper()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "current.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, version-1); err != nil {
		t.Fatal(err)
	}
	repository := newAutomationRepository(t, database)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), at)
	mustExec(t, database, `INSERT INTO automation_holds
		(automation_id, revision, trigger_id, last_receive_order, phase) VALUES (?, 1, 'hold', 1, 'idle')`, string(record.ID))
	mustExec(t, database, `INSERT INTO automation_run_branch_decisions
		(run_id, step_id, position, decision_json) VALUES (?, 'route', 0, '{}')`, string(run.ID))
	mustExec(t, database, `INSERT INTO automation_run_delays
		(run_id, step_id, position, status, started_at) VALUES (?, 'wait', 0, 'running', ?)`, string(run.ID), migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_run_notifications
		(run_id, step_id, position, attempt_id, status, started_at) VALUES (?, 'notify', 0, 'retained-attempt', 'running', ?)`,
		string(run.ID), migrationTimestamp)
	tables := snapshotAutomationCascadeRows(t, database)
	if _, err = provider.UpTo(ctx, version); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	assertScheduleMigrationIntegrity(t, database)
	// Down deliberately retains the allowance and canonical JSON; a repeated Up
	// must still preserve data, constraints, indexes, and schedule triggers.
	if _, err = provider.Down(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = provider.UpTo(ctx, version); err != nil {
		t.Fatal(err)
	}
	for _, table := range tables {
		assertMigrationRowsUnchanged(t, database, table)
	}
	var triggers int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'trigger'
		AND name IN ('automation_schedule_history_insert', 'automation_schedule_history_update')`).Scan(&triggers); err != nil || triggers != 2 {
		t.Fatalf("schedule triggers = %d, %v", triggers, err)
	}
	assertScheduleMigrationIntegrity(t, database)
}
