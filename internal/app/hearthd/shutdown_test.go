package hearthd //nolint:testpackage // Whole-app shutdown regression uses the real process lifecycle.

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// A6: canceling the whole Core process must interrupt a 24h wait and commit its
// evidence before shutdown closes SQLite. No later Command may be created.
func TestCoreShutdownInterruptsLongAutomationDelay(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database := dbtest.OpenMigrated(t, databasePath)
	repository := automationssqlite.NewAutomationRepository(database, automations.Dependencies{})
	record, err := repository.CreateAutomation(ctx, automations.Definition{
		Name: "Long delay shutdown", Enabled: false,
		Triggers: []automations.Trigger{{ID: "schedule", Kind: automations.TriggerKindCron,
			Cron: &automations.CronTrigger{Expression: "* * * * *"}}},
		Steps: []automations.Step{
			{ID: "wait", Kind: automations.StepKindDelay, Delay: &automations.DelayStep{DurationMS: 86400000}},
			{ID: "later", EntityID: devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789e1"),
				OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`)},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := startLifecycleNATSServer(t)
	address, stopCore, runErrors := startDeviceFactsCore(ctx, t, server.ClientURL(), databasePath)
	defer stopCore()
	waitForCoreHealthz(ctx, t, address, runErrors)
	response := conditionsManualRun(ctx, t, address, string(record.ID), "")
	if response.StatusCode != http.StatusAccepted {
		defer response.Body.Close()
		t.Fatalf("manual admission = %d: %s", response.StatusCode, readSliceBody(t, response))
	}
	runID := conditionsLocationRunID(t, response)
	_ = response.Body.Close()
	waitForCoreRunningDelay(ctx, t, repository, record.ID, runID)
	stopCore()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatal(runErr)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("whole Core shutdown waited for the 24h timer")
	}
	entry, err := repository.GetHistoryEntry(ctx, record.ID, runID)
	if err != nil {
		t.Fatal(err)
	}
	run := entry.Run
	if run == nil || run.Status != automations.RunInterrupted || run.FailureCode == nil ||
		*run.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("shutdown Run = %#v", run)
	}
	if len(run.Delays) != 1 || run.Delays[0].Status != automations.DelayInterrupted ||
		run.Delays[0].FailureCode == nil ||
		*run.Delays[0].FailureCode != automations.FailureCoreStopping {
		t.Fatalf("shutdown delay = %#v", run.Delays)
	}
	if !run.CompletedAt.Equal(*run.Delays[0].CompletedAt) {
		t.Fatal("delay and parent interruption timestamps differ")
	}
	if len(run.Steps) != 1 || run.Steps[0].Status != automations.StepNotAttempted ||
		run.Steps[0].ReservedCommandID != nil {
		t.Fatalf("later Command attempt = %#v", run.Steps)
	}
	var commands int
	if err = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM commands").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if commands != 0 {
		t.Fatal("Core executed a Command after stopping the wait")
	}
}

// Observe production persistence before stopping, not merely worker launch.
func waitForCoreRunningDelay(
	ctx context.Context,
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	id automations.AutomationID,
	runID string,
) {
	t.Helper()
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for {
		entry, err := repository.GetHistoryEntry(ctx, id, runID)
		if err != nil {
			t.Fatal(err)
		}
		if entry.Run != nil && len(entry.Run.Delays) == 1 && entry.Run.Delays[0].Status == automations.DelayRunning {
			return
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Core did not start the long delay")
		}
	}
}
