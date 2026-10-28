//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	adapterssqlite "github.com/mholtzscher/hearth/internal/modules/adapters/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestRunCompletionNormalizesRepositoryClockToUTC(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	at := time.Date(2026, time.October, 27, 12, 0, 0, 123456789, time.FixedZone("household", -5*60*60))
	repo := automationssqlite.NewAutomationRepository(db, automations.Dependencies{Now: func() time.Time { return at }})
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "wait", Body: automations.DelayStep{DurationMS: 1}}}
	record, run := admitStorageRun(t, repo, definition, at.UTC())
	if err := recordStorageStep(
		t.Context(),
		repo,
		run.ID,
		"wait",
		automations.DelayStarted{StartedAt: at.UTC()},
	); err != nil {
		t.Fatal(err)
	}
	if err := completeStorageStep(t.Context(), repo, run.ID, "wait", automations.DelayFinished{
		State: automations.DelaySucceeded{CompletedAt: at.UTC()},
	}); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	completed, ok := retained.State.(automations.CompletedRun)
	if !ok || automations.RunOutcomeStatus(completed.Outcome) != automations.RunSucceeded ||
		!completed.CompletedAt.Equal(at) ||
		completed.CompletedAt.Location() != time.UTC {
		t.Fatalf("completion = %#v", retained.State)
	}
}

func TestValueInitializationIsInsertOnceAndRequiredBeforeSteps(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitUninitializedStorageRun(t, repo, validDomainDefinition(t), at)
	ctx := context.Background()
	if err := repo.RecordStep(
		ctx,
		automations.StepExecutionRecord{RunID: run.ID, StepID: "light_on", Evidence: automations.ValueResolutionFailed{
			Kind:    automations.StepKindCommand,
			At:      at,
			Failure: automations.ValueFailure{Field: "parameters", FailureCode: "expression_limit"},
		}},
	); err == nil {
		t.Fatal("Step accepted before initialization")
	}
	init := automations.RunValueInitialization{
		RecordedAt: at,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err == nil {
		t.Fatal("duplicate initialization accepted")
	}
	retained := retainedStorageRun(t, repo, record, run)
	if retained.DataEvidence != automations.RunDataRecorded || retained.ValueInitialization == nil {
		t.Fatal("initialization not retained")
	}
}

//nolint:gocognit // All effect kinds and completion/cleanup paths share the same retention oracle.
func TestEffectCompletionRetainsEvidence(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"command", "notification", "function"} {
		for _, finish := range []string{"complete", "interrupt Run", "interrupt active"} {
			t.Run(kind+"/"+finish, func(t *testing.T) {
				t.Parallel()
				db := openAutomationDatabase(t)
				repo := newAutomationRepository(t, db)
				at := time.Now().UTC()
				definition := validDomainDefinition(t)
				var start automations.InitialStepEvidence
				var terminal automations.CompletedStepEvidence
				switch kind {
				case "command":
					reservation := automations.CommandReservation{
						CommandID: devices.CommandID(
							newCommandIDString(t),
						),
						CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
					}
					start = automations.CommandStarted{StartedAt: at, Reservation: reservation}
					terminal = automations.CommandFinished{
						State: automations.CommandSucceeded{CompletedAt: at, Reservation: reservation,
							Outcome: automations.SatisfiedCommand{VerifiedCommandID: reservation.CommandID}},
					}
				case "notification":
					definition = notificationStorageDefinition(t)
					notification := notificationStorageStart(t)
					start = notification
					terminal = automations.NotificationFinished{
						AttemptID: notification.AttemptID,
						State:     automations.NotificationSucceeded{CompletedAt: at},
					}
				case "function":
					definition = functionStorageDefinition(t)
					function := functionStorageStart(t)
					start = function
					terminal = automations.FunctionFinished{
						InvocationID: function.InvocationID,
						State: automations.FunctionFailed{
							CompletedAt: at,
							Evidence:    automations.FunctionAdmissionRejected{ReasonCode: "adapter_unavailable"},
						},
					}
				}
				record, run := admitStorageRun(t, repo, definition, at)
				stepID := definition.Steps[0].ID
				if err := recordStorageStep(t.Context(), repo, run.ID, stepID, start); err != nil {
					t.Fatal(err)
				}
				var err error
				switch finish {
				case "complete":
					err = completeStorageStep(t.Context(), repo, run.ID, stepID, terminal)
				case "interrupt Run":
					err = repo.InterruptRun(t.Context(), run.ID, at)
				case "interrupt active":
					err = repo.InterruptActiveRuns(t.Context(), at, automations.FailureCoreRestarted)
				}
				if err != nil {
					t.Fatal(err)
				}
				retained := retainedStorageRun(t, repo, record, run)
				if retained.ValueInitialization == nil || len(retained.Executions) != 1 {
					t.Fatalf("missing retained evidence: %#v", retained)
				}
			})
		}
	}
}

func TestValueDetailRejectsWrongInputs(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	reservation := automations.CommandReservation{
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	reached := automations.StepExecutionRecord{
		RunID:  run.ID,
		StepID: "light_on",
		Evidence: automations.CommandStarted{
			StartedAt:   at,
			Reservation: reservation,
			Parameters:  json.RawMessage(`[]`),
		},
	}
	if err := repo.RecordStep(t.Context(), reached); err == nil {
		t.Fatal("non-object parameters accepted")
	}
	reached.Evidence = automations.CommandStarted{StartedAt: at, Reservation: reservation,
		Parameters: json.RawMessage(`{"value":true,"default":9007199254740993}`)}
	if err := repo.RecordStep(t.Context(), reached); err != nil {
		t.Fatal(err)
	}
	retainedStorageRun(t, repo, record, run)
	// Cosmetic storage formatting must not change retained evidence.
	mustExec(
		t,
		db,
		`UPDATE automation_history SET value_initialization_json='  ' || value_initialization_json || '  ' WHERE id=?`,
		string(run.ID),
	)
	mustExec(
		t,
		db,
		`UPDATE automation_step_executions SET evidence_json='  ' || evidence_json || '  ' WHERE run_id=?`,
		string(run.ID),
	)
	retainedStorageRun(t, repo, record, run)
	mustExec(
		t,
		db,
		`UPDATE automation_history SET data_evidence='legacy_unavailable', value_initialization_json=NULL WHERE id=?`,
		string(run.ID),
	)
	if _, err := repo.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
		t.Fatal("legacy Run accepted invented value inputs")
	}
}

func TestDataStepsValidateSelectedPathAndRetainEvidence(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	definition := validDomainDefinition(t)
	entity := newEntityID(t)
	definition.Steps = []automations.Step{
		{
			ID: "assign",
			Body: automations.SetVariablesStep{
				Assignments: map[automations.VariableName]automations.ValueBinding{"answer": literal(`42`)},
			},
		},
		{
			ID:   "state",
			Body: automations.ReadStateStep{EntityID: entity, Pointer: "/value", OnUnusable: automations.ReadStateFail},
		},
	}
	record, run := admitStorageRun(t, repo, definition, at)
	observation := &automations.ObservationEvidence{
		ObservationID: devices.ObservationID(newObservationIDString(t)),
		ObservedAt:    at,
	}
	state := automations.StateRead{At: at, ReadAt: at, Output: json.RawMessage(`null`), Observation: observation}
	if err := repo.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{RunID: run.ID, StepID: "state", Evidence: state},
	); err == nil {
		t.Fatal("out-of-order State accepted")
	}
	assignment := automations.ValuesAssigned{At: at, Values: json.RawMessage(`{"answer":41}`)}
	if err := repo.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{RunID: run.ID, StepID: "assign", Evidence: assignment},
	); err == nil {
		t.Fatal("wrong assignment literal accepted")
	}
	assignment.Values = json.RawMessage(`{"answer":42}`)
	if err := repo.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{RunID: run.ID, StepID: "assign", Evidence: assignment},
	); err != nil {
		t.Fatal(err)
	}
	empty := automations.ConditionUnknownReason("")
	invalidState := state
	invalidState.UnusableReason = &empty
	if err := repo.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{RunID: run.ID, StepID: "state", Evidence: invalidState},
	); err == nil {
		t.Fatal("empty State reason accepted")
	}
	if err := repo.RecordStep(
		t.Context(),
		automations.StepExecutionRecord{RunID: run.ID, StepID: "state", Evidence: state},
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	if len(retained.Executions) != 2 {
		t.Fatalf("executions = %#v", retained.Executions)
	}
}

func TestHistoricalAssignmentNumericLiteralRemainsReadable(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	definition := validDomainDefinition(t)
	definition.Steps = []automations.Step{{ID: "assign", Body: automations.SetVariablesStep{
		Assignments: map[automations.VariableName]automations.ValueBinding{"n": literal(`9007199254740993.0`)},
	}}}
	record, run := admitStorageRun(t, repo, definition, at)
	write := func(values string) error {
		return repo.RecordStep(t.Context(), automations.StepExecutionRecord{RunID: run.ID, StepID: "assign",
			Evidence: automations.ValuesAssigned{At: at, Values: json.RawMessage(values)}})
	}
	if err := write(`{"n":9007199254740993}`); err == nil {
		t.Fatal("new write accepted historical unrounded result")
	}
	if err := write(`{"n":9007199254740992}`); err != nil {
		t.Fatalf("new rounded result rejected: %v", err)
	}
	if err := repo.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		db,
		`UPDATE automation_step_executions SET evidence_json=json_set(evidence_json, '$.values.n', 9007199254740993) WHERE run_id=?`,
		string(run.ID),
	)
	retained := retainedStorageRun(t, repo, record, run)
	public, err := automations.EncodePublicStepExecution(retained.Executions[0])
	if err != nil || !strings.Contains(string(public), `9007199254740993`) {
		t.Fatalf("historical value changed: %s, %v", public, err)
	}
}

//nolint:gocognit // Both terminal transitions share the same accumulation and retention oracle.
func TestBoundedValuesAccumulateBeyondFormerRunLimit(t *testing.T) {
	t.Parallel()
	for _, finish := range []string{"complete", "interrupt"} {
		t.Run(finish, func(t *testing.T) {
			t.Parallel()
			db := openAutomationDatabase(t)
			repo := newAutomationRepository(t, db)
			at := time.Now().UTC()
			definition := validDomainDefinition(t)
			entity := newEntityID(t)
			definition.Steps = nil
			for i := range 32 {
				definition.Steps = append(
					definition.Steps,
					automations.Step{
						ID: automations.StepID(fmt.Sprintf("read_%d", i)),
						Body: automations.ReadStateStep{
							EntityID:   entity,
							Pointer:    "/value",
							OnUnusable: automations.ReadStateFail,
						},
					},
				)
			}
			record, run := admitStorageRun(t, repo, definition, at)
			output := json.RawMessage(`"` + strings.Repeat("x", 65530) + `"`)
			observation := &automations.ObservationEvidence{
				ObservationID: devices.ObservationID(newObservationIDString(t)),
				ObservedAt:    at,
			}
			for _, step := range definition.Steps {
				if err := recordStorageStep(
					t.Context(),
					repo,
					run.ID,
					step.ID,
					automations.StateRead{At: at, ReadAt: at, Output: output, Observation: observation},
				); err != nil {
					t.Fatalf("record %s: %v", step.ID, err)
				}
			}
			var err error
			if finish == "complete" {
				err = repo.CompleteRun(
					t.Context(),
					automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
				)
			} else {
				err = repo.InterruptRun(t.Context(), run.ID, at)
			}
			if err != nil {
				t.Fatal(err)
			}
			retained := retainedStorageRun(t, repo, record, run)
			if len(retained.Executions) != 32 {
				t.Fatalf("executions = %d", len(retained.Executions))
			}
			var bytes int
			for _, execution := range retained.Executions {
				public, encodeErr := automations.EncodePublicStepExecution(execution)
				if encodeErr != nil {
					t.Fatal(encodeErr)
				}
				bytes += len(public)
			}
			if bytes <= 2*1024*1024 {
				t.Fatalf("retained only %d bytes", bytes)
			}
		})
	}
}

func TestPreReservationValueFailureIsAtomicAndSnapshotBound(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	reached := automations.StepExecutionRecord{
		RunID:  run.ID,
		StepID: "light_on",
		Evidence: automations.ValueResolutionFailed{
			Kind:    automations.StepKindCommand,
			At:      at,
			Failure: automations.ValueFailure{Field: "arguments", FailureCode: "expression_value_invalid"},
		},
	}
	if err := repo.RecordStep(t.Context(), reached); err == nil {
		t.Fatal("failure from wrong path accepted")
	}
	reached.Evidence = automations.ValueResolutionFailed{
		Kind:    automations.StepKindCommand,
		At:      at,
		Failure: automations.ValueFailure{Field: "parameters", FailureCode: "expression_value_invalid"},
	}
	mustExec(
		t,
		db,
		`CREATE TRIGGER reject_value_parent BEFORE UPDATE OF run_status ON automation_history BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	)
	if err := repo.RecordStep(t.Context(), reached); err == nil {
		t.Fatal("parent failure injection accepted")
	}
	before := retainedStorageRun(t, repo, record, run)
	if len(before.Executions) != 0 || automations.RunStateStatus(before.State) != automations.RunRunning {
		t.Fatal("failure partly committed")
	}
	mustExec(t, db, `DROP TRIGGER reject_value_parent`)
	if err := repo.RecordStep(t.Context(), reached); err != nil {
		t.Fatal(err)
	}
	after := retainedStorageRun(t, repo, record, run)
	if len(after.Executions) != 1 || automations.RunStateStatus(after.State) != automations.RunFailed {
		t.Fatal("value failure did not terminalize parent")
	}
	if _, ok := after.Executions[0].Details.(automations.ValueFailureExecutionDetails); !ok {
		t.Fatalf("fabricated reservation: %#v", after.Executions[0].Details)
	}
	mustExec(t, db, `UPDATE automation_history SET run_failure_code='expression_limit' WHERE id=?`, string(run.ID))
	if _, err := repo.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
		t.Fatal("parent failure-code mismatch accepted")
	}
}

func TestValueFailureRejectsForeignStateAndInvalidField(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"field", "state"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db := openAutomationDatabase(t)
			repo := newAutomationRepository(t, db)
			at := time.Now().UTC()
			definition := validDomainDefinition(t)
			var failure automations.ValueFailure
			var kind automations.StepKind
			if scenario == "field" {
				command := definition.Steps[0].Body.(automations.CommandStep)
				command.Parameters = automations.ValueBinding{
					Body: automations.ExpressionValue{Source: "{value: true}"},
				}
				definition.Steps[0].Body = command
				failure = automations.ValueFailure{
					Field:       "arguments",
					FailureCode: "expression_limit",
				}
				kind = automations.StepKindCommand
			} else {
				definition.Steps = []automations.Step{
					{
						ID: "light_on",
						Body: automations.ReadStateStep{
							EntityID:   newEntityID(t),
							Pointer:    "/value",
							OnUnusable: automations.ReadStateFail,
						},
					},
				}
				failure = automations.ValueFailure{
					FailureCode: "read_state_unusable",
					State: &automations.StateReadFailure{
						EntityID:       newEntityID(t),
						Pointer:        "/value",
						ReadAt:         at,
						UnusableReason: automations.ConditionUnknownEntityMissing,
					},
				}
				kind = automations.StepKindReadState
			}
			record, run := admitStorageRun(t, repo, definition, at)
			err := repo.RecordStep(
				t.Context(),
				automations.StepExecutionRecord{
					RunID:    run.ID,
					StepID:   "light_on",
					Evidence: automations.ValueResolutionFailed{Kind: kind, At: at, Failure: failure},
				},
			)
			if err == nil {
				t.Fatalf("%s corruption accepted", scenario)
			}
			retained := retainedStorageRun(t, repo, record, run)
			if len(retained.Executions) != 0 || automations.RunStateStatus(retained.State) != automations.RunRunning {
				t.Fatal("invalid failure wrote evidence")
			}
		})
	}
}

func TestFunctionCopiedOutputSurvivesInvocationPruning(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	functions := adapterssqlite.NewAdapterRepository(db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, functionStorageDefinition(t), at)
	start := functionStorageStart(t)
	if err := recordStorageStep(t.Context(), repo, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	output := json.RawMessage(`{"precise":9007199254740993}`)
	stamp := start.StartedAt.Format("2006-01-02T15:04:05.000000000Z")
	mustExec(
		t,
		db,
		`INSERT INTO adapter_function_invocations (id,adapter_id,runtime_id,catalog_revision,function_key,descriptor_json,arguments_json,started_at,deadline,status,result_json,completed_at) VALUES (?, 'weather', 'run_01980000-0000-7000-8000-000000000001', ?, 'refresh', '{"key":"refresh","description":"Refresh","input_schema":{"type":"object"},"output_schema":{"type":"object"},"timeout_ms":1000}', '{"precise":9007199254740993}', ?, ?, 'succeeded', ?, ?)`,
		string(start.InvocationID),
		string(functionStorageCatalog),
		stamp,
		stamp,
		string(output),
		stamp,
	)
	if err := repo.CompleteStep(
		t.Context(),
		automations.StepExecutionCompletion{
			RunID:  run.ID,
			StepID: "first",
			Evidence: automations.FunctionFinished{
				InvocationID: start.InvocationID,
				State:        automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: functionStorageCatalog},
				Output:       output,
			},
		},
	); err != nil {
		t.Fatal(err)
	}
	if deleted, err := functions.DeleteHistoryBefore(t.Context(), at.Add(time.Hour), 10); err != nil || deleted != 1 {
		t.Fatalf("prune = %d, %v", deleted, err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	function := requireStorageExecution(t, retained, "first").Details.(automations.FunctionExecutionDetails)
	if string(function.Output) != string(output) {
		t.Fatalf("copied output = %s", function.Output)
	}
	mustExec(
		t,
		db,
		`UPDATE automation_step_executions SET phase='failed', evidence_json=json_set(json_remove(evidence_json, '$.output'), '$.value_failure', json('{"failure_code":"expression_missing"}')) WHERE run_id=?`,
		string(run.ID),
	)
	if _, err := repo.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
		t.Fatal("invalid post-success failure accepted")
	}
}

func TestFunctionLargeIndependentInputAndOutputFragments(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	definition := functionStorageDefinition(t)
	function := definition.Steps[0].Body.(automations.FunctionStep)
	argument := `{"data":"` + strings.Repeat("i", 60000) + `"}`
	function.Arguments = literal(argument)
	definition.Steps = []automations.Step{{ID: "first", Body: function}}
	record, run := admitStorageRun(t, repo, definition, at)
	start := functionStorageStart(t)
	if err := recordStorageStep(t.Context(), repo, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	output := json.RawMessage(`{"data":"` + strings.Repeat("o", 60000) + `"}`)
	finished := automations.FunctionFinished{
		InvocationID: start.InvocationID,
		State:        automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: functionStorageCatalog},
		Output:       output,
	}
	if err := repo.CompleteStep(
		t.Context(),
		automations.StepExecutionCompletion{RunID: run.ID, StepID: "first", Evidence: finished},
	); err != nil {
		t.Fatal(err)
	}
	if err := repo.CompleteRun(
		t.Context(),
		automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
	); err != nil {
		t.Fatal(err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	details := retained.Executions[0].Details.(automations.FunctionExecutionDetails)
	if string(details.Output) != string(output) ||
		!strings.Contains(string(details.Arguments), strings.Repeat("i", 60000)) {
		t.Fatal("large fragments changed")
	}
}

func TestValueInitializationFailureAtomicRollback(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitUninitializedStorageRun(t, repo, validDomainDefinition(t), at)
	ctx := context.Background()
	if _, err := db.Exec(
		`CREATE TRIGGER reject_value_failure BEFORE UPDATE OF run_status ON automation_history BEGIN SELECT RAISE(ABORT, 'injected'); END`,
	); err != nil {
		t.Fatal(err)
	}
	init := automations.RunValueInitialization{
		RecordedAt: at,
		Body: automations.InitializationFailed{
			Failure: automations.ValueFailure{Field: "variables", FailureCode: "expression_value_invalid"},
		},
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err == nil {
		t.Fatal("injected failure accepted")
	}
	retained := retainedStorageRun(t, repo, record, run)
	if retained.ValueInitialization != nil || automations.RunStateStatus(retained.State) != automations.RunRunning {
		t.Fatal("partial initialization committed")
	}
	if _, err := db.Exec(`DROP TRIGGER reject_value_failure`); err != nil {
		t.Fatal(err)
	}
	if err := repo.RecordRunValueInitialization(ctx, run.ID, init); err != nil {
		t.Fatal(err)
	}
	retained = retainedStorageRun(t, repo, record, run)
	if retained.ValueInitialization == nil || automations.RunStateStatus(retained.State) != automations.RunFailed {
		t.Fatal("failure was not committed atomically")
	}
}

func TestValueInitializationCommitFailureIsNotValueFailure(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitUninitializedStorageRun(t, repo, validDomainDefinition(t), at)
	mustExec(t, db, `CREATE TABLE value_commit_parent (id INTEGER PRIMARY KEY)`)
	mustExec(
		t,
		db,
		`CREATE TABLE value_commit_child (id INTEGER REFERENCES value_commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
	)
	mustExec(
		t,
		db,
		`CREATE TRIGGER fail_value_commit AFTER UPDATE OF value_initialization_json ON automation_history BEGIN INSERT INTO value_commit_child VALUES (1); END`,
	)
	init := automations.RunValueInitialization{
		RecordedAt: at,
		Body:       automations.ValuesInitialized{Values: json.RawMessage(`{}`)},
	}
	err := repo.RecordRunValueInitialization(t.Context(), run.ID, init)
	var limit *exprvalue.Failure
	if err == nil || errors.As(err, &limit) {
		t.Fatalf("commit error = %v", err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	if retained.ValueInitialization != nil || automations.RunStateStatus(retained.State) != automations.RunRunning {
		t.Fatal("commit failure left partial initialization")
	}
	if count := rowCount(t, db, "value_commit_child"); count != 0 {
		t.Fatalf("deferred child rows = %d", count)
	}
}

func TestValueStepCommitFailureRollsBackReservation(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	record, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	mustExec(t, db, `CREATE TABLE step_commit_parent (id INTEGER PRIMARY KEY)`)
	mustExec(
		t,
		db,
		`CREATE TABLE step_commit_child (id INTEGER REFERENCES step_commit_parent(id) DEFERRABLE INITIALLY DEFERRED)`,
	)
	mustExec(
		t,
		db,
		`CREATE TRIGGER fail_step_commit AFTER INSERT ON automation_step_executions BEGIN INSERT INTO step_commit_child VALUES (1); END`,
	)
	reservation := automations.CommandReservation{
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	reached := automations.StepExecutionRecord{
		RunID:  run.ID,
		StepID: "light_on",
		Evidence: automations.CommandStarted{
			StartedAt:   at,
			Reservation: reservation,
			Parameters:  json.RawMessage(`{"value":true}`),
		},
	}
	err := repo.RecordStep(t.Context(), reached)
	var limit *exprvalue.Failure
	if err == nil || errors.As(err, &limit) {
		t.Fatalf("commit error = %v", err)
	}
	retained := retainedStorageRun(t, repo, record, run)
	if len(retained.Executions) != 0 || automations.RunStateStatus(retained.State) != automations.RunRunning {
		t.Fatal("commit failure left a partial reservation")
	}
	if count := rowCount(t, db, "step_commit_child"); count != 0 {
		t.Fatalf("deferred child rows = %d", count)
	}
	mustExec(t, db, `DROP TRIGGER fail_step_commit`)
	if err = repo.RecordStep(t.Context(), reached); err != nil {
		t.Fatal(err)
	}
	retainedStorageRun(t, repo, record, run)
}
