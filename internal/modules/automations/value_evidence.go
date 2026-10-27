//nolint:goconst // Value-failure codes are fixed by the wire contract.
package automations

import (
	"encoding/json"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	failureRunDataLimit            = "run_data_limit"
	failureExpressionLimit         = "expression_limit"
	failureResolvedInputInvalid    = "resolved_input_invalid"
	failureExpressionValueInvalid  = "expression_value_invalid"
	failureStateReadFailed         = "read_state_read_failed"
	failureStateSnapshotIncomplete = "read_state_snapshot_incomplete"
	failureStateCorrupt            = "read_state_corrupt"
	failureStateUnusable           = "read_state_unusable"
)

type ValueFailure struct {
	Path        string            `json:"path"`
	FailureCode string            `json:"failure_code"`
	SourceStart *int              `json:"source_start,omitempty"`
	SourceEnd   *int              `json:"source_end,omitempty"`
	State       *StateReadFailure `json:"state,omitempty"`
}

type StateReadFailure struct {
	EntityID       devices.EntityID       `json:"entity_id"`
	Pointer        string                 `json:"value_pointer"`
	ReadAt         time.Time              `json:"read_at"`
	Observation    *ObservationEvidence   `json:"observation,omitempty"`
	UnusableReason ConditionUnknownReason `json:"unusable_reason"`
}

func validDataTime(value time.Time) bool { return !value.IsZero() && value.Location() == time.UTC }

func validateKindInputs(raw json.RawMessage, kind StepKind) error {
	if len(raw) == 0 {
		return nil // Legacy evidence may omit inputs; recorded ownership requires them.
	}
	var fields map[string]json.RawMessage
	if json.Unmarshal(raw, &fields) != nil || fields == nil {
		return invalid("inputs must be an object")
	}
	var names []string
	switch kind {
	case StepKindCommand:
		names = []string{"parameters"}
	case StepKindFunction:
		names = []string{"arguments"}
	case StepKindNotification:
		names = []string{"title", "message"}
	case StepKindIf, StepKindChoose, StepKindDelay, StepKindSetVariables, StepKindReadState:
		return invalid("inputs disagree with execution kind")
	}
	if len(names) == 0 || len(fields) != len(names) {
		return invalid("inputs disagree with execution kind")
	}
	for _, name := range names {
		fragment := fields[name]
		if len(fragment) > automationValueMaxBytes || !utf8.Valid(fragment) {
			return invalid("invalid input fragment")
		}
		value, err := decodeJSONValue(fragment)
		if err != nil {
			return invalid("invalid resolved input")
		}
		_, object := value.(map[string]any)
		_, text := value.(string)
		if kind == StepKindNotification && !text || kind != StepKindNotification && !object {
			return invalid("invalid resolved input type")
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

func validateDataFailure(body ValueFailure) error {
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
