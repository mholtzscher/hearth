package automations

import (
	"bytes"
	"encoding/json"
	"io"
	"time"
	"unicode/utf8"
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

// DecodeStepExecutionDetails validates retained evidence and returns its canonical
// envelope for accounting without another validation/encoding pass.
//
//nolint:gocognit // The strict wire boundary distinguishes pre-reservation failures before dispatching the closed kind family.
func DecodeStepExecutionDetails(
	stepID StepID,
	env StepExecutionEnvelope,
) (StepExecutionDetails, StepExecutionEnvelope, error) {
	if _, err := ParseStepID(string(stepID)); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if !isValidExecutionTimestamp(env.StartedAt) ||
		env.CompletedAt != nil && !isValidExecutionTimestamp(*env.CompletedAt) {
		return nil, StepExecutionEnvelope{}, invalid("invalid execution envelope timestamps")
	}
	fields, err := decodeEvidenceObject(env.EvidenceJSON)
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if err = validateExecutionInputEncoding(env.Kind, fields["inputs"]); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	var details StepExecutionDetails
	if isPreReservationValueFailure(env.Kind, fields) {
		if len(fields) != 1 || env.Phase != executionFailed || env.CompletedAt == nil ||
			!env.CompletedAt.Equal(env.StartedAt) {
			return nil, StepExecutionEnvelope{}, invalid("invalid pre-reservation value failure")
		}
		var failure ValueFailure
		if err = decodeWire(fields["value_failure"], &failure); err != nil {
			return nil, StepExecutionEnvelope{}, err
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
			details, err = decodeBranchExecution(stepID, env, fields)
		case StepKindSetVariables, StepKindReadState:
			details, err = decodeDataExecution(env)
		default:
			return nil, StepExecutionEnvelope{}, invalid("unsupported execution kind %q", env.Kind)
		}
	}
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	canonical, err := EncodeStepExecutionDetails(details)
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if canonical.Kind != env.Kind || canonical.Phase != env.Phase {
		return nil, StepExecutionEnvelope{}, invalid("execution phase or kind disagrees with typed evidence")
	}
	completed := canonical.CompletedAt
	if (completed == nil) != (env.CompletedAt == nil) || completed != nil && !completed.Equal(*env.CompletedAt) {
		return nil, StepExecutionEnvelope{}, invalid("execution completion disagrees with typed evidence")
	}
	return details, canonical, nil
}

// Check original fragments before typed decoding can replace invalid UTF-8 or
// discard whitespace/escaping that counts toward each input's retained bound.
func validateExecutionInputEncoding(kind StepKind, raw json.RawMessage) error {
	if raw == nil {
		return nil // Legacy absence; ownership checks require current inputs.
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid("inputs must be an object")
	}
	// encoding/json binds struct fields case-insensitively; the input contract
	// requires these exact field names, including at the independent read boundary.
	valid := false
	switch kind {
	case StepKindCommand:
		valid = len(fields) == 1 && fields["parameters"] != nil
	case StepKindFunction:
		valid = len(fields) == 1 && fields["arguments"] != nil
	case StepKindNotification:
		valid = len(fields) == 2 && fields["title"] != nil && fields["message"] != nil
	case StepKindIf, StepKindChoose, StepKindDelay, StepKindSetVariables, StepKindReadState:
	}
	if !valid {
		return invalid("inputs disagree with execution kind")
	}
	for _, fragment := range fields {
		if len(fragment) > automationValueMaxBytes || !utf8.Valid(fragment) {
			return invalid("invalid input fragment")
		}
	}
	return nil
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

func decodeEvidenceObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	d := json.NewDecoder(bytes.NewReader(raw))
	if token, err := d.Token(); err != nil || token != json.Delim('{') {
		return nil, invalid("evidence must be one JSON object")
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, err := d.Token()
		if err != nil {
			return nil, invalid("invalid evidence field")
		}
		key, ok := token.(string)
		if !ok || fields[key] != nil {
			return nil, invalid("duplicate evidence field")
		}
		var value json.RawMessage
		if err = d.Decode(&value); err != nil {
			return nil, invalid("invalid evidence value")
		}
		fields[key] = value
	}
	if _, err := d.Token(); err != nil {
		return nil, invalid("invalid evidence object")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, invalid("trailing evidence JSON")
	}
	return fields, nil
}
func decodeWire(raw json.RawMessage, target any) error {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(target); err != nil {
		return invalid("invalid typed execution payload")
	}
	return nil
}
