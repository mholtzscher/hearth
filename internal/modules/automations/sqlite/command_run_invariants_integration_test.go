//go:build integration

package sqlite_test

import (
	"database/sql"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestCommandEvidenceGuardsRunCompletionAndHistory(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		kind string
		want automations.RunStatus
	}{
		{"running cannot terminalize", "running", automations.RunRunning},
		{"failure terminalizes parent atomically", "failed", automations.RunFailed},
		{"interruption terminalizes parent atomically", "interrupted", automations.RunInterrupted},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			assertCommandEvidenceGuardScenario(t, test.kind, test.want)
		})
	}
}

func assertCommandEvidenceGuardScenario(t *testing.T, kind string, want automations.RunStatus) {
	t.Helper()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	reservation := reserveScopedStorageCommand(t, repository, run.ID, 0)
	if kind != "running" {
		completedAt := time.Now().UTC()
		var state automations.CommandTerminalState
		switch kind {
		case "failed":
			state = automations.CommandFailed{
				CompletedAt: completedAt,
				Reservation: reservation,
				FailureCode: "command_failed",
			}
		case "interrupted":
			state = automations.CommandInterrupted{
				CompletedAt: completedAt,
				Reservation: &reservation,
				FailureCode: automations.FailureCoreStopping,
			}
		}
		if err := completeStorageStep(t.Context(), repository, run.ID, "light_on", automations.CommandFinished{
			State: state,
		}); err != nil {
			t.Fatal(err)
		}
	} else {
		assertActiveCommandCannotCompleteRun(t, repository, run.ID)
	}
	retained := retainedStorageRun(t, repository, record, run)
	if got := automations.RunStateStatus(retained.State); got != want {
		t.Fatalf("Run status = %s, want %s", got, want)
	}
	if kind != "running" {
		assertCommandRunCompletionIsImmutable(t, repository, run.ID)
	}
	assertMalformedRunHistoryRejected(t, database, repository, record.ID, run.ID)
}

func assertActiveCommandCannotCompleteRun(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	runID automations.RunID,
) {
	t.Helper()
	if err := repository.CompleteRun(t.Context(), automations.RunCompletion{
		RunID: runID, Outcome: automations.InterruptedRun{FailureCode: automations.FailureCoreStopping},
	}); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Errorf("active Command parent completion = %v, want rejection", err)
	}
}

func assertCommandRunCompletionIsImmutable(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	runID automations.RunID,
) {
	t.Helper()
	if err := repository.CompleteRun(t.Context(), automations.RunCompletion{
		RunID: runID, Outcome: automations.SucceededRun{},
	}); !errors.Is(err, automations.ErrInvalidAutomation) {
		t.Errorf("contradictory completion accepted: %v", err)
	}
}

func assertMalformedRunHistoryRejected(
	t *testing.T,
	database *sql.DB,
	repository *automationssqlite.AutomationRepository,
	automationID automations.AutomationID,
	runID automations.RunID,
) {
	t.Helper()
	// Corrupt the parent independently of the transactional completion guard.
	mustExec(t, database,
		`UPDATE automation_history SET run_status = ?, run_failure_code = NULL, run_completed_at = ? WHERE id = ?`,
		string(automations.RunSucceeded), migrationTimestamp, string(runID))
	if _, err := repository.GetHistoryEntry(
		t.Context(),
		automationID,
		string(runID),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Errorf("contradictory history accepted: %v", err)
	}
}

func TestSuccessfulRunRetainsOnlySelectedCommandExecutions(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	at := time.Now().UTC()
	definition := branchingStorageDefinition(t)
	record, run := admitStorageRun(t, repository, definition, at)
	assertMissingAndUnknownCommandEvidenceRejected(t, repository, run.ID, at)
	completeSelectedCommand(t, repository, run.ID, "before", at)
	for _, id := range []automations.StepID{"route", "fallback"} {
		if err := recordStorageBranch(
			t.Context(),
			repository,
			run.ID,
			storageDecision(id, map[automations.StepID]int{"route": 0, "fallback": 1}[id], at),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err == nil {
		t.Fatal("missing selected-path evidence was accepted")
	}
	for _, id := range []automations.StepID{"before", "default-else", "after"} {
		if id != "before" {
			completeSelectedCommand(t, repository, run.ID, id, at)
		}
	}
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	got := retainedStorageRun(t, repository, record, run)
	assertSelectedRunExecutions(t, got)
	if err := recordStorageStep(
		t.Context(),
		repository,
		run.ID,
		"a-then",
		automations.CommandStopped{At: at, FailureCode: automations.FailureCoreStopping},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("terminal parent accepted late Command interruption: %v", err)
	}
	if after := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(after, got) {
		t.Fatal("late completion changed terminal Run evidence")
	}
}

func assertMissingAndUnknownCommandEvidenceRejected(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	runID automations.RunID,
	at time.Time,
) {
	t.Helper()
	if err := repository.CompleteRun(t.Context(), automations.RunCompletion{
		RunID: runID, Outcome: automations.SucceededRun{},
	}); err == nil {
		t.Fatal("selected branch with missing command evidence succeeded")
	}
	if err := repository.RecordStep(t.Context(), automations.StepExecutionRecord{
		RunID: runID, StepID: "unknown", Evidence: automations.DelayStarted{StartedAt: at},
	}); err == nil {
		t.Fatal("unknown step was recorded")
	}
}

func completeSelectedCommand(
	t *testing.T,
	repository *automationssqlite.AutomationRepository,
	runID automations.RunID,
	id automations.StepID,
	at time.Time,
) {
	t.Helper()
	reservation := automations.CommandReservation{
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	if err := recordStorageCommand(t.Context(), repository, runID, id, reservation, at); err != nil {
		t.Fatal(err)
	}
	if err := completeStorageStep(t.Context(), repository, runID, id, automations.CommandFinished{
		State: automations.CommandSucceeded{CompletedAt: at, Reservation: reservation,
			Outcome: automations.DispatchedCommand{VerifiedCommandID: reservation.CommandID}},
	}); err != nil {
		t.Fatal(err)
	}
}

func assertSelectedRunExecutions(t *testing.T, got automations.Run) {
	t.Helper()
	if automations.RunStateStatus(got.State) != automations.RunSucceeded {
		t.Fatalf("selected path Run = %#v", got)
	}
	reached := make(map[automations.StepID]bool, len(got.Executions))
	for sequence, execution := range got.Executions {
		if execution.Sequence != sequence {
			t.Fatalf("non-global execution sequence: %#v", got.Executions)
		}
		if details, command := execution.Details.(automations.CommandExecutionDetails); command {
			succeeded, ok := details.State.(automations.CommandSucceeded)
			dispatched, isDispatched := succeeded.Outcome.(automations.DispatchedCommand)
			if !ok || !isDispatched || dispatched.VerifiedCommandID != succeeded.Reservation.CommandID {
				t.Fatalf("selected Command lost dispatched evidence: %#v", execution)
			}
			reached[execution.StepID] = true
		}
	}
	for _, id := range []automations.StepID{"before", "default-else", "after"} {
		if !reached[id] {
			t.Fatalf("selected Command %s missing from sparse trace: %#v", id, got.Executions)
		}
	}
	for _, id := range []automations.StepID{"a-then", "a-else", "b", "default-then"} {
		if reached[id] {
			t.Fatalf("unselected Command %s synthesized in trace", id)
		}
	}
}
