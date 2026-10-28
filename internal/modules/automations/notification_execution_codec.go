package automations

import "github.com/mholtzscher/hearth/internal/modules/notifications"

type notificationWire struct {
	Inputs     *notificationInputsJSON `json:"inputs,omitempty"`
	AttemptID  notifications.AttemptID `json:"attempt_id"`
	Outcome    *notifications.Outcome  `json:"outcome,omitempty"`
	ReasonCode string                  `json:"reason_code,omitempty"`
}

// Pointers distinguish missing/null fields from empty but valid retained text.
// Field order preserves the previous sorted input-envelope encoding.
type notificationInputsJSON struct {
	Message *string `json:"message"`
	Title   *string `json:"title"`
}

func (details NotificationExecutionDetails) encodeValidated() (any, error) {
	wire := notificationWire{AttemptID: details.AttemptID}
	if details.Message != nil {
		wire.Inputs = &notificationInputsJSON{Title: &details.Message.Title, Message: &details.Message.Text}
	}
	switch state := details.State.(type) {
	case NotificationRunning:
		return wire, nil
	case NotificationSucceeded:
		wire.Outcome = new(notifications.Accepted)
		return wire, nil
	case NotificationFailed:
		switch evidence := state.Evidence.(type) {
		case NotificationAdmissionRejected:
			wire.ReasonCode = evidence.ReasonCode
		case NotificationAttemptFailed:
			outcome := evidence.Outcome
			wire.Outcome = &outcome
			wire.ReasonCode = evidence.ReasonCode
		default:
			return wire, invalid("invalid Notification failure evidence")
		}
		return wire, nil
	case NotificationInterrupted:
		wire.ReasonCode = state.ReasonCode
		return wire, nil
	default:
		return wire, invalid("unsupported Notification execution state %T", details.State)
	}
}

func decodeNotificationExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	var wire notificationWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	return notificationFromWire(env, wire)
}

func notificationFromWire(env StepExecutionEnvelope, wire notificationWire) (NotificationExecutionDetails, error) {
	details := NotificationExecutionDetails{StartedAt: env.StartedAt, AttemptID: wire.AttemptID}
	if wire.Inputs != nil {
		if wire.Inputs.Title == nil || wire.Inputs.Message == nil {
			return details, invalid("Notification inputs require title and message")
		}
		details.Message = &notifications.Message{Title: *wire.Inputs.Title, Text: *wire.Inputs.Message}
	}
	switch env.Phase {
	case executionRunning:
		if env.CompletedAt != nil || wire.Outcome != nil || wire.ReasonCode != "" {
			return details, invalid("invalid running Notification")
		}
		details.State = NotificationRunning{}
	case executionSucceeded:
		if env.CompletedAt == nil || wire.Outcome == nil || *wire.Outcome != notifications.Accepted ||
			wire.ReasonCode != "" {
			return details, invalid("invalid successful Notification")
		}
		details.State = NotificationSucceeded{CompletedAt: *env.CompletedAt}
	case executionFailed:
		failed, err := failedNotificationFromWire(env, wire)
		if err != nil {
			return details, err
		}
		details.State = failed
	case executionInterrupted:
		if env.CompletedAt == nil || wire.Outcome != nil || wire.ReasonCode == "" {
			return details, invalid("invalid interrupted Notification")
		}
		details.State = NotificationInterrupted{CompletedAt: *env.CompletedAt, ReasonCode: wire.ReasonCode}
	default:
		return details, invalid("invalid Notification phase")
	}
	return details, nil
}

func failedNotificationFromWire(env StepExecutionEnvelope, wire notificationWire) (NotificationFailed, error) {
	if env.CompletedAt == nil {
		return NotificationFailed{}, invalid("failed Notification lacks completion time")
	}
	state := NotificationFailed{CompletedAt: *env.CompletedAt}
	if wire.Outcome == nil {
		state.Evidence = NotificationAdmissionRejected{ReasonCode: wire.ReasonCode}
	} else {
		state.Evidence = NotificationAttemptFailed{Outcome: *wire.Outcome, ReasonCode: wire.ReasonCode}
	}
	return state, nil
}
