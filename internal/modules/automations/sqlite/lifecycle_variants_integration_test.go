//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

//nolint:paralleltest,tparallel // Corruption probes mutate the same retained row sequentially.
func TestCompletedAttemptWithoutReservationAndPartialReservationCorruption(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	ctx := context.Background()
	record, err := repository.CreateAutomation(ctx, validDomainDefinition(t))
	if err != nil {
		t.Fatal(err)
	}
	admission, err := repository.AdmitManualRun(
		ctx,
		automations.ManualRunInput{AutomationID: record.ID},
		devices.EntityStateSnapshot{},
		time.Now().UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	run, ok := admission.(automations.Run)
	if !ok {
		t.Fatalf("manual admission = %T, want Run", admission)
	}
	initializeStorageRun(t, repository, run)
	if err = recordStorageStep(ctx, repository, run.ID, "light_on", automations.CommandStopped{
		At: run.StartedAt, FailureCode: automations.FailureCoreStopping,
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := repository.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	completed, ok := retained.Executions[0].Details.(automations.CommandExecutionDetails)
	interrupted, interruptedOK := completed.State.(automations.CommandInterrupted)
	if !ok || !interruptedOK || interrupted.Reservation != nil || completed.StartedAt.IsZero() ||
		!interrupted.CompletedAt.Equal(completed.StartedAt) {
		t.Fatalf("pre-reservation interruption = %#v", retained.Executions[0].Details)
	}
	if interrupted.FailureCode != automations.FailureCoreStopping || interrupted.VerifiedCommandID != nil {
		t.Fatalf("interrupted state = %#v", interrupted)
	}
	terminal, ok := retained.State.(automations.CompletedRun)
	if !ok || automations.RunOutcomeStatus(terminal.Outcome) != automations.RunInterrupted {
		t.Fatalf("Run state = %#v", retained.State)
	}

	commandID, err := devices.NewCommandID()
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name        string
		command     any
		correlation any
	}{
		{name: "Command only", command: string(commandID)},
		{name: "correlation only", correlation: string(correlationID)},
	} {
		t.Run(test.name, func(t *testing.T) {
			mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
			mustExec(
				t,
				database,
				`UPDATE automation_step_executions SET evidence_json = ? WHERE run_id = ? AND step_id = 'light_on'`,
				map[bool]string{true: `{"command_id":"` + string(commandID) + `"}`, false: `{"correlation_id":"` + string(correlationID) + `"}`}[test.command != nil],
				string(run.ID),
			)
			mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
			if _, readErr := repository.GetHistoryEntry(
				ctx,
				record.ID,
				string(run.ID),
			); !errors.Is(
				readErr,
				automations.ErrInvalidAutomation,
			) {
				t.Fatalf("partial reservation error = %v, want invalid Automation", readErr)
			}
		})
	}
}
