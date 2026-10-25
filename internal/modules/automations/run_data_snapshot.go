package automations

import (
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// ValidateRunDataWithPreparedSnapshot validates ownership and exact literal
// values against an unchanged normalized immutable snapshot. Repository callers
// additionally enforce running-parent, journal order, uniqueness and phase gates.
//
//nolint:gocognit // One closed dispatch validates each journal kind against its corresponding immutable Step family.
func ValidateRunDataWithPreparedSnapshot(record RunDataRecord, snapshot Definition) error {
	if err := ValidateRunDataRecord(record); err != nil {
		return err
	}
	if record.StepID == nil {
		return validateInitializationData(record, snapshot.Variables)
	}
	step := findSnapshotStep(snapshot.Steps, *record.StepID)
	if step == nil {
		return invalid("Run data Step is absent from snapshot")
	}
	switch body := record.Body.(type) {
	case InputsResolved:
		fields := map[string]ValueBinding{}
		switch binding := step.Body.(type) {
		case CommandStep:
			fields["parameters"] = binding.Parameters
		case FunctionStep:
			fields["arguments"] = binding.Arguments
		case NotificationStep:
			fields["title"], fields["message"] = binding.Title, binding.Message
		case DelayStep, ReadStateStep, SetVariablesStep, IfStep, ChooseStep:
			return invalid("Step cannot resolve effect inputs")
		default:
			return invalid("Step cannot resolve effect inputs")
		}
		return validateAssignmentOwnership(body.Values, fieldsToVariables(fields))
	case VariablesAssigned:
		binding, ok := step.Body.(SetVariablesStep)
		if !ok {
			return invalid("Step cannot assign variables")
		}
		return validateVariableOwnership(body.Values, binding.Assignments)
	case StateCaptured:
		binding, ok := step.Body.(ReadStateStep)
		if !ok || binding.EntityID != body.EntityID || binding.Pointer != body.Pointer ||
			(body.UnusableReason != nil && binding.OnUnusable != ReadStateNull) {
			return invalid("State capture disagrees with snapshot")
		}
		return validateCapturedStatePredicate(binding, body.ReadAt, body.Observation, body.UnusableReason)
	case FunctionOutputCaptured:
		if _, ok := step.Body.(FunctionStep); !ok {
			return invalid("Step cannot produce Function output")
		}
	case RunDataFailed:
		path := stepBindingPath(snapshot.Steps, *record.StepID, "/steps")
		if body.Path != path && !strings.HasPrefix(body.Path, path+"/") {
			return invalid("data failure path is outside its Step")
		}
		if body.State != nil {
			binding, ok := step.Body.(ReadStateStep)
			if !ok || binding.EntityID != body.State.EntityID || binding.Pointer != body.State.Pointer ||
				binding.OnUnusable != ReadStateFail {
				return invalid("State failure disagrees with snapshot")
			}
			return validateCapturedStatePredicate(
				binding,
				body.State.ReadAt,
				body.State.Observation,
				&body.State.UnusableReason,
			)
		}
	case VariablesInitialized:
		return invalid("initialization cannot identify a Step")
	default:
		return invalid("unsupported data body")
	}
	return nil
}

func validateInitializationData(record RunDataRecord, variables map[VariableName]ValueBinding) error {
	switch body := record.Body.(type) {
	case VariablesInitialized:
		return validateVariableOwnership(body.Values, variables)
	case RunDataFailed:
		if record.Sequence != 0 {
			return invalid("initial failure must be first")
		}
		if body.Path != "/variables" && !strings.HasPrefix(body.Path, "/variables/") && body.Path != "/trigger" {
			return invalid("initialization failure path is invalid")
		}
		return nil
	case InputsResolved, VariablesAssigned, StateCaptured, FunctionOutputCaptured:
		return invalid("data record requires Step identity")
	default:
		return invalid("data record requires Step identity")
	}
}

func validateCapturedStatePredicate(
	step ReadStateStep,
	at time.Time,
	observation *ObservationEvidence,
	reason *ConditionUnknownReason,
) error {
	if observation == nil {
		return nil
	}
	ageReason, unusable := boundedEvidenceUnknownReason(
		EntityStateCondition{MaxAgeSeconds: step.MaxAgeSeconds},
		observation.ObservedAt,
		at,
	)
	if unusable {
		if reason == nil || *reason != ageReason {
			return invalid("State age reason disagrees with snapshot")
		}
		return nil
	}
	if reason != nil {
		if *reason == ConditionUnknownEvidenceExpired || *reason == ConditionUnknownEvidenceInFuture {
			return invalid("State age reason does not apply")
		}
		if *reason == ConditionUnknownPointerMissing && step.Pointer == "" {
			return invalid("whole-State pointer cannot be missing")
		}
	}
	return nil
}

func validateVariableOwnership(raw json.RawMessage, bindings map[VariableName]ValueBinding) error {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) != len(bindings) {
		return invalid("assignment field set disagrees with snapshot")
	}
	for name, binding := range bindings {
		value, exists := values[string(name)]
		if !exists {
			return invalid("assignment field is absent")
		}
		if literal, ok := binding.Body.(LiteralValue); ok {
			left, err := exprvalue.DecodeValue(literal.Value)
			right, otherErr := exprvalue.DecodeValue(value)
			if err != nil || otherErr != nil || !reflect.DeepEqual(left.Data, right.Data) {
				return invalid("assigned literal disagrees with snapshot")
			}
		}
	}
	return nil
}

func fieldsToVariables(fields map[string]ValueBinding) map[VariableName]ValueBinding {
	bindings := make(map[VariableName]ValueBinding, len(fields))
	for name, value := range fields {
		bindings[VariableName(name)] = value
	}
	return bindings
}

func validateAssignmentOwnership(raw json.RawMessage, bindings map[VariableName]ValueBinding) error {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil || len(values) != len(bindings) {
		return invalid("resolved field set disagrees with snapshot")
	}
	for name, binding := range bindings {
		value, ok := values[string(name)]
		if !ok {
			return invalid("resolved field is absent")
		}
		if literal, isLiteral := binding.Body.(LiteralValue); isLiteral {
			left, err := decodeJSONValue(literal.Value)
			right, otherErr := decodeJSONValue(value)
			if err != nil || otherErr != nil || !reflect.DeepEqual(left, right) {
				return invalid("resolved literal disagrees with snapshot")
			}
		}
	}
	return nil
}
