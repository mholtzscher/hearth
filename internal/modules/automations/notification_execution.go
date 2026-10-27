package automations

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// executeNotification reserves its link before Send and advances only after authoritative acceptance commits.
func (service *Service) executeNotification(
	ctx context.Context,
	run Run,
	stepID StepID,
	step NotificationStep,
	values *runValues,
) bool {
	if !service.executionOpen() {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	path := stepBindingPath(run.Snapshot.Steps, stepID, "/steps")
	title, err := values.resolve(ctx, step.Title, path+"/title")
	if err != nil {
		return service.failValues(ctx, run, stepID, StepKindNotification, path+"/title", err, nil)
	}
	text, err := values.resolve(ctx, step.Message, path+"/message")
	if err != nil {
		return service.failValues(ctx, run, stepID, StepKindNotification, path+"/message", err, nil)
	}
	titleText, titleOK := title.Data.(string)
	messageText, messageOK := text.Data.(string)
	message := notifications.Message{Title: titleText, Text: messageText}
	if !titleOK || !messageOK || notifications.ValidateMessage(message) != nil {
		return service.failValues(
			ctx,
			run,
			stepID,
			StepKindNotification,
			path,
			&exprvalue.Failure{Code: failureResolvedInputInvalid},
			nil,
		)
	}
	inputs, err := encodeResolvedInputs(map[string]json.RawMessage{"title": title.JSON, "message": text.JSON})
	if err != nil {
		return service.failValues(ctx, run, stepID, StepKindNotification, path, err, nil)
	}
	if !service.executionOpen() || ctx.Err() != nil {
		service.interruptBranchRun(ctx, run.ID, stepID, service.executionStopReason())
		return false
	}
	id, err := service.dependencies.NewNotificationAttemptID()
	if err != nil || service.dependencies.Notifications == nil {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	startedAt := service.dependencies.Now().UTC()
	if !service.commitReached(ctx, run,
		StepExecutionRecord{
			RunID:    run.ID,
			StepID:   stepID,
			Inputs:   inputs,
			Evidence: NotificationStarted{StartedAt: startedAt, AttemptID: id},
		}, StepKindNotification, path) {
		return false
	}
	// A persisted reservation belongs to admitted work even if drain closes now.
	// Notifications stays available until Automation workers have joined.
	_, sendErr := service.dependencies.Notifications.Send(ctx, notifications.SendInput{
		AttemptID: id, DestinationID: step.DestinationID,
		Message: message,
	})
	terminal, established := service.reconcileNotification(ctx, step, message, id, sendErr)
	if !established {
		service.interruptRunExecutorFault(ctx, run.ID, stepID)
		return false
	}
	writeContext, cancel := context.WithTimeout(ctx, automationPersistenceTimeout)
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
	message notifications.Message,
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
		attempt.Message != message || attempt.CompletedAt == nil ||
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
