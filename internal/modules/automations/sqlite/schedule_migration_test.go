package sqlite_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pressly/goose/v3"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
)

func scheduleMigrationProvider(t *testing.T, database *sql.DB) *goose.Provider {
	t.Helper()
	provider, err := goose.NewProvider(goose.DialectSQLite3, database, os.DirFS("../../../platform/db/migrations"))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func assertScheduleMigrationIntegrity(t *testing.T, database *sql.DB) {
	t.Helper()
	var enabled, violations, indexes int
	if err := database.QueryRow(`PRAGMA foreign_keys`).Scan(&enabled); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM pragma_foreign_key_check`).Scan(&violations); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN
		('automation_history_page_idx', 'automation_history_one_running_run_idx', 'automation_history_fact_outcome_idx')`).Scan(&indexes); err != nil {
		t.Fatal(err)
	}
	if enabled != 1 || violations != 0 || indexes != 3 {
		t.Fatalf("FK enabled=%d violations=%d indexes=%d", enabled, violations, indexes)
	}
	// Enforcement must still reject a newly orphaned Step, not merely report no old violations.
	if _, err := database.Exec(`INSERT INTO automation_run_steps (run_id, position, step_id, status)
		VALUES (?, 0, 'orphan', 'not_attempted')`, newRunIDString(t)); err == nil {
		t.Fatal("FK enforcement accepted an orphaned Step")
	}
}

// A13: migration 8 history and every Step column survive both rebuilds without disabling FKs.
//
//nolint:gocognit // One ordered upgrade/downgrade sequence protects retained rows at both rebuilds.
func TestScheduleMigrationPreservesOlderHistoryAndSteps(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "upgrade.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	provider := scheduleMigrationProvider(t, database)
	if _, err = provider.UpTo(ctx, 8); err != nil {
		t.Fatal(err)
	}
	manualID, factID, heldID := newRunIDString(t), newRunIDString(t), newRunIDString(t)
	automationID := newAutomationIDString(t)
	mustExec(t, database, insertHistoryRunSQL, manualID, automationID, "succeeded", nil, migrationTimestamp, "[]")
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at, fact_id, fact_family,
		fact_entity_id, fact_variant, fact_causation_id, fact_value_json, fact_emitted_at,
		fact_previous_value_json, run_snapshot_json, run_source, run_status, run_started_at,
		run_completed_at, run_matched_trigger_ids_json, condition_decision_json)
		VALUES (?, ?, 'Fact', 'run', 1, ?, ?, 'observation', ?, 'applied', ?, 'true', ?, 'false',
		'{}', 'device_fact', 'succeeded', ?, ?, '["fact"]', '{"mode":"not_configured","bypass_requested":false}')`,
		factID, automationID, migrationTimestamp, newFactIDString(t), string(newEntityID(t)), newObservationIDString(t),
		migrationTimestamp, migrationTimestamp, migrationTimestamp)
	mustExec(t, database, `INSERT INTO automation_history (
		id, automation_id, automation_name, kind, revision, recorded_at, run_snapshot_json,
		run_source, run_status, run_started_at, run_completed_at, run_matched_trigger_ids_json,
		hold_trigger_id, hold_started_at, hold_due_at, condition_decision_json)
		VALUES (?, ?, 'Held', 'run', 1, ?, '{}', 'held_state', 'succeeded', ?, ?, '["held"]',
		'held', ?, '2026-09-01T00:01:00.000000000Z', '{"mode":"not_configured","bypass_requested":false}')`,
		heldID, automationID, migrationTimestamp, migrationTimestamp, migrationTimestamp, migrationTimestamp)
	commandID, correlationID := newCommandIDString(t), newCorrelationIDString(t)
	for _, runID := range []string{manualID, factID, heldID} {
		mustExec(t, database, `INSERT INTO automation_run_steps (
			run_id, position, step_id, status, reserved_command_id, reserved_correlation_id,
			verified_command_id, started_at, completed_at) VALUES (?, 0, 'step', 'satisfied', ?, ?, ?, ?, ?)`,
			runID, commandID, correlationID, commandID, migrationTimestamp, migrationTimestamp)
	}
	// Independent retained-row snapshots compare every column, not just row counts.
	//nolint:unqueryvet // Preserve every column to detect migration data loss, including future columns.
	mustExec(t, database, `CREATE TEMP TABLE expected_history AS SELECT * FROM automation_history`)
	//nolint:unqueryvet // Preserve every Step column, not an implementation-selected subset.
	mustExec(t, database, `CREATE TEMP TABLE expected_steps AS SELECT * FROM automation_run_steps`)
	for _, direction := range []string{"up", "down"} {
		if direction == "up" {
			_, err = provider.UpTo(ctx, 9)
		} else {
			_, err = provider.Down(ctx)
		}
		if err != nil {
			t.Fatal(err)
		}
		for _, table := range []struct{ current, expected string }{{"automation_history", "expected_history"}, {"automation_run_steps", "expected_steps"}} {
			var differences int
			if err = database.QueryRow(`SELECT count(*) FROM (SELECT * FROM ` + table.current + ` EXCEPT SELECT * FROM ` + table.expected + `)`).
				Scan(&differences); err != nil {
				t.Fatal(err)
			}
			if differences != 0 {
				t.Fatalf("%s changed %s rows", direction, table.current)
			}
			if err = database.QueryRow(`SELECT count(*) FROM (SELECT * FROM ` + table.expected + ` EXCEPT SELECT * FROM ` + table.current + `)`).
				Scan(&differences); err != nil {
				t.Fatal(err)
			}
			if differences != 0 {
				t.Fatalf("%s lost %s rows", direction, table.current)
			}
		}
		assertScheduleMigrationIntegrity(t, database)
	}
	var watermarkTables int
	if err = database.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name = 'automation_schedule_watermarks'`).
		Scan(&watermarkTables); err != nil {
		t.Fatal(err)
	}
	if watermarkTables != 0 {
		t.Fatal("down retained the watermark table")
	}
}

// A13: downgrade refusal is atomic for either Run or Skip schedule provenance.
//
//nolint:gocognit // One refusal scenario checks version, history, Steps, progress and FK enforcement.
func TestScheduleMigrationRefusesDowngradeWithoutMutation(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"run", "skip"} {
		t.Run(kind, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			at := scheduleTime(t, "2026-10-02T08:59:00Z")
			repository := scheduleRepo(database, &at)
			record, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *"))
			if err != nil {
				t.Fatal(err)
			}
			if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
				t.Fatal(err)
			}
			if kind == "skip" {
				if _, err = repository.AdmitManualRun(
					ctx,
					automations.ManualRunInput{AutomationID: record.ID},
					stateSnapshotWith(),
					at,
				); err != nil {
					t.Fatal(err)
				}
			}
			outcome := scheduleTick(t, repository, at.Add(time.Minute), time.UTC, stateSnapshotWith())
			if outcome.Outcome.MatchedAutomations != 1 {
				t.Fatalf("outcome = %#v", outcome)
			}
			//nolint:unqueryvet // Capture every retained history column before the refused downgrade.
			mustExec(t, database, `CREATE TEMP TABLE expected_history AS SELECT * FROM automation_history`)
			//nolint:unqueryvet // Capture every retained Step column before the refused downgrade.
			mustExec(t, database, `CREATE TEMP TABLE expected_steps AS SELECT * FROM automation_run_steps`)
			provider := scheduleMigrationProvider(t, database)
			if _, err = provider.DownTo(ctx, 9); err != nil {
				t.Fatal(err)
			}
			if _, err = provider.Down(ctx); err == nil {
				t.Fatal("schedule downgrade succeeded")
			}
			version, err := provider.GetDBVersion(ctx)
			if err != nil || version != 9 {
				t.Fatalf("refused downgrade version = %d, %v", version, err)
			}
			var changed int
			if err = database.QueryRow(`SELECT count(*) FROM (SELECT * FROM automation_history EXCEPT SELECT * FROM expected_history)`).
				Scan(&changed); err != nil {
				t.Fatal(err)
			}
			if changed != 0 {
				t.Fatal("refused downgrade changed history")
			}
			if err = database.QueryRow(`SELECT count(*) FROM (SELECT * FROM expected_history EXCEPT SELECT * FROM automation_history)`).
				Scan(&changed); err != nil {
				t.Fatal(err)
			}
			if changed != 0 {
				t.Fatal("refused downgrade lost history")
			}
			var steps, expectedSteps int
			if err = database.QueryRow(`SELECT count(*) FROM automation_run_steps`).Scan(&steps); err != nil {
				t.Fatal(err)
			}
			if err = database.QueryRow(`SELECT count(*) FROM expected_steps`).Scan(&expectedSteps); err != nil {
				t.Fatal(err)
			}
			if steps != expectedSteps {
				t.Fatal("refused downgrade lost Steps")
			}
			assertWatermark(t, database, at.Add(time.Minute))
			assertScheduleMigrationIntegrity(t, database)
		})
	}
}

// SQL and detail mapping independently reject schedule evidence and provenance contradictions.
func TestScheduleHistoryRejectsInvalidProvenance(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database := openAutomationDatabase(t)
	at := scheduleTime(t, "2026-10-02T08:59:00Z")
	repository := scheduleRepo(database, &at)
	record, err := repository.CreateAutomation(ctx, scheduleDefinition(t, "* * * * *"))
	if err != nil {
		t.Fatal(err)
	}
	if err = repository.InitializeScheduleWatermark(ctx, at); err != nil {
		t.Fatal(err)
	}
	run := scheduleTick(t, repository, at.Add(time.Minute), time.UTC, stateSnapshotWith()).StartedRuns[0]
	skip := scheduleTick(t, repository, at.Add(2*time.Minute), time.UTC, stateSnapshotWith()).Skips[0]
	tooMany := `["a"` + strings.Repeat(`,"a"`, 32) + `]`
	for _, fixture := range []struct {
		id, assignment string
		args           []any
	}{
		{string(run.ID), `run_matched_trigger_ids_json = '[]'`, nil},
		{string(run.ID), `run_matched_trigger_ids_json = ?`, []any{tooMany}},
		{string(run.ID), `run_matched_trigger_ids_json = '["unknown"]'`, nil},
		{string(run.ID), `fact_previous_value_json = 'false'`, nil},
		{string(run.ID), `fact_id = ?`, []any{newFactIDString(t)}},
		{string(run.ID), `hold_trigger_id = 'a', hold_started_at = ?, hold_due_at = ?`, []any{migrationTimestamp, "2026-09-01T00:01:00.000000000Z"}},
		{string(run.ID), `condition_mode = 'bypassed', condition_bypassed = 1`, nil},
		{string(run.ID), `condition_decision_json = '{"mode":"bypassed","bypass_requested":true}'`, nil},
		{string(skip.SkipID), `skip_reason = 'stale_fact'`, nil},
		{string(skip.SkipID), `skip_matched_triggers_json = '[]'`, nil},
		{string(skip.SkipID), `skip_matched_triggers_json = ?`, []any{matchedTriggerJSON(t)}},
		{string(skip.SkipID), `skip_reason = 'conditions_false'`, nil},
	} {
		args := append([]any{}, fixture.args...)
		args = append(args, fixture.id)
		if _, err = database.ExecContext(
			ctx,
			`UPDATE automation_history SET `+fixture.assignment+` WHERE id = ?`,
			args...); err == nil {
			t.Fatalf("invalid schedule provenance accepted: %s", fixture.assignment)
		}
	}
	// Bypass only CHECK constraints to simulate a damaged on-disk row. Read mapping
	// must still reject absent-evidence violations, without a test-only production seam.
	mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
	mustExec(
		t,
		database,
		`UPDATE automation_history SET fact_previous_value_json = 'false' WHERE id = ?`,
		string(run.ID),
	)
	mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
	if _, err = repository.GetHistoryEntry(
		ctx,
		record.ID,
		string(run.ID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("corrupt schedule detail = %v", err)
	}
	if _, err = repository.ListHistory(
		ctx,
		automations.ListHistoryParams{AutomationID: record.ID, Limit: 100},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("corrupt schedule summary = %v", err)
	}
}
