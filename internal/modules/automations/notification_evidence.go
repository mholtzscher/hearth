package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// NotificationStatus describes reached notification Step evidence, not provider delivery.
type NotificationStatus string

const (
	NotificationRunning                NotificationStatus = "running"
	NotificationAccepted               NotificationStatus = "accepted"
	NotificationFailed                 NotificationStatus = "failed"
	NotificationInterrupted            NotificationStatus = "interrupted"
	FailureDestinationNotFound                            = "destination_not_found"
	FailureNotificationAdmissionClosed                    = "notification_admission_closed"
)

type NotificationStart struct {
	RunID     RunID
	StepID    StepID
	Position  int
	AttemptID notifications.AttemptID
	StartedAt time.Time
}

// NotificationCompletion terminalizes the reached Step; failure/interruption also ends its Run atomically.
type NotificationCompletion struct {
	RunID       RunID
	StepID      StepID
	AttemptID   notifications.AttemptID
	Status      NotificationStatus
	CompletedAt time.Time
	Outcome     *notifications.Outcome
	ReasonCode  *string
}

func ValidateNotificationStart(start NotificationStart) error {
	if _, err := ParseRunID(string(start.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(start.StepID)); err != nil {
		return err
	}
	if notifications.ValidateID(string(start.AttemptID), "ntf_") != nil ||
		start.Position < 0 || start.Position >= 32 || !isValidExecutionTimestamp(start.StartedAt) {
		return invalid("invalid notification reservation")
	}
	return nil
}

func ValidateNotificationCompletion(completion NotificationCompletion) error {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return err
	}
	if notifications.ValidateID(string(completion.AttemptID), "ntf_") != nil ||
		!isValidExecutionTimestamp(completion.CompletedAt) {
		return invalid("invalid notification completion identity or time")
	}
	return validateNotificationTerminalEvidence(completion)
}

// validateNotificationTerminalEvidence consumes identity/time validation owned by its boundary.
func validateNotificationTerminalEvidence(completion NotificationCompletion) error {
	if completion.Outcome != nil {
		return validateNotificationProviderCompletion(completion)
	}
	if completion.ReasonCode != nil {
		switch completion.Status {
		case NotificationFailed:
			if *completion.ReasonCode == FailureDestinationNotFound ||
				*completion.ReasonCode == FailureNotificationAdmissionClosed {
				return nil
			}
		case NotificationInterrupted:
			if *completion.ReasonCode == FailureCoreStopping || *completion.ReasonCode == FailureCoreRestarted ||
				*completion.ReasonCode == FailureExecutorFault {
				return nil
			}
		case NotificationRunning, NotificationAccepted:
		}
	}
	return invalid("notification completion lacks terminal evidence")
}

func validateNotificationProviderCompletion(completion NotificationCompletion) error {
	reason := ""
	if completion.ReasonCode != nil {
		reason = *completion.ReasonCode
	}
	if notifications.ValidateEvidence(notifications.Evidence{Outcome: *completion.Outcome, ReasonCode: reason}) != nil {
		return invalid("invalid notification outcome evidence")
	}
	if *completion.Outcome == notifications.Accepted && completion.Status == NotificationAccepted &&
		completion.ReasonCode == nil {
		return nil
	}
	if *completion.Outcome != notifications.Accepted && completion.Status == NotificationFailed {
		return nil
	}
	return invalid("notification status disagrees with outcome")
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
