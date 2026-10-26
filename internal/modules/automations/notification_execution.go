package automations

import (
	"context"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// executeNotification reserves its link before Send and advances only after authoritative acceptance commits.
func (service *Service) executeNotification(
	ctx context.Context,
	run Run,
	stepID StepID,
	step NotificationStep,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	id, err := service.dependencies.NewNotificationAttemptID()
	if err != nil || service.dependencies.Notifications == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	startedAt := service.dependencies.Now().UTC()
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.RecordStep(
		writeContext,
		StepExecutionRecord{
			RunID:    run.ID,
			StepID:   stepID,
			Evidence: NotificationStarted{StartedAt: startedAt, AttemptID: id},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	// A persisted reservation belongs to admitted work even if drain closes now.
	// Notifications stays available until Automation workers have joined.
	_, sendErr := service.dependencies.Notifications.Send(ctx, notifications.SendInput{
		AttemptID: id, DestinationID: step.DestinationID,
		Message: notifications.Message{Title: step.Title, Text: step.Message},
	})
	terminal, established := service.reconcileNotification(ctx, step, id, sendErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	writeContext, cancel = context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.CompleteStep(
		writeContext,
		StepExecutionCompletion{
			RunID:    run.ID,
			StepID:   stepID,
			Evidence: NotificationFinished{AttemptID: id, State: terminal},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	_, succeeded := terminal.(NotificationSucceeded)
	return succeeded
}

func (service *Service) reconcileNotification(
	ctx context.Context,
	step NotificationStep,
	attemptID notifications.AttemptID,
	sendErr error,
) (NotificationTerminalState, bool) {
	if notifications.ValidateID(string(attemptID), "ntf_") != nil {
		return nil, false
	}
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	attempt, err := service.dependencies.Notifications.GetAttempt(readContext, attemptID)
	// Input/identity conflicts cannot turn an existing row into this invocation's
	// outcome, even if a colliding attempt happens to carry identical message bytes.
	if errors.Is(sendErr, notifications.ErrInvalidInput) || errors.Is(sendErr, notifications.ErrAttemptIDConflict) {
		return nil, false
	}
	completedAt := service.dependencies.Now().UTC()
	if errors.Is(err, notifications.ErrAttemptNotFound) {
		// Only documented pre-admission failures establish a truthful no-attempt result.
		switch {
		case errors.Is(sendErr, notifications.ErrPersistence):
			return nil, false
		case errors.Is(sendErr, notifications.ErrDestinationNotFound):
			return NotificationFailed{
				CompletedAt: completedAt,
				Evidence:    NotificationAdmissionRejected{ReasonCode: FailureDestinationNotFound},
			}, true
		case errors.Is(sendErr, notifications.ErrAdmissionClosed):
			return NotificationFailed{
				CompletedAt: completedAt,
				Evidence:    NotificationAdmissionRejected{ReasonCode: FailureNotificationAdmissionClosed},
			}, true
		default:
			return nil, false
		}
	}
	// Definitive pre-admission failures cannot establish ownership of an existing
	// attempt, even when its reserved identity and message happen to match.
	if errors.Is(sendErr, notifications.ErrAdmissionClosed) ||
		errors.Is(sendErr, notifications.ErrDestinationNotFound) {
		return nil, false
	}
	if err != nil || attempt.ID != attemptID || attempt.DestinationID != step.DestinationID ||
		attempt.Message != (notifications.Message{Title: step.Title, Text: step.Message}) || attempt.CompletedAt == nil ||
		notifications.ValidateEvidence(
			notifications.Evidence{Outcome: attempt.Outcome, ReasonCode: attempt.ReasonCode, Receipt: attempt.Receipt},
		) != nil {
		return nil, false
	}
	if attempt.Outcome == notifications.Accepted {
		return NotificationSucceeded{CompletedAt: completedAt}, true
	}
	return NotificationFailed{
		CompletedAt: completedAt,
		Evidence:    NotificationAttemptFailed{Outcome: attempt.Outcome, ReasonCode: attempt.ReasonCode},
	}, true
}
