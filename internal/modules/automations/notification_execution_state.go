package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

const (
	FailureDestinationNotFound         = "destination_not_found"
	FailureNotificationAdmissionClosed = "notification_admission_closed"
)

//sumtype:decl
type NotificationExecutionState interface{ isNotificationExecutionState() }

//sumtype:decl
type NotificationTerminalState interface {
	NotificationExecutionState
	isNotificationTerminalState()
}

type NotificationRunning struct{}

func (NotificationRunning) isNotificationExecutionState() {}

type NotificationSucceeded struct{ CompletedAt time.Time }

func (NotificationSucceeded) isNotificationExecutionState() {}
func (NotificationSucceeded) isNotificationTerminalState()  {}

type NotificationFailed struct {
	CompletedAt time.Time
	Evidence    NotificationFailure
}

func (NotificationFailed) isNotificationExecutionState() {}
func (NotificationFailed) isNotificationTerminalState()  {}

type NotificationInterrupted struct {
	CompletedAt time.Time
	ReasonCode  string
}

func (NotificationInterrupted) isNotificationExecutionState() {}
func (NotificationInterrupted) isNotificationTerminalState()  {}

//sumtype:decl
type NotificationFailure interface{ isNotificationFailure() }

type NotificationAdmissionRejected struct{ ReasonCode string }

func (NotificationAdmissionRejected) isNotificationFailure() {}

type NotificationAttemptFailed struct {
	Outcome    notifications.Outcome
	ReasonCode string
}

func (NotificationAttemptFailed) isNotificationFailure() {}

// Validate checks intrinsic Notification execution evidence and its reserved Attempt identity.
func (details NotificationExecutionDetails) Validate() error {
	if err := ValidateNotificationExecutionState(details.State); err != nil {
		return err
	}
	if notifications.ValidateID(string(details.AttemptID), "ntf_") != nil ||
		!isValidExecutionTimestamp(details.StartedAt) {
		return invalid("invalid Notification identity or start time")
	}
	return nil
}

// Kind identifies a Notification execution.
func (NotificationExecutionDetails) Kind() StepKind { return StepKindNotification }

// Phase derives the lifecycle label from a validated Notification state.
func (details NotificationExecutionDetails) Phase() string {
	switch details.State.(type) {
	case NotificationRunning:
		return executionRunning
	case NotificationSucceeded:
		return executionSucceeded
	case NotificationFailed:
		return executionFailed
	case NotificationInterrupted:
		return executionInterrupted
	default:
		panic("unsupported validated Notification state")
	}
}

// StartTime returns the recorded Notification reach time.
func (details NotificationExecutionDetails) StartTime() time.Time { return details.StartedAt }

// CompletionTime projects a validated Notification state; running has no completion.
func (details NotificationExecutionDetails) CompletionTime() *time.Time {
	return notificationStateCompletion(details.State)
}

// RunFailure preserves the distinct attempt and admission failure reasons.
func (details NotificationExecutionDetails) RunFailure() (RunOutcome, bool) {
	switch state := details.State.(type) {
	case NotificationInterrupted:
		return InterruptedRun{FailureCode: state.ReasonCode}, true
	case NotificationFailed:
		switch evidence := state.Evidence.(type) {
		case NotificationAdmissionRejected:
			return FailedRun{FailureCode: evidence.ReasonCode}, true
		case NotificationAttemptFailed:
			return FailedRun{FailureCode: evidence.ReasonCode}, true
		default:
			return nil, false
		}
	case NotificationRunning, NotificationSucceeded:
		return nil, false
	default:
		return nil, false
	}
}

func ValidateNotificationExecutionState(state NotificationExecutionState) error {
	switch state := state.(type) {
	case NotificationRunning:
		return nil
	case NotificationSucceeded:
		if isValidExecutionTimestamp(state.CompletedAt) {
			return nil
		}
	case NotificationFailed:
		if !isValidExecutionTimestamp(state.CompletedAt) {
			break
		}
		switch evidence := state.Evidence.(type) {
		case NotificationAdmissionRejected:
			if evidence.ReasonCode == FailureDestinationNotFound ||
				evidence.ReasonCode == FailureNotificationAdmissionClosed {
				return nil
			}
		case NotificationAttemptFailed:
			if evidence.Outcome != notifications.Accepted &&
				notifications.ValidateEvidence(
					notifications.Evidence{Outcome: evidence.Outcome, ReasonCode: evidence.ReasonCode},
				) == nil {
				return nil
			}
		}
	case NotificationInterrupted:
		if isValidExecutionTimestamp(state.CompletedAt) &&
			(state.ReasonCode == FailureCoreStopping || state.ReasonCode == FailureCoreRestarted || state.ReasonCode == FailureExecutorFault) {
			return nil
		}
	default:
		return invalid("unsupported Notification execution state %T", state)
	}
	return invalid("invalid Notification execution state")
}

func NotificationStateCompletion(state NotificationExecutionState) (*time.Time, error) {
	if err := ValidateNotificationExecutionState(state); err != nil {
		return nil, err
	}
	return notificationStateCompletion(state), nil
}

// notificationStateCompletion projects a validated Notification state.
func notificationStateCompletion(state NotificationExecutionState) *time.Time {
	switch state := state.(type) {
	case NotificationRunning:
		return nil
	case NotificationSucceeded:
		return &state.CompletedAt
	case NotificationFailed:
		return &state.CompletedAt
	case NotificationInterrupted:
		return &state.CompletedAt
	default:
		panic("unsupported validated Notification state")
	}
}

// NotificationWithPreparedSnapshot resolves evidence against an unchanged normalized snapshot.
func NotificationWithPreparedSnapshot(snapshot Definition, id StepID) (NotificationStep, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step != nil {
		if body, ok := step.Body.(NotificationStep); ok {
			return body, nil
		}
	}
	return NotificationStep{}, invalid("snapshot Step %q is not a notification", id)
}
