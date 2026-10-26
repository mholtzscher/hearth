package automations

import (
	"bytes"
	"encoding/json"
	"io"
	"maps"
	"time"

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

const branchEvaluatedAtField = "evaluated_at"
const branchKindField = "kind"
const branchPositionField = "position"
const branchStepIDField = "step_id"

func EncodeStepExecutionDetails(details StepExecutionDetails) (StepExecutionEnvelope, error) {
	var env StepExecutionEnvelope
	var payload any
	var err error
	switch details := details.(type) {
	case CommandExecutionDetails:
		env, payload, err = encodeCommandExecution(details)
	case DelayExecutionDetails:
		env, payload, err = encodeDelayExecution(details)
	case NotificationExecutionDetails:
		env, payload, err = encodeNotificationExecution(details)
	case FunctionExecutionDetails:
		env, payload, err = encodeFunctionExecution(details)
	case BranchExecutionDetails:
		env, payload, err = encodeBranchExecution(details)
	default:
		return env, invalid("unsupported execution details %T", details)
	}
	if err != nil {
		return env, err
	}
	if env.Kind == "" || !isValidExecutionTimestamp(env.StartedAt) {
		return env, invalid("invalid execution start or kind")
	}
	if env.CompletedAt != nil && !isValidExecutionTimestamp(*env.CompletedAt) {
		return env, invalid("invalid execution completion time")
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return env, invalid("marshal execution evidence")
	}
	env.EvidenceJSON = raw
	return env, nil
}

// EncodePublicStepExecution encodes one durable reach in the Run history contract.
// The canonical details codec validates the kind-specific evidence before it is exposed.
func EncodePublicStepExecution(execution StepExecution) (json.RawMessage, error) {
	envelope, err := EncodeStepExecutionDetails(execution.Details)
	if err != nil {
		return nil, err
	}
	public := struct {
		StepID      StepID          `json:"step_id"`
		Sequence    int             `json:"sequence"`
		Kind        StepKind        `json:"kind"`
		Phase       string          `json:"phase"`
		StartedAt   time.Time       `json:"started_at"`
		CompletedAt *time.Time      `json:"completed_at,omitempty"`
		Evidence    json.RawMessage `json:"evidence"`
	}{
		StepID: execution.StepID, Sequence: execution.Sequence, Kind: envelope.Kind,
		Phase: envelope.Phase, StartedAt: envelope.StartedAt,
		CompletedAt: envelope.CompletedAt, Evidence: envelope.EvidenceJSON,
	}
	if _, parseErr := ParseStepID(string(execution.StepID)); parseErr != nil {
		return nil, parseErr
	}
	if execution.Sequence < 0 || execution.Sequence >= automationAllStepsMaxCount {
		return nil, invalid("invalid execution sequence")
	}
	return json.Marshal(public)
}

func encodeBranchExecution(details BranchExecutionDetails) (StepExecutionEnvelope, any, error) {
	decision := details.Decision
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

func DecodeStepExecutionDetails(stepID StepID, env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if _, err := ParseStepID(string(stepID)); err != nil {
		return nil, err
	}
	if !isValidExecutionTimestamp(env.StartedAt) ||
		env.CompletedAt != nil && !isValidExecutionTimestamp(*env.CompletedAt) {
		return nil, invalid("invalid execution envelope timestamps")
	}
	if err := strictSingleObject(env.EvidenceJSON); err != nil {
		return nil, err
	}
	var details StepExecutionDetails
	var err error
	switch env.Kind {
	case StepKindCommand:
		details, err = decodeCommandExecution(env)
	case StepKindDelay:
		details, err = decodeDelayExecution(env)
	case StepKindNotification:
		details, err = decodeNotificationExecution(env)
	case StepKindFunction:
		details, err = decodeFunctionExecution(env)
	case StepKindIf, StepKindChoose:
		details, err = decodeBranchExecution(stepID, env)
	default:
		return nil, invalid("unsupported execution kind %q", env.Kind)
	}
	if err != nil {
		return nil, err
	}
	canonical, err := EncodeStepExecutionDetails(details)
	if err != nil {
		return nil, err
	}
	if canonical.Kind != env.Kind || canonical.Phase != env.Phase {
		return nil, invalid("execution phase or kind disagrees with typed evidence")
	}
	return details, nil
}

func decodeBranchExecution(id StepID, env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("branch must be instantaneous")
	}
	decision, err := decodeBranchPayload(id, env)
	if err != nil {
		return nil, err
	}
	return BranchExecutionDetails{Decision: decision}, nil
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
