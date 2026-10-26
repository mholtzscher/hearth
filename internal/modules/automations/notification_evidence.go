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
	switch state := state.(type) {
	case NotificationRunning:
		return nil, nil //nolint:nilnil // Running executions have no completion timestamp.
	case NotificationSucceeded:
		return &state.CompletedAt, nil
	case NotificationFailed:
		return &state.CompletedAt, nil
	case NotificationInterrupted:
		return &state.CompletedAt, nil
	default:
		return nil, invalid("unsupported Notification execution state %T", state)
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
