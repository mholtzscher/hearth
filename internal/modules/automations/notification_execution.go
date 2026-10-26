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
	start := NotificationStart{
		RunID:     run.ID,
		StepID:    stepID,
		AttemptID: id,
		StartedAt: service.dependencies.Now().UTC(),
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.RecordStep(
		writeContext,
		StepExecutionRecord{
			RunID:    run.ID,
			StepID:   stepID,
			Evidence: NotificationStarted{StartedAt: start.StartedAt, AttemptID: id},
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
	completion, established := service.reconcileNotification(ctx, step, start, sendErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	writeContext, cancel = context.WithTimeout(ctx, automationPersistenceTimeout)
	err = service.repository.CompleteStep(
		writeContext,
		StepExecutionCompletion{
			RunID:  run.ID,
			StepID: stepID,
			Evidence: NotificationFinished{
				CompletedAt: completion.CompletedAt,
				AttemptID:   id,
				Outcome:     completion.Outcome,
				ReasonCode:  completion.ReasonCode,
			},
		},
	)
	cancel()
	if err != nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	return completion.Status == NotificationAccepted
}

func (service *Service) reconcileNotification(
	ctx context.Context,
	step NotificationStep,
	start NotificationStart,
	sendErr error,
) (NotificationCompletion, bool) {
	readContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
	defer cancel()
	attempt, err := service.dependencies.Notifications.GetAttempt(readContext, start.AttemptID)
	// Input/identity conflicts cannot turn an existing row into this invocation's
	// outcome, even if a colliding attempt happens to carry identical message bytes.
	if errors.Is(sendErr, notifications.ErrInvalidInput) || errors.Is(sendErr, notifications.ErrAttemptIDConflict) {
		return NotificationCompletion{}, false
	}
	completion := NotificationCompletion{
		RunID: start.RunID, StepID: start.StepID, AttemptID: start.AttemptID,
		CompletedAt: service.dependencies.Now().UTC(), Status: NotificationFailed,
	}
	if errors.Is(err, notifications.ErrAttemptNotFound) {
		// Only documented pre-admission failures establish a truthful no-attempt result.
		switch {
		case errors.Is(sendErr, notifications.ErrPersistence):
			return NotificationCompletion{}, false
		case errors.Is(sendErr, notifications.ErrDestinationNotFound):
			code := FailureDestinationNotFound
			completion.ReasonCode = &code
		case errors.Is(sendErr, notifications.ErrAdmissionClosed):
			code := FailureNotificationAdmissionClosed
			completion.ReasonCode = &code
		default:
			return NotificationCompletion{}, false
		}
		return completion, true
	}
	// Definitive pre-admission failures cannot establish ownership of an existing
	// attempt, even when its reserved identity and message happen to match.
	if errors.Is(sendErr, notifications.ErrAdmissionClosed) ||
		errors.Is(sendErr, notifications.ErrDestinationNotFound) {
		return NotificationCompletion{}, false
	}
	if err != nil || attempt.ID != start.AttemptID || attempt.DestinationID != step.DestinationID ||
		attempt.Message != (notifications.Message{Title: step.Title, Text: step.Message}) || attempt.CompletedAt == nil ||
		notifications.ValidateEvidence(
			notifications.Evidence{Outcome: attempt.Outcome, ReasonCode: attempt.ReasonCode, Receipt: attempt.Receipt},
		) != nil {
		return NotificationCompletion{}, false
	}
	completion.Outcome = &attempt.Outcome
	if attempt.Outcome == notifications.Accepted {
		completion.Status = NotificationAccepted
	} else {
		completion.ReasonCode = &attempt.ReasonCode
	}
	return completion, true
}
