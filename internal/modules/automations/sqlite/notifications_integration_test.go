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
				Message:       "one",
			},
		},
		{
			ID: "second",
			Body: automations.NotificationStep{
				DestinationID: "ndst_01980000-0000-7000-8000-000000000001",
				Message:       "two",
			},
		},
	}, definition.Steps...)
	return definition
}

func notificationStorageStart(t *testing.T, run automations.Run) automations.NotificationStart {
	t.Helper()
	id, err := notifications.NewAttemptID()
	if err != nil {
		t.Fatal(err)
	}
	return automations.NotificationStart{RunID: run.ID, StepID: "first", AttemptID: id, StartedAt: time.Now().UTC()}
}

// Freely constructed repository writes cannot attach reservations to the wrong snapshot or adopt other attempt IDs.
//
//nolint:gocognit // Rejected transition matrix shares one pristine evidence oracle.
func TestNotificationStorageRejectsInvalidTransitions(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"invalid Run", "missing parent", "invalid Step", "Command", "missing Step", "invalid attempt", "negative position", "position gap", "position overflow", "zero time", "non UTC", "duplicate start", "second running", "wrong completion link", "accepted without outcome", "failed without reason", "outcome status mismatch", "pre-attempt invented outcome", "Run completed while running"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			repository := newAutomationRepository(t, openAutomationDatabase(t))
			record, run := admitStorageRun(t, repository, notificationStorageDefinition(t), time.Now().UTC())
			start := notificationStorageStart(t, run)
			mutated := start
			switch fault {
			case "invalid Run":
				mutated.RunID = "bad"
			case "missing parent":
				mutated.RunID = automations.RunID(newRunIDString(t))
			case "invalid Step":
				mutated.StepID = ""
			case "Command":
				mutated.StepID = "light_on"
			case "missing Step":
				mutated.StepID = "absent"
			case "invalid attempt":
				mutated.AttemptID = "bad"
			case "negative position":
				mutated.Position = -1
			case "position gap":
				mutated.Position = 1
			case "position overflow":
				mutated.Position = 32
			case "zero time":
				mutated.StartedAt = time.Time{}
			case "non UTC":
				mutated.StartedAt = start.StartedAt.In(time.FixedZone("offset", 3600))
			default:
				if err := repository.RecordNotificationStart(t.Context(), start); err != nil {
					t.Fatal(err)
				}
			}
			before := retainedStorageRun(t, repository, record, run)
			var err error
			switch fault {
			case "wrong completion link",
				"accepted without outcome",
				"failed without reason",
				"outcome status mismatch",
				"pre-attempt invented outcome":
				outcome := notifications.Accepted
				completion := automations.NotificationCompletion{
					RunID:       run.ID,
					StepID:      start.StepID,
					AttemptID:   start.AttemptID,
					Status:      automations.NotificationAccepted,
					CompletedAt: start.StartedAt,
					Outcome:     &outcome,
				}
				switch fault {
				case "wrong completion link":
					completion.AttemptID = "ntf_01980000-0000-7000-8000-000000000001"
				case "accepted without outcome":
					completion.Outcome = nil
				case "failed without reason":
					outcome = notifications.Unknown
					completion.Status = automations.NotificationFailed
				case "outcome status mismatch":
					completion.Status = automations.NotificationFailed
				case "pre-attempt invented outcome":
					code := automations.FailureDestinationNotFound
					outcome = notifications.NotSent
					completion.Status, completion.ReasonCode = automations.NotificationFailed, &code
				}
				err = repository.CompleteNotification(t.Context(), completion)
			case "Run completed while running":
				err = repository.CompleteRun(
					t.Context(),
					automations.RunCompletion{RunID: run.ID, Outcome: automations.SucceededRun{}},
				)
			case "second running":
				mutated = notificationStorageStart(t, run)
				mutated.StepID, mutated.Position = "second", 1
				err = repository.RecordNotificationStart(t.Context(), mutated)
			default:
				err = repository.RecordNotificationStart(t.Context(), mutated)
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
			first := notificationStorageStart(t, run)
			if err := repository.RecordNotificationStart(ctx, first); err != nil {
				t.Fatal(err)
			}
			outcome := notifications.Accepted
			if err := repository.CompleteNotification(
				ctx,
				automations.NotificationCompletion{
					RunID:       run.ID,
					StepID:      first.StepID,
					AttemptID:   first.AttemptID,
					Status:      automations.NotificationAccepted,
					Outcome:     &outcome,
					CompletedAt: first.StartedAt,
				},
			); err != nil {
				t.Fatal(err)
			}
			second := notificationStorageStart(t, run)
			second.StepID, second.Position = "second", 1
			if err := repository.RecordNotificationStart(ctx, second); err != nil {
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
					failedOutcome, reason := notifications.Unknown, "provider_error"
					return repository.CompleteNotification(
						ctx,
						automations.NotificationCompletion{
							RunID:       run.ID,
							StepID:      second.StepID,
							AttemptID:   second.AttemptID,
							Status:      automations.NotificationFailed,
							Outcome:     &failedOutcome,
							ReasonCode:  &reason,
							CompletedAt: second.StartedAt,
						},
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
			if !reflect.DeepEqual(got.Notifications[0], before.Notifications[0]) ||
				got.Notifications[1].Status == automations.NotificationRunning ||
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
			start := notificationStorageStart(t, run)
			if err := repository.RecordNotificationStart(t.Context(), start); err != nil {
				t.Fatal(err)
			}
			switch fault {
			case "snapshot Step":
				mustExec(t, database, `UPDATE automation_run_notifications SET step_id = 'light_on'`)
			case "attempt ID":
				mustExec(t, database, `UPDATE automation_run_notifications SET attempt_id = 'bad'`)
			case "position":
				mustExec(t, database, `UPDATE automation_run_notifications SET position = 1`)
			case "timestamp":
				mustExec(t, database, `UPDATE automation_run_notifications SET started_at = 'bad'`)
			case "provider reason":
				mustExec(
					t,
					database,
					`UPDATE automation_run_notifications SET status = 'failed', outcome = 'unknown', reason_code = 'invented', completed_at = started_at`,
				)
			}
			if _, err := repository.GetHistoryEntry(t.Context(), record.ID, string(run.ID)); err == nil {
				t.Fatal("corrupt history returned evidence")
			}
		})
	}
}

// A logical attempt link needs no notification row; restart preserves the link but never fabricates provider evidence.
func TestNotificationStorageEmptyLegacyAndReservationWithoutAttempt(t *testing.T) {
	t.Parallel()
	repository := newAutomationRepository(t, openAutomationDatabase(t))
	record, run := admitStorageRun(t, repository, validDomainDefinition(t), time.Now().UTC())
	if got := retainedStorageRun(t, repository, record, run); got.Notifications == nil || len(got.Notifications) != 0 {
		t.Fatalf("legacy notifications=%#v", got.Notifications)
	}
	if err := repository.InterruptRun(t.Context(), run.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := repository.RecordNotificationStart(
		t.Context(),
		notificationStorageStart(t, run),
	); !errors.Is(
		err,
		automations.ErrInvalidAutomation,
	) {
		t.Fatalf("terminal parent accepted reservation=%v", err)
	}
}
