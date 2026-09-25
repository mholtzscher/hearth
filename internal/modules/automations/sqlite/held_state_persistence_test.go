package sqlite_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Replacing a definition clears its transient holds in the same transaction;
// otherwise a pending hold could fire against a Trigger no longer in the record.
func TestReplaceAutomationClearsHeldStateRows(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, err := repository.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO automation_holds (
		automation_id, revision, trigger_id, last_receive_order, phase, started_at, due_at
	) VALUES (?, ?, 'held-trigger', 1, 'pending', ?, ?)`,
		string(record.ID), record.Revision,
		migrationTimestamp, "2026-09-01T00:01:00.000000000Z")

	if _, err = repository.ReplaceAutomation(ctx, record.ID, record.Revision, validDomainDefinition(t)); err != nil {
		t.Fatal(err)
	}
	var holdCount int
	if err = database.QueryRowContext(ctx,
		`SELECT count(*) FROM automation_holds WHERE automation_id = ?`, string(record.ID),
	).Scan(&holdCount); err != nil {
		t.Fatal(err)
	}
	if holdCount != 0 {
		t.Fatalf("replacement retained %d automation holds", holdCount)
	}
}

// A hold's Automation foreign key cascades on definition deletion, and the
// rebuilt history table leaves no broken foreign-key references.
func TestAutomationHoldsCascadeAndHistoryForeignKeysRemainValid(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, err := repository.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	var holdForeignKeyCount int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM pragma_foreign_key_list('automation_holds')`).Scan(
		&holdForeignKeyCount,
	); err != nil {
		t.Fatal(err)
	}
	if holdForeignKeyCount != 1 {
		t.Fatalf("automation_holds foreign keys = %d, want one FK to automations", holdForeignKeyCount)
	}
	if err = repository.DeleteAutomation(ctx, record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	var holdCount int
	if err = database.QueryRowContext(ctx, `SELECT count(*) FROM automation_holds`).Scan(&holdCount); err != nil {
		t.Fatal(err)
	}
	if holdCount != 0 {
		t.Fatalf("automation deletion retained %d holds", holdCount)
	}
	var foreignKeyViolations int
	if err = database.QueryRowContext(
		ctx, `SELECT count(*) FROM pragma_foreign_key_check`,
	).Scan(&foreignKeyViolations); err != nil {
		t.Fatal(err)
	}
	if foreignKeyViolations != 0 {
		t.Fatalf("foreign-key violations after migration = %d", foreignKeyViolations)
	}
}

// Held-state history retains the Trigger and scheduled interval in both detail
// and summary reads; missing or mis-mapped evidence would make the outcome
// indistinguishable from a manual Run.
func TestHeldStateHistoryMapsEvidence(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := validDomainDefinition(t)
	entityID := newEntityID(t)
	definition.Triggers = []automations.Trigger{{
		ID:   "held-trigger",
		Kind: automations.TriggerKindHeldState,
		HeldState: &automations.HeldStateTrigger{
			EntityID: entityID,
			Comparisons: []automations.ObservationComparison{{
				Operator: automations.ComparisonEqual,
				Operand:  json.RawMessage(`true`),
			}},
			ForSeconds: 60,
		},
	}}
	record, err := repository.CreateAutomation(ctx, definition)
	if err != nil {
		t.Fatal(err)
	}
	startedAt := time.Date(2026, 9, 23, 12, 0, 0, 0, time.UTC)
	dueAt := startedAt.Add(time.Minute)
	const runID = "arn_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	snapshot, err := automations.EncodeDefinition(definition)
	if err != nil {
		t.Fatal(err)
	}
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at,
		run_snapshot_json, run_source, run_status, run_started_at,
		run_matched_trigger_ids_json, hold_trigger_id, hold_started_at, hold_due_at,
		condition_decision_json
	) VALUES (?, ?, 'Office light', 'run', 1, ?, ?, 'held_state', 'running', ?,
		'["held-trigger"]', 'held-trigger', ?, ?, '{"mode":"not_configured","bypass_requested":false}')`,
		runID, string(record.ID), encodeStoredTimestamp(startedAt), string(snapshot),
		encodeStoredTimestamp(startedAt), encodeStoredTimestamp(startedAt), encodeStoredTimestamp(dueAt))

	entry := historyEntry(t, repository, record.ID, runID)
	if entry.Run == nil || entry.Run.HeldState == nil || entry.Run.HeldState.TriggerID != "held-trigger" ||
		!entry.Run.HeldState.StartedAt.Equal(startedAt) || !entry.Run.HeldState.DueAt.Equal(dueAt) {
		t.Fatalf("held-state Run evidence = %#v", entry.Run)
	}
	summary := firstHistorySummary(t, repository, record.ID)
	if summary.Source != automations.RunSourceHeldState || summary.HeldState == nil ||
		summary.HeldState.TriggerID != "held-trigger" || !summary.HeldState.DueAt.Equal(dueAt) {
		t.Fatalf("held-state history summary = %#v", summary)
	}
}

func encodeStoredTimestamp(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}
