//go:build integration

package sqlite_test

import (
	"context"
	"encoding/json"
	"errors"
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
			Failure: automations.ValueFailure{Path: "/steps/0/parameters", FailureCode: "expression_limit"},
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

//nolint:gocognit // The transition/corruption matrix shares a pristine parent and rollback oracle.
func TestValueTransitionsRejectCountersContradictingInitialization(t *testing.T) {
	t.Parallel()
	for _, transition := range []string{"record", "complete"} {
		for _, corruption := range []string{"zero records", "short bytes"} {
			t.Run(transition+"/"+corruption, func(t *testing.T) {
				t.Parallel()
				db := openAutomationDatabase(t)
				repo := newAutomationRepository(t, db)
				at := time.Now().UTC()
				_, run := admitStorageRun(t, repo, functionStorageDefinition(t), at)
				start := functionStorageStart(t)
				if transition == "complete" {
					if err := recordStorageStep(t.Context(), repo, run.ID, "first", start); err != nil {
						t.Fatal(err)
					}
				}
				if corruption == "zero records" {
					mustExec(t, db, `UPDATE automation_history SET value_evidence_records=0 WHERE id=?`, string(run.ID))
				} else {
					mustExec(t, db, `UPDATE automation_history SET value_evidence_bytes=1 WHERE id=?`, string(run.ID))
				}
				var err error
				switch transition {
				case "record":
					err = recordStorageStep(t.Context(), repo, run.ID, "first", start)
				case "complete":
					err = completeStorageStep(t.Context(), repo, run.ID, "first", automations.FunctionFinished{
						InvocationID: start.InvocationID,
						State: automations.FunctionFailed{CompletedAt: at,
							Evidence: automations.FunctionAdmissionRejected{ReasonCode: "adapter_unavailable"}},
					})
				}
				if !errors.Is(err, automations.ErrInvalidAutomation) {
					t.Fatalf("corrupt %s accepted by %s: %v", corruption, transition, err)
				}
				var phase, status string
				if err = db.QueryRow(`SELECT run_status FROM automation_history WHERE id=?`, string(run.ID)).
					Scan(&status); err != nil {
					t.Fatal(err)
				}
				if status != "running" {
					t.Fatalf("corrupt parent advanced to %s", status)
				}
				if transition == "complete" {
					if err = db.QueryRow(`SELECT phase FROM automation_step_executions WHERE run_id=?`, string(run.ID)).
						Scan(&phase); err != nil {
						t.Fatal(err)
					}
					if phase != "running" {
						t.Fatalf("corrupt completion advanced to %s", phase)
					}
				} else if count := rowCount(t, db, "automation_step_executions"); count != 0 {
					t.Fatalf("corrupt parent reserved %d executions", count)
				}
			})
		}
	}
}

func TestValueCapacityUsesCanonicalEnvelopeAndRollsBack(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	_, run := admitStorageRun(t, repo, validDomainDefinition(t), at)
	reservation := automations.CommandReservation{
		CommandID:     devices.CommandID(newCommandIDString(t)),
		CorrelationID: devices.CorrelationID(newCorrelationIDString(t)),
	}
	record := automations.StepExecutionRecord{
		RunID:    run.ID,
		StepID:   "light_on",
		Inputs:   json.RawMessage(`{"parameters":{"value":true}}`),
		Evidence: automations.CommandStarted{StartedAt: at, Reservation: reservation},
	}
	details, _, err := automations.PrepareStepExecutionDetails(record)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := automations.EncodePublicStepExecution(
		automations.StepExecution{StepID: record.StepID, Sequence: 0, Details: details},
	)
	if err != nil {
		t.Fatal(err)
	}
	const ordinaryLimit = 2*1024*1024 - 4096
	const reservationLimit = ordinaryLimit - 512 // Pending effect lifecycle allowance, not charged evidence.
	// Populate native counters to isolate the exact boundary without scanning or
	// manufacturing dozens of unrelated historical execution payloads.
	mustExec(
		t,
		db,
		`UPDATE automation_history SET value_evidence_bytes = ? WHERE id = ?`,
		reservationLimit-len(canonical)+1,
		string(run.ID),
	)
	err = repo.RecordStep(t.Context(), record)
	var limit *exprvalue.Failure
	if !errors.As(err, &limit) || limit.Code != "run_data_limit" {
		t.Fatalf("beyond capacity = %v", err)
	}
	var rows, bytes int
	if err = db.QueryRow(`SELECT (SELECT count(*) FROM automation_step_executions WHERE run_id = ?), value_evidence_bytes FROM automation_history WHERE id = ?`, string(run.ID), string(run.ID)).
		Scan(&rows, &bytes); err != nil {
		t.Fatal(err)
	}
	if rows != 0 || bytes != reservationLimit-len(canonical)+1 {
		t.Fatalf("partial capacity write rows=%d bytes=%d", rows, bytes)
	}
	mustExec(
		t,
		db,
		`UPDATE automation_history SET value_evidence_bytes = ? WHERE id = ?`,
		reservationLimit-len(canonical),
		string(run.ID),
	)
	if err = repo.RecordStep(t.Context(), record); err != nil {
		t.Fatalf("exact capacity rejected: %v", err)
	}
	if err = db.QueryRow(`SELECT value_evidence_bytes FROM automation_history WHERE id = ?`, string(run.ID)).
		Scan(&bytes); err != nil ||
		bytes != reservationLimit {
		t.Fatalf("exact counter = %d, %v", bytes, err)
	}
	completion := automations.CommandFinished{State: automations.CommandSucceeded{
		CompletedAt: at, Reservation: reservation,
		Outcome: automations.SatisfiedCommand{VerifiedCommandID: reservation.CommandID},
	}}
	if err = completeStorageStep(t.Context(), repo, run.ID, record.StepID, completion); err != nil {
		t.Fatalf("reserved terminal capacity rejected: %v", err)
	}
	if err = db.QueryRow(`SELECT value_evidence_bytes FROM automation_history WHERE id = ?`, string(run.ID)).
		Scan(&bytes); err != nil ||
		bytes <= reservationLimit ||
		bytes > ordinaryLimit {
		t.Fatalf("terminal counter = %d, %v", bytes, err)
	}
}

func TestFunctionCapacityFailureRetriesOnlyCompletion(t *testing.T) {
	t.Parallel()
	db := openAutomationDatabase(t)
	repo := newAutomationRepository(t, db)
	at := time.Now().UTC()
	_, run := admitStorageRun(t, repo, functionStorageDefinition(t), at)
	start := functionStorageStart(t)
	if err := recordStorageStep(t.Context(), repo, run.ID, "first", start); err != nil {
		t.Fatal(err)
	}
	mustExec(
		t,
		db,
		`UPDATE automation_history SET value_evidence_bytes = ?, value_evidence_records = 255 WHERE id = ?`,
		2*1024*1024-4096,
		string(run.ID),
	)
	finished := automations.FunctionFinished{
		InvocationID: start.InvocationID,
		State:        automations.FunctionSucceeded{CompletedAt: at, CatalogRevision: functionStorageCatalog},
		Output:       json.RawMessage(`{"value":42}`),
	}
	err := repo.CompleteStep(
		t.Context(),
		automations.StepExecutionCompletion{RunID: run.ID, StepID: "first", Evidence: finished},
	)
	var limit *exprvalue.Failure
	if !errors.As(err, &limit) || limit.Code != "run_data_limit" {
		t.Fatalf("output capacity = %v", err)
	}
	var phase, status, raw string
	if err = db.QueryRow(`SELECT e.phase, h.run_status, e.evidence_json FROM automation_step_executions e JOIN automation_history h ON h.id=e.run_id WHERE e.run_id=?`, string(run.ID)).
		Scan(&phase, &status, &raw); err != nil {
		t.Fatal(err)
	}
	if phase != "running" || status != "running" || strings.Contains(raw, `"output"`) {
		t.Fatalf("partial completion: %s %s %s", phase, status, raw)
	}
	finished.Output = nil
	finished.ValueFailure = &automations.ValueFailure{Path: "/steps/0", FailureCode: "run_data_limit"}
	if err = repo.CompleteStep(
		t.Context(),
		automations.StepExecutionCompletion{RunID: run.ID, StepID: "first", Evidence: finished},
	); err != nil {
		t.Fatal(err)
	}
	var count, bytes int
	if err = db.QueryRow(`SELECT e.phase, h.run_status, e.evidence_json, h.value_evidence_records, h.value_evidence_bytes FROM automation_step_executions e JOIN automation_history h ON h.id=e.run_id WHERE e.run_id=?`, string(run.ID)).
		Scan(&phase, &status, &raw, &count, &bytes); err != nil {
		t.Fatal(err)
	}
	if phase != "failed" || status != "failed" || count != 256 || bytes > 2*1024*1024 ||
		!strings.Contains(raw, `"invocation_outcome":"succeeded"`) ||
		!strings.Contains(raw, `"run_data_limit"`) {
		t.Fatalf("terminal reservation failure: %s %s %d %d %s", phase, status, count, bytes, raw)
	}
}

//nolint:gocognit // All effect kinds and completion/cleanup paths share the same independent public-envelope accounting oracle.
func TestEffectCompletionAccountsWholeRetainedEnvelope(t *testing.T) {
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
				initial, err := automations.EncodeRunValueInitialization(*retained.ValueInitialization)
				if err != nil {
					t.Fatal(err)
				}
				public, err := automations.EncodePublicStepExecution(retained.Executions[0])
				if err != nil {
					t.Fatal(err)
				}
				var bytes, records int
				if err = db.QueryRow(`SELECT value_evidence_bytes,value_evidence_records FROM automation_history WHERE id=?`, string(run.ID)).
					Scan(&bytes, &records); err != nil {
					t.Fatal(err)
				}
				if bytes != len(initial)+len(public) || records != 2 {
					t.Fatalf("accounting = %d/%d, want %d/2", bytes, records, len(initial)+len(public))
				}
			})
		}
	}
}

func TestValueDetailRejectsCounterCorruptionAndWrongInputs(t *testing.T) {
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
		RunID:    run.ID,
		StepID:   "light_on",
		Inputs:   json.RawMessage(`{"parameters":{"value":false}}`),
		Evidence: automations.CommandStarted{StartedAt: at, Reservation: reservation},
	}
	if err := repo.RecordStep(t.Context(), reached); err == nil {
		t.Fatal("wrong literal inputs accepted")
	}
	reached.Inputs = json.RawMessage(`{"parameters":{"value":true}}`)
	if err := repo.RecordStep(t.Context(), reached); err != nil {
		t.Fatal(err)
	}
	retainedStorageRun(t, repo, record, run)
	mustExec(
		t,
		db,
		`UPDATE automation_history SET value_evidence_bytes=value_evidence_bytes+1 WHERE id=?`,
		string(run.ID),
	)
	if _, err := repo.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
		t.Fatal("corrupt value accounting accepted")
	}
	mustExec(
		t,
		db,
		`UPDATE automation_history SET data_evidence='legacy_unavailable', value_initialization_json=NULL, value_evidence_bytes=0, value_evidence_records=0 WHERE id=?`,
		string(run.ID),
	)
	if _, err := repo.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
		t.Fatal("legacy Run accepted invented value inputs")
	}
}

func TestDataStepsValidateSelectedPathAndRetainCanonicalAccounting(t *testing.T) {
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
	initial, err := automations.EncodeRunValueInitialization(*retained.ValueInitialization)
	if err != nil {
		t.Fatal(err)
	}
	want := len(initial)
	for _, execution := range retained.Executions {
		raw, encodeErr := automations.EncodePublicStepExecution(execution)
		if encodeErr != nil {
			t.Fatal(encodeErr)
		}
		want += len(raw)
	}
	var bytes, records int
	if err = db.QueryRow(`SELECT value_evidence_bytes,value_evidence_records FROM automation_history WHERE id=?`, string(run.ID)).
		Scan(&bytes, &records); err != nil {
		t.Fatal(err)
	}
	if bytes != want || records != 3 {
		t.Fatalf("canonical accounting got %d/%d want %d/3", bytes, records, want)
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
			Failure: automations.ValueFailure{Path: "/steps/1/parameters", FailureCode: "run_data_limit"},
		},
	}
	if err := repo.RecordStep(t.Context(), reached); err == nil {
		t.Fatal("failure from wrong path accepted")
	}
	reached.Evidence = automations.ValueResolutionFailed{
		Kind:    automations.StepKindCommand,
		At:      at,
		Failure: automations.ValueFailure{Path: "/steps/0/parameters", FailureCode: "run_data_limit"},
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

func TestValueFailureRejectsForeignStateAndInvalidExpressionSpan(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"span", "state"} {
		t.Run(scenario, func(t *testing.T) {
			t.Parallel()
			db := openAutomationDatabase(t)
			repo := newAutomationRepository(t, db)
			at := time.Now().UTC()
			definition := validDomainDefinition(t)
			var failure automations.ValueFailure
			var kind automations.StepKind
			if scenario == "span" {
				command := definition.Steps[0].Body.(automations.CommandStep)
				command.Parameters = automations.ValueBinding{
					Body: automations.ExpressionValue{Source: "{value: true}"},
				}
				definition.Steps[0].Body = command
				start, end := 0, 100
				failure = automations.ValueFailure{
					Path:        "/steps/0/parameters",
					FailureCode: "expression_limit",
					SourceStart: &start,
					SourceEnd:   &end,
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
					Path:        "/steps/0",
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
		`UPDATE automation_step_executions SET phase='failed', evidence_json=json_set(json_remove(evidence_json, '$.output'), '$.value_failure', json('{"path":"/steps/0","failure_code":"expression_missing"}')) WHERE run_id=?`,
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
		!strings.Contains(string(details.Inputs), strings.Repeat("i", 60000)) {
		t.Fatal("large fragments changed")
	}
	header, err := automations.EncodeRunValueInitialization(*retained.ValueInitialization)
	if err != nil {
		t.Fatal(err)
	}
	public, err := automations.EncodePublicStepExecution(retained.Executions[0])
	if err != nil {
		t.Fatal(err)
	}
	var bytes, records int
	if err = db.QueryRow(`SELECT value_evidence_bytes,value_evidence_records FROM automation_history WHERE id=?`, string(run.ID)).
		Scan(&bytes, &records); err != nil {
		t.Fatal(err)
	}
	if bytes != len(header)+len(public) || records != 3 {
		t.Fatalf("combined canonical Function accounting = %d/%d, want %d/3", bytes, records, len(header)+len(public))
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
			Failure: automations.ValueFailure{Path: "/variables", FailureCode: "run_data_limit"},
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

func TestValueInitializationCommitFailureIsNotCapacity(t *testing.T) {
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

func TestValueStepCommitFailureRollsBackCounterAndReservation(t *testing.T) {
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
		RunID:    run.ID,
		StepID:   "light_on",
		Inputs:   json.RawMessage(`{"parameters":{"value":true}}`),
		Evidence: automations.CommandStarted{StartedAt: at, Reservation: reservation},
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
