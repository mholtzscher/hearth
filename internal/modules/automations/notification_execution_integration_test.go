//go:build integration

package automations_test

import (
	"context"
	"errors"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/automations"
	automationssqlite "github.com/mholtzscher/hearth/internal/modules/automations/sqlite"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// V2: real attempt persistence, not the Send return, governs sequential continuation.
//
//nolint:gocognit // One outcome matrix checks durable evidence and downstream effects together.
func TestNotificationExecutionOutcomesGateNextCommand(t *testing.T) {
	t.Parallel()
	for _, evidence := range []notifications.Evidence{
		{Outcome: notifications.Accepted, Receipt: "receipt"},
		{Outcome: notifications.Rejected, ReasonCode: "provider_forbidden"},
		{Outcome: notifications.NotSent, ReasonCode: "adapter_busy"},
		{Outcome: notifications.Unknown, ReasonCode: "provider_error"},
	} {
		t.Run(string(evidence.Outcome), func(t *testing.T) {
			t.Parallel()
			var sends atomic.Int32
			fixture := newNotificationFixture(
				t,
				func(_ context.Context, attempt notifications.Attempt) notifications.Evidence {
					sends.Add(1)
					if attempt.Message != (notifications.Message{Title: "Freezer", Text: " Too warm.\n"}) {
						t.Error("message changed")
					}
					return evidence
				},
			)
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = fixture.service
			devices := newScriptedDevices()
			repo := automationssqlite.NewAutomationRepository(fixture.database, dependencies)
			service := automations.NewService(repo, devices, dependencies)
			record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := retainedNotificationRun(t, service, record, run)
			wantStatus, wantCommands := automations.RunFailed, 0
			if evidence.Outcome == notifications.Accepted {
				wantStatus, wantCommands = automations.RunSucceeded, 1
			}
			wantExecutions := 1 + wantCommands
			if automations.RunStateStatus(got.State) != wantStatus || devices.executionCount() != wantCommands ||
				sends.Load() != 1 || len(got.Executions) != wantExecutions {
				t.Fatalf("Run=%#v Commands=%d sends=%d", got, devices.executionCount(), sends.Load())
			}
			execution := requireExecutionForStep(t, got, "notify")
			entry := execution.Details.(automations.NotificationExecutionDetails)
			attempt, err := fixture.service.GetAttempt(t.Context(), entry.AttemptID)
			if err != nil || attempt.Outcome != evidence.Outcome {
				t.Fatalf("evidence=%#v attempt=%#v err=%v", entry, attempt, err)
			}
			if evidence.Outcome == notifications.Accepted {
				if _, ok := entry.State.(automations.NotificationSucceeded); !ok {
					t.Fatalf("accepted state=%#v", entry.State)
				}
			} else {
				failed, ok := entry.State.(automations.NotificationFailed)
				attemptFailure, evidenceOK := failed.Evidence.(automations.NotificationAttemptFailed)
				if !ok || !evidenceOK || attemptFailure.Outcome != evidence.Outcome ||
					attemptFailure.ReasonCode != evidence.ReasonCode {
					t.Fatalf("failed state=%#v", entry.State)
				}
			}
		})
	}
}

// Selected nested leaves alone acquire attempts; positions remain independent of Commands.
func TestNotificationExecutionSelectedBranchesOnly(t *testing.T) {
	t.Parallel()
	var sends atomic.Int32
	fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
		sends.Add(1)
		return notifications.Evidence{Outcome: notifications.Accepted}
	})
	dependencies := runtimeTestDependencies()
	dependencies.Notifications = fixture.service
	scripted := newScriptedDevices()
	service := automations.NewService(
		automationssqlite.NewAutomationRepository(fixture.database, dependencies),
		scripted,
		dependencies,
	)
	definition := notificationRuntimeDefinition(t, fixture.destination.ID, 1)
	notify := definition.Steps[0]
	leaf := func(id string) automations.Step { node := notify; node.ID = automations.StepID(id); return node }
	condition := automations.Condition{
		ID:   "trigger",
		Body: automations.TriggerCondition{TriggerIDs: []automations.TriggerID{"trigger"}},
	}
	definition.Steps = []automations.Step{leaf("top"), {ID: "if", Body: automations.IfStep{
		Conditions: condition, Then: []automations.Step{leaf("unselected")}, Else: []automations.Step{
			{
				ID: "choose",
				Body: automations.ChooseStep{
					Branches: []automations.ChooseBranch{
						{ID: "arm", Conditions: condition, Steps: []automations.Step{leaf("unchosen")}},
					},
					Default: []automations.Step{leaf("fallback")},
				},
			},
		},
	}}, definition.Steps[1]}
	record := createRuntimeAutomation(t, service, definition)
	run := admitDelayRun(t, service, record)
	waitForRuns(t, service)
	got := retainedNotificationRun(t, service, record, run)
	if automations.RunStateStatus(got.State) != automations.RunSucceeded || sends.Load() != 2 ||
		scripted.executionCount() != 1 ||
		len(got.Executions) != 5 ||
		len(branchDecisions(got)) != 2 {
		t.Fatalf("selected Run=%#v sends=%d", got, sends.Load())
	}
	for sequence, id := range []automations.StepID{"top", "if", "choose", "fallback"} {
		if got.Executions[sequence].Sequence != sequence || got.Executions[sequence].StepID != id {
			t.Fatalf("reach order=%#v", got.Executions)
		}
	}
	lastCommand := automations.CommandLeaves(got.Snapshot.Steps)[0]
	if got.Executions[4].Sequence != 4 || got.Executions[4].StepID != lastCommand.ID {
		t.Fatalf("trailing command execution order=%#v", got.Executions)
	}
}

// Missing/nonterminal/mismatched evidence must never be adopted or retried, even if Send claims acceptance.
//
//nolint:gocognit // A seam fault matrix keeps authoritative and returned evidence distinct.
func TestNotificationReconciliationRejectsUnverifiableEvidence(t *testing.T) {
	t.Parallel()
	for _, fault := range []string{"missing", "read failure", "ID", "destination", "title", "message", "nonterminal", "invalid outcome", "invalid reason", "accepted return rejected row", "Send error accepted row"} {
		t.Run(fault, func(t *testing.T) {
			t.Parallel()
			var sends atomic.Int32
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				sends.Add(1)
				if fault == "accepted return rejected row" {
					return notifications.Evidence{Outcome: notifications.Rejected, ReasonCode: "provider_rejected"}
				}
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			seam := notificationReadOverride{AutomationNotifications: fixture.service}
			seam.read = func(ctx context.Context, id notifications.AttemptID) (notifications.Attempt, error) {
				attempt, err := fixture.service.GetAttempt(ctx, id)
				if err != nil {
					return attempt, err
				}
				switch fault {
				case "missing":
					return notifications.Attempt{}, notifications.ErrAttemptNotFound
				case "read failure":
					return notifications.Attempt{}, errors.New("read failed")
				case "ID":
					attempt.ID = "ntf_01980000-0000-7000-8000-000000000099"
				case "destination":
					attempt.DestinationID = notificationDestination
				case "title":
					attempt.Message.Title = "other"
				case "message":
					attempt.Message.Text = "other"
				case "nonterminal":
					attempt.CompletedAt = nil
				case "invalid outcome":
					attempt.Outcome = "bad"
				case "invalid reason":
					attempt.ReasonCode = "bad"
				}
				return attempt, nil
			}
			seam.send = func(ctx context.Context, in notifications.SendInput) (notifications.Attempt, error) {
				attempt, err := fixture.service.Send(ctx, in)
				if fault == "Send error accepted row" {
					return notifications.Attempt{}, notifications.ErrPersistence
				}
				if fault == "accepted return rejected row" {
					attempt.Outcome, attempt.ReasonCode = notifications.Accepted, ""
				}
				return attempt, err
			}
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = seam
			scripted := newScriptedDevices()
			service := automations.NewService(
				automationssqlite.NewAutomationRepository(fixture.database, dependencies),
				scripted,
				dependencies,
			)
			record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := retainedNotificationRun(t, service, record, run)
			want, commands := automations.RunInterrupted, 0
			if fault == "accepted return rejected row" {
				want = automations.RunFailed
			}
			if fault == "Send error accepted row" {
				want, commands = automations.RunSucceeded, 1
			}
			if automations.RunStateStatus(got.State) != want || scripted.executionCount() != commands ||
				sends.Load() != 1 ||
				!service.AdmissionOpen() {
				t.Fatalf("Run=%#v sends=%d Commands=%d", got, sends.Load(), scripted.executionCount())
			}
			if want == automations.RunInterrupted {
				entry := requireNotificationExecution(t, got, "notify")
				if _, ok := entry.State.(automations.NotificationInterrupted); !ok {
					t.Fatalf("unexpected state=%#v", entry.State)
				}
			}
		})
	}
}

// Definition references survive runtime replacement; lack of current registration is real not-sent evidence.
//
//nolint:gocognit // The three real lifecycle failures share one fixture and attribution oracle.
func TestNotificationRuntimeUnavailableAndPreAttemptFailures(t *testing.T) {
	t.Parallel()
	for _, failure := range []string{"unavailable", "missing destination", "closed admission"} {
		t.Run(failure, func(t *testing.T) {
			t.Parallel()
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				t.Error("failure dispatched externally")
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = fixture.service
			scripted := newScriptedDevices()
			service := automations.NewService(
				automationssqlite.NewAutomationRepository(fixture.database, dependencies),
				scripted,
				dependencies,
			)
			record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 1))
			switch failure {
			case "unavailable":
				registration := fixture.registration
				if err := fixture.runtimes.ReleaseAdapterRuntime(
					t.Context(),
					adapters.ReleaseRuntimeWrite{
						AdapterID:  registration.AdapterID,
						RuntimeID:  adapters.RuntimeID(registration.RuntimeID),
						ReleasedAt: time.Now().UTC(),
					},
				); err != nil {
					t.Fatal(err)
				}
				id, err := uuid.NewV7()
				if err != nil {
					t.Fatal(err)
				}
				registration.RuntimeID = "run_" + id.String()
				claimNotificationRuntime(t, fixture.runtimes, registration)
			case "missing destination":
				if _, err := fixture.database.Exec(
					`DELETE FROM notification_destinations WHERE id = ?`,
					string(fixture.destination.ID),
				); err != nil {
					t.Fatal(err)
				}
			case "closed admission":
				if err := fixture.service.Drain(t.Context()); err != nil {
					t.Fatal(err)
				}
			}
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			got := retainedNotificationRun(t, service, record, run)
			if automations.RunStateStatus(got.State) != automations.RunFailed || scripted.executionCount() != 0 ||
				len(got.Executions) != 1 {
				t.Fatalf("failure Run=%#v", got)
			}
			entry := requireNotificationExecution(t, got, "notify")
			attempt, err := fixture.service.GetAttempt(t.Context(), entry.AttemptID)
			if failure == "unavailable" {
				failed, ok := entry.State.(automations.NotificationFailed)
				attemptFailure, evidenceOK := failed.Evidence.(automations.NotificationAttemptFailed)
				if err != nil || attempt.Outcome != notifications.NotSent || !ok || !evidenceOK ||
					attemptFailure.Outcome != notifications.NotSent || attemptFailure.ReasonCode != "destination_unavailable" {
					t.Fatalf("unavailable=%#v %#v %v", entry, attempt, err)
				}
			} else {
				code := automations.FailureDestinationNotFound
				if failure == "closed admission" {
					code = automations.FailureNotificationAdmissionClosed
				}
				failed, ok := entry.State.(automations.NotificationFailed)
				admission, evidenceOK := failed.Evidence.(automations.NotificationAdmissionRejected)
				if !errors.Is(err, notifications.ErrAttemptNotFound) || !ok || !evidenceOK ||
					admission.ReasonCode != code {
					t.Fatalf("pre-attempt=%#v %v", entry, err)
				}
			}
		})
	}
}

// V3: pruning either owner never removes or changes the other owner's retained evidence.
//
//nolint:gocognit,nestif // Each direction checks absence at one owner and unchanged evidence at the other.
func TestNotificationAndAutomationHistoryPruneIndependently(t *testing.T) {
	t.Parallel()
	for _, first := range []string{"attempt", "Run"} {
		t.Run(first, func(t *testing.T) {
			t.Parallel()
			fixture := newNotificationFixture(t, func(context.Context, notifications.Attempt) notifications.Evidence {
				return notifications.Evidence{Outcome: notifications.Accepted}
			})
			dependencies := runtimeTestDependencies()
			dependencies.Notifications = fixture.service
			repo := automationssqlite.NewAutomationRepository(fixture.database, dependencies)
			service := automations.NewService(repo, newScriptedDevices(), dependencies)
			record := createRuntimeAutomation(t, service, notificationRuntimeDefinition(t, fixture.destination.ID, 0))
			run := admitDelayRun(t, service, record)
			waitForRuns(t, service)
			before := retainedNotificationRun(t, service, record, run)
			id := requireNotificationExecution(t, before, "notify").AttemptID
			attempt, err := fixture.service.GetAttempt(t.Context(), id)
			if err != nil {
				t.Fatal(err)
			}
			cutoff := time.Now().UTC().Add(time.Hour)
			if first == "attempt" {
				count, pruneErr := fixture.repository.DeleteHistoryBefore(t.Context(), cutoff, 100)
				if pruneErr != nil || count != 1 {
					t.Fatalf("attempt prune=%d %v", count, pruneErr)
				}
				if _, err = fixture.service.GetAttempt(
					t.Context(),
					id,
				); !errors.Is(
					err,
					notifications.ErrAttemptNotFound,
				) {
					t.Fatalf("attempt remains=%v", err)
				}
				if got := retainedNotificationRun(t, service, record, run); !reflect.DeepEqual(got, before) {
					t.Fatalf("attempt pruning changed Run=%#v", got)
				}
			} else {
				count, pruneErr := repo.DeleteHistoryBefore(t.Context(), cutoff, 100)
				if pruneErr != nil || count != 1 {
					t.Fatalf("Run prune=%d %v", count, pruneErr)
				}
				if _, err = service.GetHistoryEntry(
					t.Context(),
					record.ID,
					string(run.ID),
				); !errors.Is(
					err,
					automations.ErrHistoryNotFound,
				) {
					t.Fatalf("Run remains=%v", err)
				}
				got, readErr := fixture.service.GetAttempt(t.Context(), id)
				if readErr != nil || !reflect.DeepEqual(got, attempt) {
					t.Fatalf("Run prune changed attempt=%#v %v", got, readErr)
				}
				var children int
				if err = fixture.database.QueryRow(
					`SELECT count(*) FROM automation_step_executions WHERE run_id = ? AND kind = 'notification'`,
					string(run.ID),
				).
					Scan(&children); err != nil ||
					children != 0 {
					t.Fatalf("Run child cascade=%d %v", children, err)
				}
			}
		})
	}
}
