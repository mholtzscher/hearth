package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

const (
	seededRunningAutomationRunID = "arn_01890f47-7a6b-7c4d-8e9f-0123456789d1"
	seededAutomationID           = "aut_01890f47-7a6b-7c4d-8e9f-0123456789e1"
)

// TestCoreStartupInterruptsRunningAutomationRuns protects A7/A13 at the app
// boundary: a Run left running by a previous process must be classified
// interrupted with the restart reason during startup, before Core serves or
// consumes, and stay terminal. It fails if the startup automation interruption
// is dropped, ordered after NATS, or silently replays or invents success.
func TestCoreStartupInterruptsRunningAutomationRuns(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	seedRunningAutomationRun(ctx, t, databasePath)
	server := startLifecycleNATSServer(t)
	httpAddress := unusedLoopbackAddress(t)

	runContext, stopCore := context.WithCancel(ctx)
	defer stopCore()
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{HouseholdTimezone: "UTC",
			HTTPAddr: httpAddress, NATSURL: server.ClientURL(), SQLitePath: databasePath,
			Agent: requiredAgentConfig(t),
		}, slog.New(slog.DiscardHandler))
	}()
	// /healthz is served only after startup interruptions commit, so a 200
	// proves the classification ran before Core accepted traffic.
	waitForCoreHealthz(ctx, t, httpAddress, runErrors)
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("hearthd did not stop")
	}

	status, failureCode, completedAt := readAutomationRunOutcome(
		ctx, t, databasePath, seededRunningAutomationRunID,
	)
	if status != string(automations.RunInterrupted) {
		t.Fatalf("run status after restart = %q, want %q", status, automations.RunInterrupted)
	}
	if failureCode != automations.FailureCoreRestarted {
		t.Fatalf("run failure code = %q, want %q", failureCode, automations.FailureCoreRestarted)
	}
	if !completedAt.Valid || completedAt.String == "" {
		t.Fatal("interrupted run has no completion time")
	}
}

// seedRunningAutomationRun writes one running Run as a previous process would
// have left it. The Run is manual so no Fact summary is required.
func seedRunningAutomationRun(ctx context.Context, t *testing.T, databasePath string) {
	t.Helper()
	database := dbtest.OpenMigrated(t, databasePath)
	defer func() { _ = database.Close() }()
	startedAt := time.Now().UTC().Add(-time.Hour).Format("2006-01-02T15:04:05.000000000Z")
	if _, execErr := database.ExecContext(ctx, `
		INSERT INTO automation_history (
			id, automation_id, automation_name, kind, revision, recorded_at,
			run_snapshot_json, run_source, run_status, run_started_at,
			run_matched_trigger_ids_json, condition_decision_json
		) VALUES (?, ?, 'Interrupted on restart', 'run', 1, ?,
			'{"name":"Interrupted on restart","enabled":true,"triggers":[],"steps":[]}',
			'manual', 'running', ?, '[]',
			'{"mode":"not_configured","bypass_requested":false}')`,
		seededRunningAutomationRunID, seededAutomationID, startedAt, startedAt,
	); execErr != nil {
		t.Fatal(execErr)
	}
}

// readAutomationRunOutcome reads the terminal classification of one seeded Run.
func readAutomationRunOutcome(
	ctx context.Context,
	t *testing.T,
	databasePath string,
	runID string,
) (string, string, sql.NullString) {
	t.Helper()
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var status string
	var failureCode sql.NullString
	var completedAt sql.NullString
	if queryErr := database.QueryRowContext(ctx, `
		SELECT run_status, run_failure_code, run_completed_at
		FROM automation_history WHERE id = ?`, runID,
	).Scan(&status, &failureCode, &completedAt); queryErr != nil {
		t.Fatal(queryErr)
	}
	return status, failureCode.String, completedAt
}
