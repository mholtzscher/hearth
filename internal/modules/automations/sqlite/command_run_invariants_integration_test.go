//go:build integration

package sqlite_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

func TestCommandEvidenceGuardsRunCompletionAndHistory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		command automations.StepOutcome
		outcome automations.RunOutcome
	}{
		{"running under success", nil, automations.SucceededRun{}},
		{"running under failure", nil, automations.FailedRun{FailureCode: "command_failed"}},
		{"running under interruption", nil, automations.InterruptedRun{FailureCode: automations.FailureCoreStopping}},
		{"failed under success", automations.FailedStep{FailureCode: "command_failed"}, automations.SucceededRun{}},
		{"interrupted under success", automations.InterruptedStep{FailureCode: automations.FailureCoreStopping}, automations.SucceededRun{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
			reserveScopedStorageCommand(t, repository, run.ID, 0)
			if test.command != nil {
				if err := repository.CompleteStep(
					t.Context(),
					automations.StepCompletion{RunID: run.ID, Outcome: test.command},
				); err != nil {
					t.Fatal(err)
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			if err := repository.CompleteRun(t.Context(), automations.RunCompletion{
				RunID: run.ID, Outcome: test.outcome,
			}); !errors.Is(err, automations.ErrInvalidAutomation) {
				t.Errorf("contradictory completion accepted: %v", err)
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Error("rejected completion changed retained evidence")
			}
			// Simulate contradictory stored data independently of the completion guard.
			mustExec(
				t,
				database,
				`UPDATE automation_history SET run_status = ?, run_failure_code = ?, run_completed_at = ? WHERE id = ?`,
				string(automations.RunOutcomeStatus(test.outcome)),
				automations.RunOutcomeFailureCode(test.outcome),
				migrationTimestamp,
				string(run.ID),
			)
			if _, err := repository.GetHistoryEntry(
				t.Context(),
				record.ID,
				string(run.ID),
			); !errors.Is(
				err,
				automations.ErrInvalidAutomation,
			) {
				t.Errorf("contradictory history accepted: %v", err)
			}
		})
	}
}

func TestSuccessfulRunRetainsUnselectedCommandLeaves(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	definition := validDomainDefinition(t)
	selected := definition.Steps[0]
	unselected := selected
	unselected.ID = "unselected"
	definition.Steps = []automations.Step{selected, unselected}
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	commandID := reserveScopedStorageCommand(t, repository, run.ID, 0)
	if err := repository.CompleteStep(t.Context(), automations.StepCompletion{
		RunID: run.ID, Outcome: automations.DispatchedStep{VerifiedCommandID: commandID},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	got := retainedStorageRun(t, repository, record, run)
	if automations.RunStateStatus(got.State) != automations.RunSucceeded || len(got.Steps) != 2 ||
		automations.StepAttemptStatus(got.Steps[0].State) != automations.StepDispatched ||
		automations.StepAttemptStatus(got.Steps[1].State) != automations.StepNotAttempted {
		t.Fatalf("successful selected path lost Command evidence: %#v", got)
	}
	if err := repository.CompleteStep(t.Context(), automations.StepCompletion{
		RunID: run.ID, Position: 1, Outcome: automations.InterruptedStep{FailureCode: automations.FailureCoreStopping},
	}); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Fatalf("terminal parent accepted late Command interruption: %v", err)
	}
	if after := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(after, got) {
		t.Fatal("late completion changed terminal Run evidence")
	}
}
