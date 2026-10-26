package automations

import "github.com/mholtzscher/hearth/internal/modules/notifications"

type notificationWire struct {
	AttemptID  notifications.AttemptID `json:"attempt_id"`
	Outcome    *notifications.Outcome  `json:"outcome,omitempty"`
	ReasonCode string                  `json:"reason_code,omitempty"`
}

func encodeNotificationExecution(details NotificationExecutionDetails) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindNotification, StartedAt: details.StartedAt}
	completedAt, err := NotificationStateCompletion(details.State)
	if err != nil {
		return env, nil, err
	}
	env.CompletedAt = completedAt
	wire, phase, err := notificationToWire(details)
	env.Phase = phase
	return env, wire, err
}

func notificationToWire(details NotificationExecutionDetails) (notificationWire, string, error) {
	wire := notificationWire{AttemptID: details.AttemptID}
	if notifications.ValidateID(string(details.AttemptID), "ntf_") != nil ||
		!isValidExecutionTimestamp(details.StartedAt) {
		return wire, "", invalid("invalid Notification identity or start time")
	}
	if err := ValidateNotificationExecutionState(details.State); err != nil {
		return wire, "", err
	}
	switch state := details.State.(type) {
	case NotificationRunning:
		return wire, executionRunning, nil
	case NotificationSucceeded:
		wire.Outcome = new(notifications.Accepted)
		return wire, executionSucceeded, nil
	case NotificationFailed:
		switch evidence := state.Evidence.(type) {
		case NotificationAdmissionRejected:
			wire.ReasonCode = evidence.ReasonCode
		case NotificationAttemptFailed:
			outcome := evidence.Outcome
			wire.Outcome = &outcome
			wire.ReasonCode = evidence.ReasonCode
		default:
			return wire, "", invalid("invalid Notification failure evidence")
		}
		return wire, executionFailed, nil
	case NotificationInterrupted:
		wire.ReasonCode = state.ReasonCode
		return wire, executionInterrupted, nil
	default:
		return wire, "", invalid("unsupported Notification execution state %T", details.State)
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
	if notifications.ValidateID(string(wire.AttemptID), "ntf_") != nil || !isValidExecutionTimestamp(env.StartedAt) {
		return details, invalid("invalid Notification identity or start time")
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
	if err := ValidateNotificationExecutionState(details.State); err != nil {
		return details, err
	}
	completedAt, err := NotificationStateCompletion(details.State)
	if err != nil || (completedAt == nil) != (env.CompletedAt == nil) ||
		completedAt != nil && !completedAt.Equal(*env.CompletedAt) {
		return details, invalid("Notification state completion disagrees with envelope")
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
