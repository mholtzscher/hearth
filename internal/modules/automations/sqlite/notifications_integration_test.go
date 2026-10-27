//go:build integration

package sqlite_test

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

func notificationStorageDefinition(t *testing.T) automations.Definition {
	t.Helper()
	definition := validDomainDefinition(t)
	definition.Steps = append([]automations.Step{
		{
			ID: "first",
			Body: automations.NotificationStep{
				DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Title:         literal(`""`),
				Message:       literal(`"one"`),
			},
		},
		{
			ID: "second",
			Body: automations.NotificationStep{
				DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Title:         literal(`""`),
				Message:       literal(`"two"`),
			},
		},
	}, definition.Steps...)
	return definition
}

func notificationStorageStart(t *testing.T) automations.NotificationStarted {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.NotificationStarted{AttemptID: id, StartedAt: time.Now().UTC()}
}

// Freely constructed repository writes cannot attach reservations to the wrong snapshot or adopt other attempt IDs.
//
//nolint:gocognit // Rejected transition matrix shares one pristine evidence oracle.
func TestNotificationStorageRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"invalid Run", "missing parent", "invalid Step", "Command", "missing Step", "invalid attempt", "zero time", "non UTC", "duplicate start", "second running", "wrong completion link", "accepted without outcome", "failed without reason", "accepted outcome with failure reason", "pre-attempt invented outcome", "Run completed while running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			record, run := admitStorageRun(t, repository, notificationStorageDefinition(t), time.Now().UTC())
			start := notificationStorageStart(t)
			stepID := automations.StepID("first")
			mutated := start
			switch fault {
			case "invalid Run", "missing parent":
			case "invalid Step":
				stepID = ""
			case "Command":
				stepID = "light_on"
			case "missing Step":
				stepID = "absent"
			case "invalid attempt":
				mutated.AttemptID = "bad"
			case "zero time":
				mutated.StartedAt = time.Time{}
			case "non UTC":
				mutated.StartedAt = start.StartedAt.In(time.FixedZone("offset", 3600))
			default:
				if err := recordStorageStep(t.Context(), repository, run.ID, stepID, start); err != nil {
					t.Fatal(err)
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			var err error
			switch fault {
			case "wrong completion link",
				"accepted without outcome",
				"failed without reason",
				"accepted outcome with failure reason",
				"pre-attempt invented outcome":
				completion := automations.NotificationFinished{
					AttemptID: start.AttemptID,
					State:     automations.NotificationSucceeded{CompletedAt: start.StartedAt},
				}
				switch fault {
				case "wrong completion link":
					completion.AttemptID = "ntf_01980000-0000-7000-8000-000000000001"
				case "accepted without outcome":
					completion.State = automations.NotificationFailed{CompletedAt: start.StartedAt}
				case "failed without reason":
					completion.State = automations.NotificationFailed{
						CompletedAt: start.StartedAt,
						Evidence:    automations.NotificationAttemptFailed{Outcome: notifications.Unknown},
					}
				case "accepted outcome with failure reason":
					completion.State = automations.NotificationFailed{
						CompletedAt: start.StartedAt,
						Evidence: automations.NotificationAttemptFailed{
							Outcome:    notifications.Accepted,
							ReasonCode: "provider_error",
						},
					}
				case "pre-attempt invented outcome":
					completion.State = automations.NotificationFailed{
						CompletedAt: start.StartedAt,
						Evidence:    automations.NotificationAdmissionRejected{ReasonCode: "invented"},
					}
				}
				err = completeStorageStep(t.Context(), repository, run.ID, stepID, completion)
			case "Run completed while running":
				err = repository.CompleteRun(
					t.Context(),
					automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
				)
			case "second running":
				mutated = notificationStorageStart(t)
				stepID = "second"
				err = recordStorageStep(t.Context(), repository, run.ID, stepID, mutated)
			case "invalid Run", "missing parent":
				invalidRunID := automations.RunID("bad")
				if fault == "missing parent" {
					invalidRunID = automations.RunID(newRunIDString(t))
				}
				err = repository.RecordStep(t.Context(), automations.StepExecutionRecord{
					RunID: invalidRunID, StepID: stepID, Evidence: mutated,
				})
			default:
				err = recordStorageStep(t.Context(), repository, run.ID, stepID, mutated)
			}
			if err == nil {
				t.Fatal("invalid transition accepted")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("rejected transition mutated evidence=%#v", got)
			}
		})
	}
}

// Real SQLite fault injection proves failed completion and interruption are atomic, not partial cleanup.
//
//nolint:gocognit // The trigger fault and successful control run check the same transactional timeline.
func TestNotificationStorageTransactionsRollbackAndPreserveTerminalEvidence(t *testing.T) {
	t.Parallel()
	for _, operation := range []string{"complete failure", "interrupt Run", "restart"} {
		t.Run(operation, func(t *testing.T) {
			t.Parallel()
			ctx := context.Background()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, notificationStorageDefinition(t), time.Now().UTC())
			first := notificationStorageStart(t)
			firstStep := automations.StepID("first")
			if err := recordStorageStep(ctx, repository, run.ID, firstStep, first); err != nil {
				t.Fatal(err)
			}
			if err := completeStorageStep(ctx, repository, run.ID, firstStep,
				automations.NotificationFinished{AttemptID: first.AttemptID,
					State: automations.NotificationSucceeded{CompletedAt: first.StartedAt}},
			); err != nil {
				t.Fatal(err)
			}
			second := notificationStorageStart(t)
			secondStep := automations.StepID("second")
			if err := recordStorageStep(ctx, repository, run.ID, secondStep, second); err != nil {
				t.Fatal(err)
			}
			before := retainedStorageRun(t, repository, record, run)
			mustExec(
				t,
				database,
				`CREATE TRIGGER fail_notification_parent BEFORE UPDATE ON automation_history BEGIN SELECT RAISE(ABORT, 'fault'); END`,
			)
			perform := func() error {
				switch operation {
				case "complete failure":
					return completeStorageStep(ctx, repository, run.ID, secondStep,
						automations.NotificationFinished{AttemptID: second.AttemptID,
							State: automations.NotificationFailed{
								CompletedAt: second.StartedAt,
								Evidence: automations.NotificationAttemptFailed{
									Outcome:    notifications.Unknown,
									ReasonCode: "provider_error",
								},
							}},
					)
				case "interrupt Run":
					return repository.InterruptRun(ctx, run.ID, time.Now().UTC())
				default:
					return repository.InterruptActiveRuns(ctx, time.Now().UTC(), automations.FailureCoreRestarted)
				}
			}
			if err := perform(); err == nil {
				t.Fatal("injected parent failure not reached")
			}
			if got := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(got, before) {
				t.Fatalf("transaction partially committed=%#v", got)
			}
			mustExec(t, database, `DROP TRIGGER fail_notification_parent`)
			if err := perform(); err != nil {
				t.Fatal(err)
			}
			got := retainedStorageRun(t, repository, record, run)
			firstAfter := requireStorageExecution(t, got, firstStep)
			firstBefore := requireStorageExecution(t, before, firstStep)
			secondAfter := requireStorageExecution(t, got, secondStep).Details.(automations.NotificationExecutionDetails)
			_, stillRunning := secondAfter.State.(automations.NotificationRunning)
			if !reflect.DeepEqual(firstAfter, firstBefore) ||
				stillRunning ||
				automations.RunStateStatus(got.State) == automations.RunRunning {
				t.Fatalf("terminal preservation=%#v", got)
			}
			if operation == "interrupt Run" {
				if err := repository.InterruptRun(ctx, run.ID, time.Now().UTC()); err != nil {
					t.Fatal(err)
				}
				if again := retainedStorageRun(t, repository, record, run); !reflect.DeepEqual(again, got) {
					t.Fatal("idempotent interruption changed terminal evidence")
				}
			}
		})
	}
}

// Retained reads independently reject semantically corrupt, SQL-valid evidence rather than falling back to live definitions.
func TestNotificationStorageCorruptionFailsHistoryRead(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"snapshot Step", "attempt ID", "position", "timestamp", "provider reason"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			database := openAutomationDatabase(t)
			repository := newAutomationRepository(t, database)
			record, run := admitStorageRun(t, repository, notificationStorageDefinition(t), time.Now().UTC())
			start := notificationStorageStart(t)
			if err := recordStorageStep(t.Context(), repository, run.ID, "first", start); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "snapshot Step":
				mustExec(
					t,
					database,
					`UPDATE automation_step_executions SET step_id = 'light_on' WHERE run_id = ?`,
					string(run.ID),
				)
			case "attempt ID":
				mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
				mustExec(
					t,
					database,
					`UPDATE automation_step_executions SET evidence_json = json_set(evidence_json, '$.attempt_id', 'bad') WHERE run_id = ?`,
					string(run.ID),
				)
				mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
			case "position":
				mustExec(
					t,
					database,
					`UPDATE automation_step_executions SET sequence = 1 WHERE run_id = ?`,
					string(run.ID),
				)
			case "timestamp":
				mustExec(
					t,
					database,
					`UPDATE automation_step_executions SET started_at = 'bad' WHERE run_id = ?`,
					string(run.ID),
				)
			case "provider reason":
				mustExec(t, database, `PRAGMA ignore_check_constraints = ON`)
				mustExec(
					t,
					database,
					`UPDATE automation_step_executions SET phase = 'failed', completed_at = started_at, evidence_json = json_set(evidence_json, '$.outcome', 'unknown', '$.reason_code', 'invented') WHERE run_id = ?`,
					string(run.ID),
				)
				mustExec(t, database, `PRAGMA ignore_check_constraints = OFF`)
			}
			if _, err := repository.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
				t.Fatal("corrupt history returned evidence")
			}
		})
	}
}

// A logical attempt link needs no notification row; restart preserves the link but never fabricates provider evidence.
func TestNotificationStorageEmptyTraceAndReservationWithoutAttempt(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	if got := retainedStorageRun(t, repository, record, run); len(got.Executions) != 0 {
		t.Fatalf("unreached notification executions=%#v", got.Executions)
	}
	if err := repository.InterruptRun(t.Context(), run.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := recordStorageStep(t.Context(), repository, run.ID, "first",
		notificationStorageStart(t),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("terminal parent accepted reservation=%v", err)
	}
}
