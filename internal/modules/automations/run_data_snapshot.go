package automations

import (
	"encoding/json"
	"reflect"
)

// ValidateRunDataWithPreparedSnapshot validates ownership and exact literal
// values against an unchanged normalized immutable snapshot. Repository callers
// additionally enforce running-parent, journal order, uniqueness and phase gates.
func ValidateRunDataWithPreparedSnapshot(record RunDataRecord, snapshot Definition) error {
	if err := ValidateRunDataRecord(record); err != nil {
		return err
	}
	if record.StepID == nil {
		switch body := record.Body.(type) {
		case VariablesInitialized:
			return validateAssignmentOwnership(body.Values, snapshot.Variables)
		case RunDataFailed:
			if record.Sequence != 0 {
				return invalid("initial failure must be first")
			}
			return nil
		default:
			return invalid("data record requires Step identity")
		}
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
		default:
			return invalid("Step cannot resolve effect inputs")
		}
		return validateAssignmentOwnership(body.Values, fieldsToVariables(fields))
	case VariablesAssigned:
		binding, ok := step.Body.(SetVariablesStep)
		if !ok {
			return invalid("Step cannot assign variables")
		}
		return validateAssignmentOwnership(body.Values, binding.Assignments)
	case StateCaptured:
		binding, ok := step.Body.(ReadStateStep)
		if !ok || binding.EntityID != body.EntityID || binding.Pointer != body.Pointer ||
			(body.UnusableReason != nil && binding.OnUnusable != ReadStateNull) {
			return invalid("State capture disagrees with snapshot")
		}
	case FunctionOutputCaptured:
		if _, ok := step.Body.(FunctionStep); !ok {
			return invalid("Step cannot produce Function output")
		}
	case RunDataFailed:
		if body.State != nil {
			binding, ok := step.Body.(ReadStateStep)
			if !ok || binding.EntityID != body.State.EntityID || binding.Pointer != body.State.Pointer ||
				binding.OnUnusable != ReadStateFail {
				return invalid("State failure disagrees with snapshot")
			}
		}
	case VariablesInitialized:
		return invalid("initialization cannot identify a Step")
	default:
		return invalid("unsupported data body")
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
