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
	if err := validateStepExecutionDetails(details); err != nil {
		return StepExecutionEnvelope{}, err
	}
	payload, err := details.encodeValidated()
	if err != nil {
		return StepExecutionEnvelope{}, err
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return StepExecutionEnvelope{}, invalid("marshal execution evidence")
	}
	return StepExecutionEnvelope{
		Kind: details.Kind(), Phase: details.Phase(), StartedAt: details.StartTime(),
		CompletedAt: details.CompletionTime(), EvidenceJSON: raw,
	}, nil
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

//nolint:gocognit // The strict wire boundary distinguishes pre-reservation failures before dispatching the closed kind family.
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
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(env.EvidenceJSON, &fields); err != nil {
		return nil, invalid("invalid evidence")
	}
	if isPreReservationValueFailure(env.Kind, fields) {
		if len(fields) != 1 || env.Phase != executionFailed || env.CompletedAt == nil ||
			!env.CompletedAt.Equal(env.StartedAt) {
			return nil, invalid("invalid pre-reservation value failure")
		}
		var failure ValueFailure
		if err = decodeWire(fields["value_failure"], &failure); err != nil {
			return nil, err
		}
		details = ValueFailureExecutionDetails{StepKind: env.Kind, At: env.StartedAt, Failure: failure}
	} else {
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
		case StepKindSetVariables, StepKindReadState:
			details, err = decodeDataExecution(env)
		default:
			return nil, invalid("unsupported execution kind %q", env.Kind)
		}
	}
	if err != nil {
		return nil, err
	}
	if err = validateStepExecutionDetails(details); err != nil {
		return nil, err
	}
	if details.Kind() != env.Kind || details.Phase() != env.Phase {
		return nil, invalid("execution phase or kind disagrees with typed evidence")
	}
	completed := details.CompletionTime()
	if (completed == nil) != (env.CompletedAt == nil) || completed != nil && !completed.Equal(*env.CompletedAt) {
		return nil, invalid("execution completion disagrees with typed evidence")
	}
	return details, nil
}

func isPreReservationValueFailure(kind StepKind, fields map[string]json.RawMessage) bool {
	if _, failed := fields["value_failure"]; !failed {
		return false
	}
	switch kind {
	case StepKindIf, StepKindChoose:
		return false // Branch errors retain their decision alongside the value failure.
	case StepKindFunction:
		return fields["invocation_id"] == nil // A verified provider success may have a value-flow failure.
	case StepKindCommand, StepKindNotification, StepKindDelay, StepKindSetVariables, StepKindReadState:
		return true
	default:
		return true // The intrinsic kind validator rejects an unknown kind.
	}
}

func strictSingleObject(raw json.RawMessage) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return invalid("evidence must be one JSON object")
	}
	seen := map[string]bool{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return invalid("invalid evidence field")
		}
		key, ok := token.(string)
		if !ok || seen[key] {
			return invalid("duplicate evidence field")
		}
		seen[key] = true
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return invalid("invalid evidence value")
		}
	}
	if _, err := d.Token(); err != nil {
		return invalid("invalid evidence object")
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
