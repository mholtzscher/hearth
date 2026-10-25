//nolint:goconst // Canonical wire discriminators and safe failure allowlists stay literal at the codec boundary.
package automations

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	RunDataRecorded            = "recorded"
	RunDataLegacyUnavailable   = "legacy_unavailable"
	RunDataMaxRecords          = 256
	RunDataMaxBytes            = 2097152
	RunDataMaxRecordBytes      = 131072
	RunDataFailureReserveBytes = 1024
)

type RunDataRecord struct {
	Sequence   int
	StepID     *StepID
	RecordedAt time.Time
	Body       RunDataBody
}

//sumtype:decl
type RunDataBody interface{ isRunDataBody() }
type VariablesInitialized struct{ Values json.RawMessage }
type InputsResolved struct{ Values json.RawMessage }
type VariablesAssigned struct{ Values json.RawMessage }
type StateCaptured struct {
	EntityID       devices.EntityID
	Pointer        string
	ReadAt         time.Time
	Observation    *ObservationEvidence
	UnusableReason *ConditionUnknownReason
	Output         json.RawMessage
}
type FunctionOutputCaptured struct{ Output json.RawMessage }
type RunDataFailed struct {
	Path        string
	FailureCode string
	SourceStart *int
	SourceEnd   *int
	State       *StateReadFailure
}
type StateReadFailure struct {
	EntityID       devices.EntityID
	Pointer        string
	ReadAt         time.Time
	Observation    *ObservationEvidence
	UnusableReason ConditionUnknownReason
}

func (VariablesInitialized) isRunDataBody()   {}
func (InputsResolved) isRunDataBody()         {}
func (VariablesAssigned) isRunDataBody()      {}
func (StateCaptured) isRunDataBody()          {}
func (FunctionOutputCaptured) isRunDataBody() {}
func (RunDataFailed) isRunDataBody()          {}

type dataObservationJSON struct {
	ObservationID devices.ObservationID `json:"observation_id"`
	ObservedAt    time.Time             `json:"observed_at"`
}
type stateReadJSON struct {
	EntityID       devices.EntityID        `json:"entity_id"`
	Pointer        string                  `json:"value_pointer"`
	ReadAt         time.Time               `json:"read_at"`
	Observation    *dataObservationJSON    `json:"observation,omitempty"`
	UnusableReason *ConditionUnknownReason `json:"unusable_reason,omitempty"`
}
type runDataJSON struct {
	Sequence       int                     `json:"sequence"`
	StepID         *StepID                 `json:"step_id,omitempty"`
	RecordedAt     time.Time               `json:"recorded_at"`
	Kind           string                  `json:"kind"`
	Values         json.RawMessage         `json:"values,omitempty"`
	Output         json.RawMessage         `json:"output,omitempty"`
	EntityID       devices.EntityID        `json:"entity_id,omitempty"`
	Pointer        *string                 `json:"value_pointer,omitempty"`
	ReadAt         *time.Time              `json:"read_at,omitempty"`
	Observation    *dataObservationJSON    `json:"observation,omitempty"`
	UnusableReason *ConditionUnknownReason `json:"unusable_reason,omitempty"`
	Path           *string                 `json:"path,omitempty"`
	FailureCode    string                  `json:"failure_code,omitempty"`
	SourceStart    *int                    `json:"source_start,omitempty"`
	SourceEnd      *int                    `json:"source_end,omitempty"`
	State          *stateReadJSON          `json:"state,omitempty"`
}

func dataObservation(value *ObservationEvidence) *dataObservationJSON {
	if value == nil {
		return nil
	}
	return &dataObservationJSON{ObservationID: value.ObservationID, ObservedAt: value.ObservedAt}
}
func observationFromData(value *dataObservationJSON) *ObservationEvidence {
	if value == nil {
		return nil
	}
	return &ObservationEvidence{ObservationID: value.ObservationID, ObservedAt: value.ObservedAt}
}

func encodeRunData(record RunDataRecord) (json.RawMessage, error) {
	value := runDataJSON{Sequence: record.Sequence, StepID: record.StepID, RecordedAt: record.RecordedAt}
	switch body := record.Body.(type) {
	case VariablesInitialized:
		value.Kind, value.Values = "variables_initialized", body.Values
	case InputsResolved:
		value.Kind, value.Values = "inputs_resolved", body.Values
	case VariablesAssigned:
		value.Kind, value.Values = "variables_assigned", body.Values
	case StateCaptured:
		value.Kind, value.Output = "state_captured", body.Output
		value.EntityID, value.Pointer, value.ReadAt = body.EntityID, &body.Pointer, &body.ReadAt
		value.Observation, value.UnusableReason = dataObservation(body.Observation), body.UnusableReason
	case FunctionOutputCaptured:
		value.Kind, value.Output = "function_output", body.Output
	case RunDataFailed:
		value.Kind, value.Path, value.FailureCode = "failed", &body.Path, body.FailureCode
		value.SourceStart, value.SourceEnd = body.SourceStart, body.SourceEnd
		if body.State != nil {
			state := body.State
			value.State = &stateReadJSON{EntityID: state.EntityID, Pointer: state.Pointer, ReadAt: state.ReadAt,
				Observation: dataObservation(state.Observation), UnusableReason: &state.UnusableReason}
		}
	default:
		return nil, invalid("unsupported Run data body")
	}
	return json.Marshal(value)
}

// EncodeRunDataRecord returns the canonical flattened, exact-value wire record.
func EncodeRunDataRecord(record RunDataRecord) (json.RawMessage, error) {
	if err := ValidateRunDataRecord(record); err != nil {
		return nil, err
	}
	return encodeRunData(record)
}

// DecodeRunDataRecord rejects unknown, wrong-variant and missing fields.
func DecodeRunDataRecord(raw json.RawMessage) (RunDataRecord, error) {
	if len(raw) > RunDataMaxRecordBytes {
		return RunDataRecord{}, invalid("Run data record exceeds bound")
	}
	var value runDataJSON
	if err := DecodeStrictJSONObject(raw, &value); err != nil {
		return RunDataRecord{}, invalid("invalid Run data record")
	}
	record := RunDataRecord{Sequence: value.Sequence, StepID: value.StepID, RecordedAt: value.RecordedAt}
	switch value.Kind {
	case "variables_initialized":
		record.Body = VariablesInitialized{Values: value.Values}
	case "inputs_resolved":
		record.Body = InputsResolved{Values: value.Values}
	case "variables_assigned":
		record.Body = VariablesAssigned{Values: value.Values}
	case "function_output":
		record.Body = FunctionOutputCaptured{Output: value.Output}
	case "state_captured":
		if value.Pointer == nil || value.ReadAt == nil {
			return RunDataRecord{}, invalid("State capture requires pointer and time")
		}
		record.Body = StateCaptured{
			EntityID: value.EntityID,
			Pointer:  *value.Pointer,
			ReadAt:   *value.ReadAt,
			Observation: observationFromData(
				value.Observation,
			),
			UnusableReason: value.UnusableReason,
			Output:         value.Output,
		}
	case "failed":
		if value.Path == nil {
			return RunDataRecord{}, invalid("data failure requires path")
		}
		body := RunDataFailed{
			Path:        *value.Path,
			FailureCode: value.FailureCode,
			SourceStart: value.SourceStart,
			SourceEnd:   value.SourceEnd,
		}
		if value.State != nil {
			if value.State.UnusableReason == nil {
				return RunDataRecord{}, invalid("State failure requires reason")
			}
			body.State = &StateReadFailure{
				EntityID:       value.State.EntityID,
				Pointer:        value.State.Pointer,
				ReadAt:         value.State.ReadAt,
				Observation:    observationFromData(value.State.Observation),
				UnusableReason: *value.State.UnusableReason,
			}
		}
		record.Body = body
	default:
		return RunDataRecord{}, invalid("unsupported Run data kind")
	}
	if err := ValidateRunDataRecord(record); err != nil {
		return RunDataRecord{}, err
	}
	// Compare key sets against the canonical selected variant, not raw spelling
	// or number tokens. This rejects even null/zero-valued extra variant fields.
	canonical, err := encodeRunData(record)
	if err != nil {
		return RunDataRecord{}, err
	}
	var actual, expected map[string]json.RawMessage
	if json.Unmarshal(raw, &actual) != nil || json.Unmarshal(canonical, &expected) != nil ||
		len(actual) != len(expected) {
		return RunDataRecord{}, invalid("Run data fields disagree with kind")
	}
	for key := range actual {
		if _, ok := expected[key]; !ok {
			return RunDataRecord{}, invalid("Run data fields disagree with kind")
		}
	}
	return record, nil
}

func validDataTime(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

//nolint:gocognit // One exhaustive body dispatch keeps envelope and selected-variant validation auditable.
func ValidateRunDataRecord(record RunDataRecord) error {
	if record.Sequence < 0 || record.Sequence >= RunDataMaxRecords || !validDataTime(record.RecordedAt) {
		return invalid("invalid Run data order or time")
	}
	if record.StepID != nil {
		if _, err := ParseStepID(string(*record.StepID)); err != nil {
			return err
		}
	}
	var err error
	switch body := record.Body.(type) {
	case VariablesInitialized:
		if record.StepID != nil || record.Sequence != 0 {
			return invalid("invalid initialization identity")
		}
		err = validateDataVariables(body.Values, false)
	case VariablesAssigned:
		if record.StepID == nil {
			return invalid("assignment requires Step")
		}
		err = validateDataVariables(body.Values, true)
	case InputsResolved:
		if record.StepID == nil {
			return invalid("inputs require Step")
		}
		err = validateResolvedInputs(body.Values)
	case StateCaptured:
		if record.StepID == nil {
			return invalid("State capture requires Step")
		}
		if _, err = exprvalue.NormalizeEvidence(body.Output); err != nil {
			return invalid("invalid captured State value")
		}
		if body.UnusableReason == nil {
			if body.Observation == nil {
				return invalid("usable State requires Observation")
			}
			err = validateStateData(body.EntityID, body.Pointer, body.ReadAt, body.Observation, "")
		} else {
			if *body.UnusableReason == "" {
				return invalid("unusable State requires a nonempty reason")
			}
			if !bytes.Equal(bytes.TrimSpace(body.Output), []byte("null")) {
				return invalid("unusable State output must be null")
			}
			err = validateStateData(body.EntityID, body.Pointer, body.ReadAt, body.Observation, *body.UnusableReason)
		}
	case FunctionOutputCaptured:
		if record.StepID == nil {
			return invalid("Function output requires Step")
		}
		_, err = exprvalue.NormalizeEvidence(body.Output)
	case RunDataFailed:
		err = validateDataFailure(body)
	default:
		return invalid("unsupported Run data body")
	}
	if err != nil {
		return err
	}
	raw, err := encodeRunData(record)
	if err != nil || len(raw) > RunDataMaxRecordBytes {
		return invalid("Run data record exceeds bound")
	}
	if _, failure := record.Body.(RunDataFailed); failure && len(raw) > RunDataFailureReserveBytes {
		return invalid("Run data failure exceeds reserved bound")
	}
	return nil
}

func validateResolvedInputs(raw json.RawMessage) error {
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid("inputs must be an object")
	}
	validFields := false
	if len(fields) == 1 {
		for name, fragment := range fields {
			if name != "parameters" && name != "arguments" {
				return invalid("invalid resolved input field")
			}
			value, err := decodeJSONValue(fragment)
			if err != nil {
				return invalid("invalid resolved input")
			}
			_, validFields = value.(map[string]any)
		}
	} else if len(fields) == resolvedNotificationFieldCount {
		title, titleErr := decodeJSONValue(fields["title"])
		message, messageErr := decodeJSONValue(fields["message"])
		_, titleOK := title.(string)
		_, messageOK := message.(string)
		validFields = titleErr == nil && messageErr == nil && titleOK && messageOK
	}
	if !validFields {
		return invalid("invalid resolved input field set or types")
	}
	for _, fragment := range fields {
		if len(fragment) > automationValueMaxBytes || !utf8.Valid(fragment) || !json.Valid(fragment) {
			return invalid("invalid input fragment")
		}
	}
	return nil
}

func validateDataVariables(raw json.RawMessage, nonempty bool) error {
	normalized, err := exprvalue.NormalizeEvidence(raw)
	if err != nil {
		return invalid("invalid variable values")
	}
	value, err := decodeJSONValue(normalized)
	if err != nil {
		return invalid("invalid variable values")
	}
	object, ok := value.(map[string]any)
	if !ok || len(object) > 64 || (nonempty && len(object) == 0) {
		return invalid("invalid variable values object")
	}
	for name := range object {
		if !validVariableName(VariableName(name)) {
			return invalid("invalid variable name")
		}
	}
	return nil
}

func validateStateData(
	entity devices.EntityID,
	pointer string,
	at time.Time,
	observation *ObservationEvidence,
	reason ConditionUnknownReason,
) error {
	if _, err := devices.ParseEntityID(string(entity)); err != nil {
		return invalid("invalid State Entity")
	}
	if !validDataTime(at) {
		return invalid("invalid State read time")
	}
	if err := ValidateJSONPointer(pointer); err != nil {
		return invalid("invalid State pointer")
	}
	if observation != nil {
		if err := validateObservationEvidence(*observation); err != nil {
			return err
		}
	}
	switch reason {
	case "":
		if observation == nil {
			return invalid("usable State requires Observation")
		}
	case ConditionUnknownEntityMissing, ConditionUnknownStateMissing:
		if observation != nil {
			return invalid("missing State cannot carry Observation")
		}
	case ConditionUnknownEvidenceInFuture, ConditionUnknownEvidenceExpired, ConditionUnknownPointerMissing:
		if observation == nil {
			return invalid("State reason requires Observation")
		}
	case ConditionUnknownTypeMismatch:
		return invalid("type mismatch is not State-read absence")
	default:
		return invalid("invalid State unusable reason")
	}
	return nil
}

func validateDataFailure(body RunDataFailed) error {
	if len(body.Path) > 512 || (body.Path != "" && !strings.HasPrefix(body.Path, "/")) {
		return invalid("invalid data failure path")
	}
	if err := ValidateJSONPointer(body.Path); err != nil {
		return invalid("invalid data failure path")
	}
	switch body.FailureCode {
	case "expression_missing",
		"expression_type",
		"expression_evaluation",
		"expression_value_invalid",
		"expression_limit",
		"resolved_input_invalid",
		"read_state_unusable",
		"read_state_read_failed",
		"read_state_snapshot_incomplete",
		"read_state_corrupt",
		"run_data_limit":
	default:
		return invalid("invalid data failure code")
	}
	if (body.SourceStart == nil) != (body.SourceEnd == nil) {
		return invalid("partial expression span")
	}
	if body.SourceStart != nil &&
		(*body.SourceStart < 0 || *body.SourceEnd < *body.SourceStart || *body.SourceEnd > 4096) {
		return invalid("invalid expression span")
	}
	if (body.State != nil) != (body.FailureCode == "read_state_unusable") {
		return invalid("State failure details disagree with code")
	}
	if body.State != nil {
		if body.State.UnusableReason == "" {
			return invalid("State failure requires a nonempty unusable reason")
		}
		return validateStateData(
			body.State.EntityID,
			body.State.Pointer,
			body.State.ReadAt,
			body.State.Observation,
			body.State.UnusableReason,
		)
	}
	return nil
}
