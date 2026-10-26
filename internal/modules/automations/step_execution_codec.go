package automations

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
)

// StepExecutionEnvelope contains indexed lifecycle columns and the typed payload.
type StepExecutionEnvelope struct {
	Kind         StepKind
	Phase        string
	StartedAt    time.Time
	CompletedAt  *time.Time
	EvidenceJSON json.RawMessage
}

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
