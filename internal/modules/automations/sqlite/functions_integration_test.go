//go:build integration

package sqlite_test

import (
	"encoding/json"
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
			Arguments:           literalBinding(`{"precise":9007199254740993}`),
		}}
	}
	definition.Steps = append(
		definition.Steps,
		automations.Step{ID: "wait", Body: automations.DelayStep{DurationMS: 1000}},
		automations.Step{ID: "notify", Body: automations.NotificationStep{
			DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
			Title:         literalBinding(`""`),
			Message:       literalBinding(`"done"`),
		}},
	)
	return definition
}

func functionStorageStart(t *testing.T, run automations.Run) automations.FunctionStart {
	t.Helper()
	id, err := adapters.NewInvocationID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.FunctionStart{RunID: run.ID, StepID: "first", InvocationID: id, StartedAt: time.Now().UTC(),
		Inputs: storageInputs(run, "first", `{"arguments":{"precise":9007199254740993}}`)}
}

func functionStorageCompletion(
	start automations.FunctionStart,
	state automations.FunctionExecutionState,
) automations.FunctionCompletion {
	var data *automations.RunDataRecord
	if success, ok := state.(automations.FunctionSucceeded); ok {
		data = &automations.RunDataRecord{Sequence: start.Inputs.Sequence + 1, StepID: &start.StepID,
			RecordedAt: success.CompletedAt, Body: automations.FunctionOutputCaptured{Output: json.RawMessage(`{}`)}}
	}
	return automations.FunctionCompletion{
		RunID:        start.RunID,
		StepID:       start.StepID,
		InvocationID: start.InvocationID,
		State:        state,
		Data:         data,
	}
}

func TestFunctionStorageRejectsInvalidStarts(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"invalid Run", "missing parent", "invalid Step", "Command", "missing Step", "invalid invocation", "negative position", "position gap", "position overflow", "zero time", "non UTC", "duplicate start", "second running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			start := functionStorageStart(t, run)
			switch fault {
			case "invalid Run":
				start.RunID = "bad"
			case "missing parent":
				start.RunID = automations.RunID(newRunIDString(t))
			case "invalid Step":
				start.StepID = ""
			case "Command":
				start.StepID = "light_on"
			case "missing Step":
				start.StepID = "absent"
			case "invalid invocation":
				start.InvocationID = "bad"
			case "negative position":
				start.Position = -1
			case "position gap":
				start.Position = 1
			case "position overflow":
				start.Position = 32
			case "zero time":
				start.StartedAt = time.Time{}
			case "non UTC":
				start.StartedAt = start.StartedAt.In(time.FixedZone("offset", 3600))
			default:
				if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
					t.Fatal(err)
				}
				if fault == "second running" {
					start.StepID, start.Position = "second", 1
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			if err := repository.RecordFunctionStart(t.Context(), start); err == nil {
				t.Fatal("invalid reservation accepted")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected start mutated evidence: %#v", got)
			}
		})
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
			start := functionStorageStart(t, run)
			if err := repository.RecordFunctionStart(t.Context(), start); err != nil {
				t.Fatal(err)
			}
			before := retainedStorageRun(t, repository, record, run)
			completion := functionStorageCompletion(
				start,
				automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
			)
			switch fault {
			case "wrong link":
				completion.InvocationID = "afi_01980000-0000-7000-8000-000000000001"
			case "wrong Step":
				completion.StepID = "second"
			case "running":
				completion.State = automations.FunctionRunning{}
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
				err = repository.CompleteFunction(t.Context(), completion)
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
			first := functionStorageStart(t, run)
			if err := repository.RecordFunctionStart(t.Context(), first); err != nil {
				t.Fatal(err)
			}
			success := automations.FunctionSucceeded{
				CompletedAt:     first.StartedAt,
				CatalogRevision: functionStorageCatalog,
			}
			if err := repository.CompleteFunction(t.Context(), functionStorageCompletion(first, success)); err != nil {
				t.Fatal(err)
			}
			second := functionStorageStart(t, retainedStorageRun(t, repository, record, run))
			second.StepID, second.Position = "second", 1
			second.Inputs.StepID = &second.StepID
			if err := repository.RecordFunctionStart(t.Context(), second); err != nil {
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
				return repository.CompleteFunction(
					t.Context(),
					functionStorageCompletion(second, functionStorageOutcome(operation, at)),
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
			if automations.RunStateStatus(got.State) != wantStatus ||
				!reflect.DeepEqual(got.Functions[0], before.Functions[0]) ||
				got.Functions[1].InvocationID != second.InvocationID {
				t.Fatalf("terminal evidence not preserved: %#v", got)
			}
			if !automations.FunctionTerminalTime(got.Functions[1].State).Equal(at) {
				t.Fatalf("backward completion timestamp changed: %#v", got.Functions[1])
			}
			expected := functionStorageOutcome(operation, *automations.FunctionTerminalTime(got.Functions[1].State))
			if !reflect.DeepEqual(got.Functions[1].State, expected) {
				t.Fatalf("outcome=%#v want=%#v", got.Functions[1].State, expected)
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

func functionStorageOutcome(operation string, at time.Time) automations.FunctionExecutionState {
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
