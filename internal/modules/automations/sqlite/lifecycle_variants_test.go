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
	if err = repository.CompleteStep(ctx, automations.StepCompletion{
		RunID: run.ID, Position: 0, Outcome: automations.InterruptedStep{FailureCode: automations.FailureCoreStopping},
	}); err != nil {
		t.Fatal(err)
	}
	if err = repository.CompleteRun(ctx, automations.RunCompletion{
		RunID: run.ID, Outcome: automations.InterruptedRun{FailureCode: automations.FailureCoreStopping},
	}); err != nil {
		t.Fatal(err)
	}
	entry, err := repository.GetHistoryEntry(ctx, record.ID, string(run.ID))
	if err != nil {
		t.Fatal(err)
	}
	retained := entry.(automations.Run)
	completed, ok := retained.Steps[0].State.(automations.CompletedStep)
	if !ok || completed.Reservation != nil || completed.StartedAt.IsZero() ||
		!completed.CompletedAt.Equal(completed.StartedAt) {
		t.Fatalf("pre-reservation interruption = %#v", retained.Steps[0].State)
	}
	outcome, ok := completed.Outcome.(automations.InterruptedStep)
	if !ok || outcome.FailureCode != automations.FailureCoreStopping || outcome.VerifiedCommandID != nil {
		t.Fatalf("interrupted outcome = %#v", completed.Outcome)
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
				`UPDATE automation_run_steps SET reserved_command_id = ?, reserved_correlation_id = ? WHERE run_id = ?`,
				test.command,
				test.correlation,
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
