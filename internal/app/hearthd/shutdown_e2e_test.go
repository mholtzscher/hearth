//go:build e2e

package hearthd //nolint:testpackage // Whole-app shutdown regression uses the real process lifecycle.

import (
	"context"
	"encoding/json"
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
		Triggers: []automations.Trigger{{ID: "schedule", Body: automations.CronTrigger{Expression: "* * * * *"}}},
		Steps: []automations.Step{
			{ID: "wait", Body: automations.DelayStep{DurationMS: 86400000}},
			{ID: "later", Body: automations.CommandStep{
				EntityID:      devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-0123456789e1"),
				OperationName: devices.OperationNameSet, Parameters: devices.CommandParameters(`{"value":true}`)}},
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
	assertCoreDelayAdmission(t, response)
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
	run, ok := entry.(automations.Run)
	if !ok {
		t.Fatalf("shutdown history = %#v", entry)
	}
	completed, ok := run.State.(automations.CompletedRun)
	if !ok {
		t.Fatalf("shutdown Run = %#v", run)
	}
	interrupted, ok := completed.Outcome.(automations.InterruptedRun)
	if !ok || interrupted.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("shutdown Run = %#v", run)
	}
	if len(run.Executions) != 1 {
		t.Fatalf("reached executions after shutdown = %#v", run.Executions)
	}
	delay, ok := run.Executions[0].Body.(automations.DelayExecutionBody)
	if !ok || delay.CompletedAt == nil || delay.FailureCode == nil ||
		*delay.FailureCode != automations.FailureCoreStopping {
		t.Fatalf("shutdown delay execution = %#v", run.Executions[0])
	}
	if !completed.CompletedAt.Equal(*delay.CompletedAt) {
		t.Fatal("delay and parent interruption timestamps differ")
	}
	var commands int
	if err = database.QueryRowContext(ctx, "SELECT COUNT(*) FROM commands").Scan(&commands); err != nil {
		t.Fatal(err)
	}
	if commands != 0 {
		t.Fatal("Core executed a Command after stopping the wait")
	}
}

func assertCoreDelayAdmission(t *testing.T, response *http.Response) {
	t.Helper()
	var admitted struct {
		Cause struct {
			Kind string `json:"kind"`
		} `json:"cause"`
		Snapshot json.RawMessage `json:"snapshot"`
	}
	if err := json.NewDecoder(response.Body).Decode(&admitted); err != nil {
		t.Fatal(err)
	}
	if admitted.Cause.Kind != "manual" {
		t.Fatalf("admission cause = %#v", admitted.Cause)
	}
	snapshot, err := automations.DecodeDefinition(admitted.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Steps) != 2 || snapshot.Steps[0].Kind() != automations.StepKindDelay ||
		snapshot.Steps[1].Kind() != automations.StepKindCommand {
		t.Fatalf("admission snapshot = %#v", snapshot)
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
		if run, ok := entry.(automations.Run); ok {
			for _, execution := range run.Executions {
				if delay, isDelay := execution.Body.(automations.DelayExecutionBody); isDelay &&
					delay.CompletedAt == nil {
					return
				}
			}
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("Core did not start the long delay")
		}
	}
}
