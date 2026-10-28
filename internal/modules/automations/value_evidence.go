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
	valueFieldTrigger              = "trigger"
	valueFieldVariables            = "variables"
	valueFieldAssignments          = "assignments"
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
	Field       string            `json:"field,omitempty"`
	ConditionID ConditionID       `json:"condition_id,omitempty"`
	FailureCode string            `json:"failure_code"`
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

func validateResolvedObject(raw json.RawMessage) error {
	if len(raw) > automationValueMaxBytes || !utf8.Valid(raw) {
		return invalid("invalid input fragment")
	}
	value, err := decodeJSONValue(raw)
	if err != nil {
		return invalid("invalid resolved input")
	}
	if _, ok := value.(map[string]any); !ok {
		return invalid("resolved input must be an object")
	}
	return nil
}

func validateResolvedText(text string) error {
	raw, err := json.Marshal(text)
	if err != nil || !utf8.ValidString(text) || len(raw) > automationValueMaxBytes {
		return invalid("invalid resolved text")
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
	if body.ConditionID != "" && (body.Field != "" || !subjectSlugPattern.MatchString(string(body.ConditionID))) {
		return invalid("invalid failure Condition")
	}
	switch body.Field {
	case "",
		valueFieldTrigger,
		valueFieldVariables,
		valueFieldAssignments,
		"parameters",
		"arguments",
		"title",
		"message":
	default:
		scope, name, found := strings.Cut(body.Field, ".")
		if !found || scope != valueFieldVariables && scope != valueFieldAssignments ||
			!validVariableName(VariableName(name)) {
			return invalid("invalid failure field")
		}
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
	if (body.State != nil) != (body.FailureCode == "read_state_unusable") {
		return invalid("State failure details disagree with code")
	}
	if body.State != nil {
		if body.Field != "" || body.ConditionID != "" {
			return invalid("State failure cannot name an input or Condition")
		}
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
