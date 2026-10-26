//go:build integration

package sqlite_test

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
)

const functionStorageCatalog adapters.CatalogID = "afc_01980000-0000-7000-8000-000000000001"

func functionStorageDefinition(t *testing.T) automations.Definition {
	t.Helper()
	definition := notificationStorageDefinition(t)
	for index, id := range []automations.StepID{"first", "second"} {
		definition.Steps[index] = automations.Step{ID: id, Body: automations.FunctionStep{
			AdapterID:           "weather",
			FunctionKey:         "refresh",
			ContractFingerprint: adapters.ContractFingerprint("sha256-v1:" + strings.Repeat("a", 64)),
			Arguments:           json.RawMessage(`{"precise":9007199254740993}`),
		}}
	}
	definition.Steps = append(
		definition.Steps,
		automations.Step{ID: "wait", Body: automations.DelayStep{DurationMS: 1000}},
		automations.Step{ID: "notify", Body: automations.NotificationStep{
			DestinationID: "ndst_01980000-0000-7000-8000-000000000001", Message: "done",
		}},
	)
	return definition
}

func functionStorageStart(t *testing.T) automations.FunctionStarted {
	t.Helper()
	id, err := adapters.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.FunctionStarted{InvocationID: id, StartedAt: time.Now().UTC()}
}

func TestFunctionStorageRejectsInvalidStarts(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"invalid Run", "missing parent", "invalid Step", "Command", "missing Step", "invalid invocation", "zero time", "non UTC", "duplicate start", "second running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			start := functionStorageStart(t)
			runID, stepID := run.ID, automations.StepID("first")
			switch fault {
			case "invalid Run":
				runID = "bad"
			case "missing parent":
				runID = automations.RunID(newRunIDString(t))
			case "invalid Step":
				stepID = ""
			case "Command":
				stepID = "light_on"
			case "missing Step":
				stepID = "absent"
			case "invalid invocation":
				start.InvocationID = "bad"
			case "zero time":
				start.StartedAt = time.Time{}
			case "non UTC":
				start.StartedAt = start.StartedAt.In(time.FixedZone("offset", 3600))
			default:
				if err := recordStorageStep(t.Context(), repository, run.ID, "first", start); err != nil {
					t.Fatal(err)
				}
				if fault == "second running" {
					stepID = "second"
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			if err := recordStorageStep(t.Context(), repository, runID, stepID, start); err == nil {
				t.Fatal("invalid reservation accepted")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected start mutated evidence: %#v", got)
			}
		})
	}
}

func TestStepExecutionActiveGuardAppliesAcrossKinds(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := functionStorageDefinition(t)
	definition.Steps = []automations.Step{
		definition.Steps[0],
		{ID: "wait", Body: automations.DelayStep{DurationMS: 1000}},
	}
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	start := functionStorageStart(t)
	if err := recordStorageStep(t.Context(), repository, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	before := retainedStorageRun(t, repository, record, run)
	if err := recordStorageStep(
		t.Context(),
		repository,
		run.ID,
		"wait",
		automations.DelayStarted{StartedAt: start.StartedAt},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("cross-kind concurrent reach = %v", err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatal("cross-kind active guard changed execution")
	}
	var count int
	if err := database.QueryRow(`SELECT count(*) FROM automation_step_executions WHERE run_id = ?`, string(run.ID)).
		Scan(&count); err != nil ||
		count != 1 {
		t.Fatalf("retained executions = %d, %v; want only the active Function", count, err)
	}
}

func TestStepStartReadsOnlyLocalControlEvidence(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := functionStorageDefinition(t)
	definition.Steps = []automations.Step{
		definition.Steps[0],
		{ID: "wait", Body: automations.DelayStep{DurationMS: 1000}},
	}
	_, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	start := functionStorageStart(t)
	if err := recordStorageStep(t.Context(), repository, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	succeeded := automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog}
	if err := completeStorageStep(
		t.Context(),
		repository,
		run.ID,
		"first",
		automations.FunctionFinished{InvocationID: start.InvocationID, State: succeeded},
	); err != nil {
		t.Fatal(err)
	}
	// A local transition needs the prior row's indexed phase/order, not its
	// historical kind payload. Detail reads remain responsible for decoding it.
	mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
	mustExec(
		t,
		database,
		`UPDATE automation_step_executions SET evidence_json = '{}' WHERE run_id = ? AND step_id = 'first'`,
		string(run.ID),
	)
	mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
	if err := recordStorageStep(
		t.Context(),
		repository,
		run.ID,
		"wait",
		automations.DelayStarted{StartedAt: start.StartedAt},
	); err != nil {
		t.Fatalf("next local transition decoded unrelated historical payload: %v", err)
	}
	var sequence int64
	if err := database.QueryRow(`SELECT sequence FROM automation_step_executions WHERE run_id = ? AND step_id = 'wait'`, string(run.ID)).
		Scan(&sequence); err != nil ||
		sequence != 1 {
		t.Fatalf("next execution sequence = %d, %v; want repository-assigned 1", sequence, err)
	}
}

//nolint:gocognit // Independently constructed completion matrix shares one unchanged-evidence oracle.
func TestFunctionStorageRejectsInvalidCompletions(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"wrong link", "wrong Step", "running", "nil", "zero time", "non UTC", "bad catalog", "invented rejection", "success as failure", "bad reason", "bad interruption", "parent success", "parent failure"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			start := functionStorageStart(t)
			if err := recordStorageStep(t.Context(), repository, run.ID, "first", start); err != nil {
				t.Fatal(err)
			}
			before := retainedStorageRun(t, repository, record, run)
			completion := automations.FunctionFinished{
				InvocationID: start.InvocationID,
				State: automations.FunctionSucceeded{
					CompletedAt:     start.StartedAt,
					CatalogRevision: functionStorageCatalog,
				},
			}
			switch fault {
			case "wrong link":
				completion.InvocationID = "afi_01980000-0000-7000-8000-000000000001"
			case "wrong Step":
				// CompleteStep addresses the target step explicitly below.
			case "running":
				completion.State = automations.FunctionFailed{CompletedAt: start.StartedAt}
			case "nil":
				completion.State = nil
			case "zero time":
				completion.State = automations.FunctionSucceeded{
					CompletedAt:     time.Time{},
					CatalogRevision: functionStorageCatalog,
				}
			case "non UTC":
				completion.State = automations.FunctionSucceeded{
					CompletedAt:     start.StartedAt.In(time.FixedZone("offset", 3600)),
					CatalogRevision: functionStorageCatalog,
				}
			case "bad catalog":
				completion.State = automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: "bad"}
			case "invented rejection":
				completion.State = automations.FunctionFailed{
					CompletedAt: start.StartedAt,
					Evidence:    automations.FunctionAdmissionRejected{ReasonCode: "invented"},
				}
			case "success as failure":
				completion.State = automations.FunctionFailed{
					CompletedAt: start.StartedAt,
					Evidence: automations.FunctionInvocationFailed{
						CatalogRevision: functionStorageCatalog,
						Outcome:         adapters.Succeeded{Result: json.RawMessage(`{}`)},
					},
				}
			case "bad reason":
				completion.State = automations.FunctionFailed{
					CompletedAt: start.StartedAt,
					Evidence: automations.FunctionInvocationFailed{
						CatalogRevision: functionStorageCatalog,
						Outcome:         adapters.Unknown{ReasonCode: "unsafe reason"},
					},
				}
			case "bad interruption":
				completion.State = automations.FunctionInterrupted{CompletedAt: start.StartedAt, ReasonCode: "invented"}
			}
			var err error
			if strings.HasPrefix(fault, "parent") {
				var outcome automations.RunOutcome = automations.SucceededRun{}
				if fault == "parent failure" {
					outcome = automations.FailedRun{FailureCode: "failed"}
				}
				err = repository.CompleteRun(t.Context(), automations.RunCompletion{RunID: run.ID, Outcome: outcome})
			} else {
				stepID := automations.StepID("first")
				if fault == "wrong Step" {
					stepID = "second"
				}
				err = completeStorageStep(t.Context(), repository, run.ID, stepID, completion)
			}
			if err == nil {
				t.Fatal("invalid completion accepted")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected completion mutated evidence: %#v", got)
			}
		})
	}
}

// Parent-update faults must roll back the child outcome, while recovery preserves prior successes and reservations.
//
//nolint:gocognit // Fault and control runs share a timeline and exact outcome assertions across all terminal variants.
func TestFunctionStorageAtomicFailureAndRecovery(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"rejected", "failed", "not_executed", "unknown", "interrupted", "cleanup", "restart"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			first := functionStorageStart(t)
			if err := recordStorageStep(t.Context(), repository, run.ID, "first", first); err != nil {
				t.Fatal(err)
			}
			success := automations.FunctionSucceeded{
				CompletedAt:     first.StartedAt,
				CatalogRevision: functionStorageCatalog,
			}
			if err := completeStorageStep(
				t.Context(),
				repository,
				run.ID,
				"first",
				automations.FunctionFinished{InvocationID: first.InvocationID, State: success},
			); err != nil {
				t.Fatal(err)
			}
			second := functionStorageStart(t)
			if err := recordStorageStep(t.Context(), repository, run.ID, "second", second); err != nil {
				t.Fatal(err)
			}
			before := retainedStorageRun(t, repository, record, run)
			at := second.StartedAt.Add(-time.Hour)
			perform := func() error {
				if operation == "cleanup" {
					return repository.InterruptRun(t.Context(), run.ID, at)
				}
				if operation == "restart" {
					return repository.InterruptActiveRuns(
						t.Context(),
						at,
						automations.FailureCoreRestarted,
					)
				}
				return completeStorageStep(
					t.Context(),
					repository,
					run.ID,
					"second",
					automations.FunctionFinished{
						InvocationID: second.InvocationID,
						State:        functionStorageOutcome(operation, at),
					},
				)
			}
			mustExec(
				t,
				database,
				`CREATE TRIGGER fail_function_parent BEFORE UPDATE ON automation_history BEGIN SELECT RAISE(ABORT, 'fault'); END`,
			)
			if err := perform(); err == nil {
				t.Fatal("parent fault not reached")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("partial commit: %#v", got)
			}
			mustExec(t, database, `DROP TRIGGER fail_function_parent`)
			if err := perform(); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			wantStatus, wantReason := automations.RunFailed, "provider_error"
			if operation == "rejected" {
				wantReason = "adapter_unavailable"
			}
			if operation == "interrupted" || operation == "cleanup" || operation == "restart" {
				wantStatus, wantReason = automations.RunInterrupted, automations.FailureCoreStopping
			}
			if operation == "cleanup" {
				wantReason = automations.FailureExecutorFault
			}
			if operation == "restart" {
				wantReason = automations.FailureCoreRestarted
			}
			firstExecution := requireStorageExecution(t, got, "first")
			secondExecution := requireStorageExecution(t, got, "second")
			secondFunction := secondExecution.Details.(automations.FunctionExecutionDetails)
			beforeFirst := requireStorageExecution(t, before, "first")
			if automations.RunStateStatus(got.State) != wantStatus ||
				!reflect.DeepEqual(firstExecution, beforeFirst) ||
				secondFunction.InvocationID != second.InvocationID {
				t.Fatalf("terminal evidence not preserved: %#v", got)
			}
			if !automations.FunctionTerminalTime(secondFunction.State).Equal(at) {
				t.Fatalf("backward completion timestamp changed: %#v", secondFunction)
			}
			expected := functionStorageOutcome(operation, *automations.FunctionTerminalTime(secondFunction.State))
			if !reflect.DeepEqual(secondFunction.State, expected) {
				t.Fatalf("outcome=%#v want=%#v", secondFunction.State, expected)
			}
			if _, code := automations.RunStateCompletion(got.State); code == nil || *code != wantReason {
				t.Fatalf("parent reason=%v want=%s", code, wantReason)
			}
			if err := repository.InterruptRun(t.Context(), run.ID, time.Now().UTC()); err != nil {
				t.Fatal(err)
			}
			if again := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(again, got) {
				t.Fatal("cleanup changed terminal evidence")
			}
		})
	}
}

func functionStorageOutcome(operation string, at time.Time) automations.FunctionTerminalState {
	switch operation {
	case "cleanup":
		return automations.FunctionInterrupted{CompletedAt: at, ReasonCode: automations.FailureExecutorFault}
	case "restart":
		return automations.FunctionInterrupted{CompletedAt: at, ReasonCode: automations.FailureCoreRestarted}
	case "interrupted":
		return automations.FunctionInterrupted{CompletedAt: at, ReasonCode: automations.FailureCoreStopping}
	case "rejected":
		return automations.FunctionFailed{
			CompletedAt: at,
			Evidence:    automations.FunctionAdmissionRejected{ReasonCode: "adapter_unavailable"},
		}
	default:
		var outcome adapters.Evidence = adapters.Failed{ReasonCode: "provider_error"}
		if operation == "not_executed" {
			outcome = adapters.NotExecuted{ReasonCode: "provider_error"}
		}
		if operation == "unknown" {
			outcome = adapters.Unknown{ReasonCode: "provider_error"}
		}
		return automations.FunctionFailed{
			CompletedAt: at,
			Evidence: automations.FunctionInvocationFailed{
				CatalogRevision: functionStorageCatalog,
				Outcome:         outcome,
			},
		}
	}
}
