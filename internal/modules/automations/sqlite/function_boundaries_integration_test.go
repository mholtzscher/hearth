//go:build integration

package sqlite_test

import (
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// Reservations exclude every other effect kind in both directions.
//
//nolint:gocognit // One transition matrix verifies both reservation orders against unchanged retained evidence.
func TestFunctionStorageCrossKindReservations(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"Command", "delay", "notification"} {
		for _, functionFirst := range []bool{true, false} {
			t.Run(
				kind+map[bool]string{true: "/function-first", false: "/function-last"}[functionFirst],
				func(t *testing.T) {
					t.Parallel()
					repository := newAutomationRepository(t, openAutomationDatabase(t))
					base := functionStorageDefinition(t)
					functionStep := base.Steps[0]
					functionStep.ID = "function"
					var otherStep automations.Step
					switch kind {
					case "Command":
						otherStep = validDomainDefinition(t).Steps[0]
					case "delay":
						otherStep = base.Steps[3]
					case "notification":
						otherStep = base.Steps[4]
					}
					definition := base
					definition.Steps = []automations.Step{functionStep, otherStep}
					if !functionFirst {
						definition.Steps = []automations.Step{otherStep, functionStep}
					}
					record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
					start := functionStorageStart(t, run)
					start.StepID = "function"
					other := func() error {
						switch kind {
						case "Command":
							reservation := automations.CommandReservation{
								CommandID:     devices.CommandID(newCommandIDString(t)),
								CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
							}
							return recordStorageCommand(
								t.Context(),
								repository,
								run.ID,
								otherStep.ID,
								reservation,
								time.Now().UTC(),
							)
						case "delay":
							return recordStorageStep(
								t.Context(),
								repository,
								run.ID,
								otherStep.ID,
								automations.DelayStarted{StartedAt: time.Now().UTC()},
							)
						default:
							notification := notificationStorageStart(t, run)
							notification.StepID = otherStep.ID
							return recordStorageStep(
								t.Context(),
								repository,
								run.ID,
								notification.StepID,
								automations.NotificationStarted{
									StartedAt: notification.StartedAt,
									AttemptID: notification.AttemptID,
								},
							)
						}
					}
					first, second := func() error { return recordStorageFunction(t.Context(), repository, start) }, other
					if !functionFirst {
						first, second = second, first
					}
					if err := first(); err != nil {
						t.Fatal(err)
					}
					before := retainedStorageRun(t, repository, record, run)
					if err := second(); !errors.Is(err, automations.ErrInvalidAutomation) {
						t.Fatalf("conflicting reservation=%v", err)
					}
					if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
						t.Fatalf("conflict mutated evidence: %#v", got)
					}
				},
			)
		}
	}
}

func TestFunctionStorageStartFailureAndImmutableSnapshot(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	definition := functionStorageDefinition(t)
	definition.Steps = definition.Steps[:2]
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	start := functionStorageStart(t, run)
	mustExec(
		t,
		database,
		`CREATE TRIGGER fail_function_start BEFORE INSERT ON automation_step_executions BEGIN SELECT RAISE(ABORT, 'fault'); END`,
	)
	if err := recordStorageFunction(t.Context(), repository, start); err == nil {
		t.Fatal("start fault not reached")
	}
	if got := retainedStorageRun(
		t,
		repository,
		record,
		run,
	); len(got.Executions) != 0 ||
		automations.RunStateStatus(got.State) != automations.RunRunning {
		t.Fatalf("partial start: %#v", got)
	}
	mustExec(t, database, `DROP TRIGGER fail_function_start`)
	if err := repository.DeleteAutomation(t.Context(), record.ID, record.Revision); err != nil {
		t.Fatal(err)
	}
	if err := recordStorageFunction(t.Context(), repository, start); err != nil {
		t.Fatal(err)
	}
	if err := completeStorageFunction(
		t.Context(), repository,
		functionStorageCompletion(
			start,
			automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
		),
	); err != nil {
		t.Fatal(err)
	}
	got := retainedStorageRun(t, repository, record, run)
	function := requireStorageExecution(t, got, "first").Body.(automations.FunctionExecutionBody)
	if function.InvocationID != start.InvocationID || automations.RunStateStatus(got.State) != automations.RunRunning {
		t.Fatalf("snapshot-owned reservation=%#v", got)
	}
	if err := completeStorageFunction(
		t.Context(), repository,
		functionStorageCompletion(
			start,
			automations.FunctionSucceeded{CompletedAt: start.StartedAt, CatalogRevision: functionStorageCatalog},
		),
	); err == nil {
		t.Fatal("terminal reservation completed twice")
	}
	duplicate := functionStorageStart(t, run)
	duplicate.StepID, duplicate.Position, duplicate.InvocationID = "second", 1, start.InvocationID
	if err := recordStorageFunction(t.Context(), repository, duplicate); err == nil {
		t.Fatal("invocation identity reused")
	}
	duplicate.InvocationID = functionStorageStart(t, run).InvocationID
	duplicate.StartedAt = start.StartedAt.Add(-time.Second)
	if err := recordStorageFunction(t.Context(), repository, duplicate); err != nil {
		t.Fatalf("append after backward clock correction: %v", err)
	}
	if err := completeStorageFunction(t.Context(), repository, functionStorageCompletion(
		duplicate,
		automations.FunctionSucceeded{
			CompletedAt:     duplicate.StartedAt.Add(-time.Second),
			CatalogRevision: functionStorageCatalog,
		},
	)); err != nil {
		t.Fatal(err)
	}
	if err := recordStorageFunction(t.Context(), repository, functionStorageStart(t, run)); err == nil {
		t.Fatal("duplicate Step admitted work")
	}
}

// Adapter recovery and pruning are independent; Automation evidence survives until its own history is pruned.
func TestFunctionStorageRecoveryRetentionIndependenceAndCascade(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	functions := adapterssqlite.NewAdapterRepository(database)
	record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
	start := functionStorageStart(t, run)
	if err := recordStorageFunction(t.Context(), repository, start); err != nil {
		t.Fatal(err)
	}
	// Synthetic in-flight Adapter invocation; its snapshot belongs solely to Adapters.
	mustExec(
		t,
		database,
		`INSERT INTO adapter_function_invocations
		(id, adapter_id, runtime_id, catalog_revision, function_key, descriptor_json, arguments_json, started_at, deadline, status)
		VALUES (?, 'weather', 'run_01980000-0000-7000-8000-000000000001', ?, 'refresh',
		'{"key":"refresh","description":"Refresh","input_schema":{"type":"object"},"output_schema":{"type":"object"},"timeout_ms":1000}',
		'{"precise":9007199254740993}', ?, ?, 'running')`,
		string(start.InvocationID),
		string(functionStorageCatalog),
		start.StartedAt.Format(
			"2006-01-02T15:04:05.000000000Z",
		),
		start.StartedAt.Add(time.Second).Format("2006-01-02T15:04:05.000000000Z"),
	)
	at := start.StartedAt.Add(-time.Second)
	if err := functions.Recover(t.Context(), at); err != nil {
		t.Fatal(err)
	}
	if err := repository.InterruptActiveRuns(t.Context(), at, automations.FailureCoreRestarted); err != nil {
		t.Fatal(err)
	}
	invocation, err := functions.GetInvocation(t.Context(), start.InvocationID)
	if err != nil {
		t.Fatal(err)
	}
	if invocation.Completion == nil {
		t.Fatalf("Adapter recovery=%#v", invocation)
	}
	if !invocation.StartedAt.Equal(start.StartedAt) || !invocation.Completion.At.Equal(at) {
		t.Fatalf("Adapter timestamps rewritten=%#v", invocation)
	}
	if _, unknown := invocation.Completion.Evidence.(adapters.Unknown); !unknown {
		t.Fatalf("Adapter outcome=%#v", invocation.Completion)
	}
	before := retainedStorageRun(t, repository, record, run)
	function := requireStorageExecution(t, before, start.StepID).Body.(automations.FunctionExecutionBody)
	if state, ok := function.State.(automations.FunctionInterrupted); !ok ||
		state.ReasonCode != automations.FailureCoreRestarted ||
		!state.CompletedAt.Equal(at) ||
		function.InvocationID != start.InvocationID {
		t.Fatalf("Automation recovery=%#v", before)
	}
	if err = repository.InterruptActiveRuns(
		t.Context(),
		at.Add(-time.Second),
		automations.FailureCoreRestarted,
	); err != nil {
		t.Fatal(err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatalf("repeated recovery changed Automation evidence=%#v", got)
	}
	deleted, err := functions.DeleteHistoryBefore(t.Context(), at.Add(time.Second), 500)
	if err != nil || deleted != 1 {
		t.Fatalf("Adapter pruning=%d %v", deleted, err)
	}
	if _, err = functions.GetInvocation(
		t.Context(),
		start.InvocationID,
	); !errors.Is(
		err,
		adapters.ErrInvocationNotFound,
	) {
		t.Fatalf("pruned invocation=%v", err)
	}
	if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
		t.Fatalf("Adapter prune changed Automation evidence=%#v", got)
	}
	deleted, err = repository.DeleteHistoryBefore(t.Context(), at.Add(time.Second), 500)
	if err != nil || deleted != 1 {
		t.Fatalf("Automation pruning=%d %v", deleted, err)
	}
	var children int
	if err = database.QueryRow(`SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND kind = 'function'`, string(run.ID)).
		Scan(&children); err != nil ||
		children != 0 {
		t.Fatalf("cascade children=%d %v", children, err)
	}
}

func TestFunctionStorageCorruptHistoryRejected(t *testing.T) {
	t.Parallel()
	for _, mutation := range []string{`{"invocation_id":"bad"}`, `{"invocation_id":"valid","extra":true}`, `{}`, `null`} {
		t.Run(mutation, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
			if err := recordStorageFunction(t.Context(), repository, functionStorageStart(t, run)); err != nil {
				t.Fatal(err)
			}
			mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
			mustExec(
				t,
				database,
				`UPDATE automation_step_executions SET evidence_json = ? WHERE run_id = ? AND kind = 'function'`,
				mutation,
				string(run.ID),
			)
			mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
			if _, err := repository.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
				t.Fatal("corrupt evidence returned")
			}
		})
	}
}

func TestFunctionStorageFunctionOnlySuccess(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	definition := functionStorageDefinition(t)
	definition.Steps = definition.Steps[:2]
	record, run := admitStorageRun(t, repository, definition, time.Now().UTC())
	for position, id := range []automations.StepID{"first", "second"} {
		start := functionStorageStart(t, run)
		start.StepID, start.Position = id, position
		start.StartedAt = run.StartedAt.Add(-time.Duration(position+1) * time.Second)
		if err := recordStorageFunction(t.Context(), repository, start); err != nil {
			t.Fatal(err)
		}
		state := automations.FunctionSucceeded{
			CompletedAt:     start.StartedAt.Add(-time.Second),
			CatalogRevision: functionStorageCatalog,
		}
		if err := completeStorageFunction(
			t.Context(),
			repository,
			functionStorageCompletion(start, state),
		); err != nil {
			t.Fatal(err)
		}
	}
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	got := retainedStorageRun(t, repository, record, run)
	if automations.RunStateStatus(got.State) != automations.RunSucceeded || len(got.Executions) != 2 {
		t.Fatalf("Function-only Run=%#v", got)
	}
	for position, execution := range got.Executions {
		function := execution.Body.(automations.FunctionExecutionBody)
		state, ok := function.State.(automations.FunctionSucceeded)
		if !ok || state.CatalogRevision != functionStorageCatalog || execution.Sequence != position {
			t.Fatalf("success evidence=%#v", execution)
		}
	}
}

// Completion SQL independently prevents a corrupt running parent from promoting failed Function evidence to success.
func TestFunctionStorageSuccessfulRunGuard(t *testing.T) {
	t.Parallel()
	database := openAutomationDatabase(t)
	repository := newAutomationRepository(t, database)
	_, run := admitStorageRun(t, repository, functionStorageDefinition(t), time.Now().UTC())
	start := functionStorageStart(t, run)
	if err := recordStorageFunction(t.Context(), repository, start); err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		database,
		`UPDATE automation_step_executions SET phase = 'failed', completed_at = started_at,
		evidence_json = '{"invocation_id":"' || json_extract(evidence_json, '$.invocation_id') || '","reason_code":"adapter_unavailable"}'
		WHERE kind = 'function' AND run_id = ?`, string(run.ID),
	)
	if err := repository.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("failed Function promoted to success: %v", err)
	}
	var status string
	if err := database.QueryRow(`SELECT run_status FROM automation_history WHERE id = ?`, string(run.ID)).
		Scan(&status); err != nil ||
		status != "running" {
		t.Fatalf("guard mutated parent=%s %v", status, err)
	}
}
