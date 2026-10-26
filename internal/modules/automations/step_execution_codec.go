package automations

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// StepExecutionEnvelope contains indexed lifecycle columns and the typed payload.
type StepExecutionEnvelope struct {
	Kind         StepKind
	Phase        string
	StartedAt    time.Time
	CompletedAt  *time.Time
	EvidenceJSON json.RawMessage
}

type commandWire struct {
	CommandID         *devices.CommandID     `json:"command_id,omitempty"`
	CorrelationID     *devices.CorrelationID `json:"correlation_id,omitempty"`
	VerifiedCommandID *devices.CommandID     `json:"verified_command_id,omitempty"`
	CommandOutcome    string                 `json:"command_outcome,omitempty"`
	ReasonCode        string                 `json:"reason_code,omitempty"`
}
type delayWire struct {
	ReasonCode string `json:"reason_code,omitempty"`
}
type notificationWire struct {
	AttemptID  notifications.AttemptID `json:"attempt_id"`
	Outcome    *notifications.Outcome  `json:"outcome,omitempty"`
	ReasonCode string                  `json:"reason_code,omitempty"`
}
type functionWire struct {
	InvocationID      adapters.InvocationID `json:"invocation_id"`
	CatalogRevision   adapters.CatalogID    `json:"catalog_revision,omitempty"`
	InvocationOutcome string                `json:"invocation_outcome,omitempty"`
	ReasonCode        string                `json:"reason_code,omitempty"`
}

const branchEvaluatedAtField = "evaluated_at"
const branchKindField = "kind"
const branchPositionField = "position"
const branchStepIDField = "step_id"

func EncodeStepExecutionBody(body StepExecutionBody) (StepExecutionEnvelope, error) {
	var env StepExecutionEnvelope
	var payload any
	switch b := body.(type) {
	case CommandExecution:
		var err error
		env, payload, err = encodeCommandExecution(b)
		if err != nil {
			return env, err
		}
	case DelayExecutionBody:
		var err error
		env, payload, err = encodeDelayExecution(b)
		if err != nil {
			return env, err
		}
	case NotificationExecutionBody:
		var err error
		env, payload, err = encodeNotificationExecution(b)
		if err != nil {
			return env, err
		}
	case FunctionExecutionBody:
		var err error
		env, payload, err = encodeFunctionExecution(b)
		if err != nil {
			return env, err
		}
	case BranchExecutionBody:
		var err error
		env, payload, err = encodeBranchExecution(b)
		if err != nil {
			return env, err
		}
	default:
		return env, invalid("unsupported execution body %T", body)
	}
	if env.Kind == "" || !validDelayTimestamp(env.StartedAt) {
		return env, invalid("invalid execution start or kind")
	}
	if env.CompletedAt != nil && !validDelayTimestamp(*env.CompletedAt) {
		return env, invalid("invalid execution completion time")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return env, invalid("marshal execution evidence")
	}
	env.EvidenceJSON = raw
	return env, nil
}

func encodeCommandExecution(body CommandExecution) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindCommand, StartedAt: body.StartedAt, CompletedAt: body.CompletedAt}
	wire, err := commandToWire(body)
	if err != nil {
		return env, nil, err
	}
	env.Phase = commandPhase(body)
	return env, wire, nil
}

func encodeDelayExecution(body DelayExecutionBody) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindDelay, StartedAt: body.StartedAt, CompletedAt: body.CompletedAt}
	if body.FailureCode != nil && (body.CompletedAt == nil || !interruptionCode(*body.FailureCode)) {
		return env, nil, invalid("invalid Delay interruption evidence")
	}
	wire := delayWire{}
	if body.FailureCode != nil {
		wire.ReasonCode = *body.FailureCode
	}
	switch {
	case body.CompletedAt == nil:
		env.Phase = executionRunning
	case body.FailureCode == nil:
		env.Phase = executionSucceeded
	default:
		env.Phase = executionInterrupted
	}
	return env, wire, nil
}

func encodeNotificationExecution(body NotificationExecutionBody) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindNotification, StartedAt: body.StartedAt, CompletedAt: body.CompletedAt}
	wire, phase, err := notificationToWire(body)
	env.Phase = phase
	return env, wire, err
}

func encodeFunctionExecution(body FunctionExecutionBody) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindFunction, StartedAt: body.StartedAt, CompletedAt: body.CompletedAt}
	wire, phase, err := functionToWire(body)
	env.Phase = phase
	return env, wire, err
}

func encodeBranchExecution(body BranchExecutionBody) (StepExecutionEnvelope, any, error) {
	decision := body.Decision
	if err := validateBranchDecisionShape(decision); err != nil {
		return StepExecutionEnvelope{}, nil, err
	}
	env := StepExecutionEnvelope{Kind: decision.Kind(), StartedAt: decision.EvaluatedAt}
	at := decision.EvaluatedAt
	env.CompletedAt = &at
	env.Phase = executionSucceeded
	if decision.FailureCode() != nil {
		env.Phase = executionFailed
	}
	raw, err := EncodeBranchDecision(decision)
	if err != nil {
		return env, nil, err
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		return env, nil, invalid("encode branch evidence")
	}
	for _, key := range []string{branchPositionField, branchStepIDField, branchKindField, branchEvaluatedAtField} {
		delete(fields, key)
	}
	return env, fields, nil
}

func DecodeStepExecutionBody(stepID StepID, env StepExecutionEnvelope) (StepExecutionBody, error) {
	if _, err := ParseStepID(string(stepID)); err != nil {
		return nil, err
	}
	if !validDelayTimestamp(env.StartedAt) || env.CompletedAt != nil && !validDelayTimestamp(*env.CompletedAt) {
		return nil, invalid("invalid execution envelope timestamps")
	}
	if err := strictSingleObject(env.EvidenceJSON); err != nil {
		return nil, err
	}
	var body StepExecutionBody
	switch env.Kind {
	case StepKindCommand:
		var err error
		body, err = decodeCommandExecution(env)
		if err != nil {
			return nil, err
		}
	case StepKindDelay:
		var err error
		body, err = decodeDelayExecution(env)
		if err != nil {
			return nil, err
		}
	case StepKindNotification:
		var err error
		body, err = decodeNotificationExecution(env)
		if err != nil {
			return nil, err
		}
	case StepKindFunction:
		var err error
		body, err = decodeFunctionExecution(env)
		if err != nil {
			return nil, err
		}
	case StepKindIf, StepKindChoose:
		var err error
		body, err = decodeBranchExecution(stepID, env)
		if err != nil {
			return nil, err
		}
	default:
		return nil, invalid("unsupported execution kind %q", env.Kind)
	}
	canonical, err := EncodeStepExecutionBody(body)
	if err != nil {
		return nil, err
	}
	if canonical.Kind != env.Kind || canonical.Phase != env.Phase {
		return nil, invalid("execution phase or kind disagrees with typed evidence")
	}
	return body, nil
}

func decodeCommandExecution(env StepExecutionEnvelope) (StepExecutionBody, error) {
	var wire commandWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	return commandFromWire(env, wire)
}

func decodeDelayExecution(env StepExecutionEnvelope) (StepExecutionBody, error) {
	var wire delayWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	body := DelayExecutionBody{StartedAt: env.StartedAt, CompletedAt: env.CompletedAt}
	if wire.ReasonCode != "" {
		body.FailureCode = &wire.ReasonCode
	}
	if body.CompletedAt == nil && wire.ReasonCode != "" ||
		body.CompletedAt != nil && env.Phase == executionSucceeded && wire.ReasonCode != "" ||
		body.CompletedAt != nil && env.Phase == executionInterrupted && wire.ReasonCode == "" {
		return nil, invalid("invalid Delay phase or evidence")
	}
	if body.CompletedAt != nil && env.Phase != executionSucceeded && env.Phase != executionInterrupted {
		return nil, invalid("invalid Delay phase")
	}
	return body, nil
}

func decodeNotificationExecution(env StepExecutionEnvelope) (StepExecutionBody, error) {
	var wire notificationWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	return notificationFromWire(env, wire)
}

func decodeFunctionExecution(env StepExecutionEnvelope) (StepExecutionBody, error) {
	var wire functionWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	return functionFromWire(env, wire)
}

func decodeBranchExecution(id StepID, env StepExecutionEnvelope) (StepExecutionBody, error) {
	if env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("branch must be instantaneous")
	}
	decision, err := decodeBranchPayload(id, env)
	if err != nil {
		return nil, err
	}
	return BranchExecutionBody{Decision: decision}, nil
}

func strictSingleObject(raw json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var obj map[string]json.RawMessage
	if err := d.Decode(&obj); err != nil || obj == nil {
		return invalid("evidence must be one JSON object")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return invalid("trailing evidence JSON")
	}
	return nil
}
func decodeWire(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return invalid("invalid typed execution payload")
	}
	return nil
}

func commandPhase(b CommandExecution) string {
	if b.CompletedAt == nil {
		return executionRunning
	}
	switch b.Outcome.(type) {
	case SatisfiedStep, DispatchedStep:
		return executionSucceeded
	case FailedStep:
		return executionFailed
	case InterruptedStep:
		return executionInterrupted
	}
	return ""
}
func commandToWire(b CommandExecution) (commandWire, error) {
	w, err := commandReservationToWire(b.Reservation)
	if err != nil {
		return w, err
	}
	if b.CompletedAt == nil {
		if b.Reservation == nil || b.Outcome != nil {
			return w, invalid("running Command requires reservation and no outcome")
		}
		return w, nil
	}
	if !validDelayTimestamp(b.StartedAt) || !validDelayTimestamp(*b.CompletedAt) {
		return w, invalid("invalid Command timestamps")
	}
	if err = ValidateStepCompletion(StepCompletion{Outcome: b.Outcome}); err != nil {
		return w, err
	}
	return commandOutcomeToWire(b, w)
}

func commandReservationToWire(reservation *CommandReservation) (commandWire, error) {
	w := commandWire{}
	if reservation == nil {
		return w, nil
	}
	if _, err := devices.ParseCommandID(string(reservation.CommandID)); err != nil {
		return w, err
	}
	if _, err := devices.ParseCorrelationID(string(reservation.CorrelationID)); err != nil {
		return w, err
	}
	w.CommandID = &reservation.CommandID
	w.CorrelationID = &reservation.CorrelationID
	return w, nil
}

func commandOutcomeToWire(b CommandExecution, w commandWire) (commandWire, error) {
	switch o := b.Outcome.(type) {
	case SatisfiedStep:
		return satisfiedCommandToWire(b, w, o)
	case DispatchedStep:
		return dispatchedCommandToWire(b, w, o)
	case FailedStep:
		return failedCommandToWire(b, w, o)
	case InterruptedStep:
		return interruptedCommandToWire(b, w, o)
	default:
		return w, invalid("invalid Command outcome %T", b.Outcome)
	}
}

func satisfiedCommandToWire(b CommandExecution, w commandWire, outcome SatisfiedStep) (commandWire, error) {
	if b.Reservation == nil || outcome.VerifiedCommandID != b.Reservation.CommandID {
		return w, invalid("satisfied Command reservation mismatch")
	}
	w.VerifiedCommandID = &outcome.VerifiedCommandID
	w.CommandOutcome = "satisfied"
	return w, nil
}

func dispatchedCommandToWire(b CommandExecution, w commandWire, outcome DispatchedStep) (commandWire, error) {
	if b.Reservation == nil || outcome.VerifiedCommandID != b.Reservation.CommandID {
		return w, invalid("dispatched Command reservation mismatch")
	}
	w.VerifiedCommandID = &outcome.VerifiedCommandID
	w.CommandOutcome = "dispatched"
	return w, nil
}

func failedCommandToWire(b CommandExecution, w commandWire, outcome FailedStep) (commandWire, error) {
	if b.Reservation == nil || outcome.FailureCode == "" {
		return w, invalid("failed Command requires reservation and reason")
	}
	w.ReasonCode = outcome.FailureCode
	if outcome.VerifiedCommandID == nil {
		return w, nil
	}
	if *outcome.VerifiedCommandID != b.Reservation.CommandID {
		return w, invalid("failed Command reservation mismatch")
	}
	w.VerifiedCommandID = outcome.VerifiedCommandID
	return w, nil
}

func interruptedCommandToWire(b CommandExecution, w commandWire, outcome InterruptedStep) (commandWire, error) {
	if outcome.FailureCode == "" {
		return w, invalid("interrupted Command requires reason")
	}
	if b.Reservation == nil && (outcome.VerifiedCommandID != nil || !interruptionCode(outcome.FailureCode)) {
		return w, invalid("invalid unreserved Command interruption")
	}
	if outcome.VerifiedCommandID != nil &&
		(b.Reservation == nil || *outcome.VerifiedCommandID != b.Reservation.CommandID) {
		return w, invalid("interrupted Command reservation mismatch")
	}
	w.ReasonCode = outcome.FailureCode
	w.VerifiedCommandID = outcome.VerifiedCommandID
	return w, nil
}

func commandFromWire(env StepExecutionEnvelope, w commandWire) (CommandExecution, error) {
	b := CommandExecution{StartedAt: env.StartedAt, CompletedAt: env.CompletedAt}
	reservation, hasReservation, err := commandReservationFromWire(w)
	if err != nil {
		return b, err
	}
	if hasReservation {
		b.Reservation = &reservation
	}
	if env.CompletedAt == nil {
		return commandFromRunningWire(env, w, b)
	}
	if err = validateVerifiedCommand(w, b.Reservation); err != nil {
		return b, err
	}
	return commandFromTerminalWire(env, w, b)
}

func commandReservationFromWire(w commandWire) (CommandReservation, bool, error) {
	if (w.CommandID == nil) != (w.CorrelationID == nil) {
		return CommandReservation{}, false, invalid("partial Command reservation")
	}
	if w.CommandID == nil {
		return CommandReservation{}, false, nil
	}
	if _, err := devices.ParseCommandID(string(*w.CommandID)); err != nil {
		return CommandReservation{}, false, err
	}
	if _, err := devices.ParseCorrelationID(string(*w.CorrelationID)); err != nil {
		return CommandReservation{}, false, err
	}
	return CommandReservation{CommandID: *w.CommandID, CorrelationID: *w.CorrelationID}, true, nil
}

func commandFromRunningWire(env StepExecutionEnvelope, w commandWire, b CommandExecution) (CommandExecution, error) {
	if env.Phase != executionRunning || b.Reservation == nil || w.VerifiedCommandID != nil ||
		w.CommandOutcome != "" || w.ReasonCode != "" {
		return b, invalid("invalid running Command shape")
	}
	return b, nil
}

func validateVerifiedCommand(w commandWire, reservation *CommandReservation) error {
	if w.VerifiedCommandID == nil {
		return nil
	}
	if _, err := devices.ParseCommandID(string(*w.VerifiedCommandID)); err != nil {
		return err
	}
	if reservation == nil || *w.VerifiedCommandID != reservation.CommandID {
		return invalid("verified Command does not match reservation")
	}
	return nil
}

func commandFromTerminalWire(env StepExecutionEnvelope, w commandWire, b CommandExecution) (CommandExecution, error) {
	switch env.Phase {
	case executionSucceeded:
		return successfulCommandFromWire(w, b)
	case executionFailed:
		if b.Reservation == nil || w.ReasonCode == "" || w.CommandOutcome != "" {
			return b, invalid("invalid failed Command shape")
		}
		b.Outcome = FailedStep{FailureCode: w.ReasonCode, VerifiedCommandID: w.VerifiedCommandID}
	case executionInterrupted:
		if w.ReasonCode == "" || w.CommandOutcome != "" {
			return b, invalid("invalid interrupted Command shape")
		}
		b.Outcome = InterruptedStep{FailureCode: w.ReasonCode, VerifiedCommandID: w.VerifiedCommandID}
	default:
		return b, invalid("invalid Command phase")
	}
	return b, nil
}

func successfulCommandFromWire(w commandWire, b CommandExecution) (CommandExecution, error) {
	if w.ReasonCode != "" || w.VerifiedCommandID == nil {
		return b, invalid("successful Command requires verified link")
	}
	switch w.CommandOutcome {
	case "satisfied":
		b.Outcome = SatisfiedStep{VerifiedCommandID: *w.VerifiedCommandID}
	case "dispatched":
		b.Outcome = DispatchedStep{VerifiedCommandID: *w.VerifiedCommandID}
	default:
		return b, invalid("invalid successful Command outcome")
	}
	return b, nil
}

func notificationToWire(b NotificationExecutionBody) (notificationWire, string, error) {
	w := notificationWire{AttemptID: b.AttemptID}
	phase := executionRunning
	if notifications.ValidateID(string(b.AttemptID), "ntf_") != nil {
		return w, "", invalid("invalid Notification Attempt identity")
	}
	if b.CompletedAt == nil {
		if b.Outcome != nil || b.ReasonCode != nil {
			return w, "", invalid("running Notification has terminal evidence")
		}
		return w, phase, nil
	}
	if b.Outcome != nil {
		w.Outcome = b.Outcome
	}
	if b.ReasonCode != nil {
		w.ReasonCode = *b.ReasonCode
	}
	switch {
	case b.Outcome == nil && b.ReasonCode != nil && interruptionCode(*b.ReasonCode):
		phase = executionInterrupted
	case b.Outcome != nil && *b.Outcome == notifications.Accepted:
		phase = executionSucceeded
	default:
		phase = executionFailed
	}
	if err := validateNotificationTerminalEvidence(
		NotificationCompletion{Status: notificationStatus(phase), Outcome: b.Outcome, ReasonCode: b.ReasonCode},
	); err != nil {
		return w, "", err
	}
	return w, phase, nil
}
func notificationStatus(p string) NotificationStatus {
	switch p {
	case executionSucceeded:
		return NotificationAccepted
	case executionInterrupted:
		return NotificationInterrupted
	default:
		return NotificationFailed
	}
}
func notificationFromWire(env StepExecutionEnvelope, w notificationWire) (NotificationExecutionBody, error) {
	b := NotificationExecutionBody{StartedAt: env.StartedAt, CompletedAt: env.CompletedAt, AttemptID: w.AttemptID}
	if w.Outcome != nil {
		b.Outcome = w.Outcome
	}
	if w.ReasonCode != "" {
		b.ReasonCode = &w.ReasonCode
	}
	if notifications.ValidateID(string(w.AttemptID), "ntf_") != nil {
		return b, invalid("invalid Notification Attempt identity")
	}
	if env.CompletedAt == nil {
		if env.Phase != executionRunning || w.Outcome != nil || w.ReasonCode != "" {
			return b, invalid("invalid running Notification")
		}
		return b, nil
	}
	if err := validateNotificationTerminalEvidence(
		NotificationCompletion{Status: notificationStatus(env.Phase), Outcome: w.Outcome, ReasonCode: b.ReasonCode},
	); err != nil {
		return b, err
	}
	return b, nil
}

func functionToWire(b FunctionExecutionBody) (functionWire, string, error) {
	w := functionWire{InvocationID: b.InvocationID}
	phase, w, err := functionStateToWire(b, w)
	if err != nil {
		return w, "", err
	}
	if _, err = adapters.ParseInvocationID(string(b.InvocationID)); err != nil {
		return w, "", err
	}
	if b.CompletedAt != nil {
		if err = validateFunctionTerminalState(b.State); err != nil {
			return w, "", err
		}
	}
	return w, phase, nil
}

func functionStateToWire(b FunctionExecutionBody, w functionWire) (string, functionWire, error) {
	switch s := b.State.(type) {
	case FunctionRunning:
		if b.CompletedAt != nil {
			return "", w, invalid("running Function has completion time")
		}
		return executionRunning, w, nil
	case FunctionSucceeded:
		return succeededFunctionToWire(b, w, s)
	case FunctionFailed:
		return failedFunctionToWire(b, w, s)
	case FunctionInterrupted:
		return interruptedFunctionToWire(b, w, s)
	default:
		return "", w, invalid("invalid Function state")
	}
}

func succeededFunctionToWire(
	b FunctionExecutionBody,
	w functionWire,
	state FunctionSucceeded,
) (string, functionWire, error) {
	if b.CompletedAt == nil || !b.CompletedAt.Equal(state.CompletedAt) {
		return "", w, invalid("Function completion time mismatch")
	}
	w.CatalogRevision = state.CatalogRevision
	w.InvocationOutcome = executionSucceeded
	return executionSucceeded, w, nil
}

func failedFunctionToWire(b FunctionExecutionBody, w functionWire, state FunctionFailed) (string, functionWire, error) {
	if b.CompletedAt == nil || !b.CompletedAt.Equal(state.CompletedAt) {
		return "", w, invalid("Function completion time mismatch")
	}
	switch evidence := state.Evidence.(type) {
	case FunctionAdmissionRejected:
		w.ReasonCode = evidence.ReasonCode
	case FunctionInvocationFailed:
		w.CatalogRevision = evidence.CatalogRevision
		var err error
		w, err = functionFailureOutcomeToWire(w, evidence.Outcome)
		if err != nil {
			return "", w, err
		}
	default:
		return "", w, invalid("invalid Function failure")
	}
	return executionFailed, w, nil
}

func functionFailureOutcomeToWire(w functionWire, evidence adapters.Evidence) (functionWire, error) {
	switch outcome := evidence.(type) {
	case adapters.Succeeded:
		return w, invalid("invalid failed Function outcome")
	case adapters.Failed:
		w.InvocationOutcome = executionFailed
		w.ReasonCode = outcome.ReasonCode
	case adapters.NotExecuted:
		w.InvocationOutcome = functionOutcomeNotExecuted
		w.ReasonCode = outcome.ReasonCode
	case adapters.Unknown:
		w.InvocationOutcome = string(BranchUnknown)
		w.ReasonCode = outcome.ReasonCode
	default:
		return w, invalid("invalid failed Function outcome")
	}
	return w, nil
}

func interruptedFunctionToWire(
	b FunctionExecutionBody,
	w functionWire,
	state FunctionInterrupted,
) (string, functionWire, error) {
	if b.CompletedAt == nil || !b.CompletedAt.Equal(state.CompletedAt) {
		return "", w, invalid("Function completion time mismatch")
	}
	w.ReasonCode = state.ReasonCode
	return executionInterrupted, w, nil
}

func functionFromWire(env StepExecutionEnvelope, w functionWire) (FunctionExecutionBody, error) {
	b := FunctionExecutionBody{StartedAt: env.StartedAt, CompletedAt: env.CompletedAt, InvocationID: w.InvocationID}
	switch env.Phase {
	case executionRunning:
		b.State = FunctionRunning{}
		if err := validateRunningFunctionWire(env, w); err != nil {
			return b, err
		}
	case executionSucceeded:
		state, err := succeededFunctionFromWire(env, w)
		if err != nil {
			return b, err
		}
		b.State = state
	case executionFailed:
		state, err := failedFunctionFromWire(env, w)
		if err != nil {
			return b, err
		}
		b.State = state
	case executionInterrupted:
		state, err := interruptedFunctionFromWire(env, w)
		if err != nil {
			return b, err
		}
		b.State = state
	default:
		return b, invalid("invalid Function phase")
	}
	if _, err := adapters.ParseInvocationID(string(w.InvocationID)); err != nil {
		return b, err
	}
	if env.CompletedAt != nil {
		if err := validateFunctionTerminalState(b.State); err != nil {
			return b, err
		}
	}
	return b, nil
}

func validateRunningFunctionWire(env StepExecutionEnvelope, w functionWire) error {
	if env.CompletedAt != nil || w.CatalogRevision != "" || w.InvocationOutcome != "" || w.ReasonCode != "" {
		return invalid("invalid running Function")
	}
	return nil
}

func succeededFunctionFromWire(env StepExecutionEnvelope, w functionWire) (FunctionExecutionState, error) {
	if env.CompletedAt == nil || w.CatalogRevision == "" || w.InvocationOutcome != executionSucceeded ||
		w.ReasonCode != "" {
		return nil, invalid("invalid successful Function")
	}
	return FunctionSucceeded{CompletedAt: *env.CompletedAt, CatalogRevision: w.CatalogRevision}, nil
}

func failedFunctionFromWire(env StepExecutionEnvelope, w functionWire) (FunctionExecutionState, error) {
	if env.CompletedAt == nil || w.ReasonCode == "" {
		return nil, invalid("invalid failed Function")
	}
	if w.InvocationOutcome == "" {
		if w.CatalogRevision != "" {
			return nil, invalid("admission rejection has catalog")
		}
		return FunctionFailed{
			CompletedAt: *env.CompletedAt,
			Evidence:    FunctionAdmissionRejected{ReasonCode: w.ReasonCode},
		}, nil
	}
	outcome, err := functionFailureOutcomeFromWire(w)
	if err != nil {
		return nil, err
	}
	return FunctionFailed{
		CompletedAt: *env.CompletedAt,
		Evidence:    FunctionInvocationFailed{CatalogRevision: w.CatalogRevision, Outcome: outcome},
	}, nil
}

func functionFailureOutcomeFromWire(w functionWire) (adapters.Evidence, error) {
	switch w.InvocationOutcome {
	case executionFailed:
		return adapters.Failed{ReasonCode: w.ReasonCode}, nil
	case functionOutcomeNotExecuted:
		return adapters.NotExecuted{ReasonCode: w.ReasonCode}, nil
	case string(BranchUnknown):
		return adapters.Unknown{ReasonCode: w.ReasonCode}, nil
	default:
		return nil, invalid("invalid invocation outcome")
	}
}

func interruptedFunctionFromWire(env StepExecutionEnvelope, w functionWire) (FunctionExecutionState, error) {
	if env.CompletedAt == nil || w.ReasonCode == "" || w.CatalogRevision != "" || w.InvocationOutcome != "" {
		return nil, invalid("invalid interrupted Function")
	}
	return FunctionInterrupted{CompletedAt: *env.CompletedAt, ReasonCode: w.ReasonCode}, nil
}

func decodeBranchPayload(id StepID, env StepExecutionEnvelope) (BranchDecision, error) {
	var fields map[string]json.RawMessage
	if err := decodeWire(env.EvidenceJSON, &fields); err != nil {
		return BranchDecision{}, err
	}
	for _, forbidden := range []string{branchStepIDField, branchPositionField, branchKindField, branchEvaluatedAtField} {
		if _, ok := fields[forbidden]; ok {
			return BranchDecision{}, invalid("branch payload duplicates envelope field")
		}
	}
	for k := range fields {
		if k != "outcome" && k != "evaluations" && k != "selected_branch_id" && k != "failure_code" {
			return BranchDecision{}, invalid("unknown branch evidence field")
		}
	}
	kind, _ := json.Marshal(env.Kind)
	sid, _ := json.Marshal(id)
	at, _ := json.Marshal(env.StartedAt)
	phaseOutcome := ""
	if raw, ok := fields["outcome"]; ok {
		if err := json.Unmarshal(raw, &phaseOutcome); err != nil {
			return BranchDecision{}, invalid("invalid branch outcome")
		}
	} else {
		return BranchDecision{}, invalid("branch evidence requires outcome")
	}
	if env.Phase == executionFailed {
		if phaseOutcome != string(BranchUnknown) && phaseOutcome != string(BranchError) {
			return BranchDecision{}, invalid("branch phase contradicts outcome")
		}
	} else if env.Phase != executionSucceeded || phaseOutcome == string(BranchUnknown) || phaseOutcome == string(BranchError) {
		return BranchDecision{}, invalid("branch phase contradicts outcome")
	}
	outcome, _ := json.Marshal(phaseOutcome)
	position := json.RawMessage("0")
	full := map[string]json.RawMessage{
		branchStepIDField:      sid,
		branchPositionField:    position,
		branchKindField:        kind,
		branchEvaluatedAtField: at,
		"outcome":              outcome,
	}
	maps.Copy(full, fields)
	raw, err := json.Marshal(full)
	if err != nil {
		return BranchDecision{}, err
	}
	return DecodeBranchDecision(raw)
}
