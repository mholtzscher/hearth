package automations

import (
	"bytes"
	"encoding/json"
	"reflect"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// ValidateRunValueInitializationWithPreparedSnapshot checks initialization against a prepared snapshot.
func ValidateRunValueInitializationWithPreparedSnapshot(init RunValueInitialization, snapshot Definition) error {
	if _, err := EncodeRunValueInitialization(init); err != nil {
		return err
	}
	switch b := init.Body.(type) {
	case ValuesInitialized:
		return validateVariableOwnership(b.Values, snapshot.Variables)
	case InitializationFailed:
		if b.Failure.Path != "/variables" && !strings.HasPrefix(b.Failure.Path, "/variables/") &&
			b.Failure.Path != "/trigger" {
			return invalid("initial failure path is invalid")
		}
		if strings.HasPrefix(b.Failure.Path, "/variables/") {
			binding, ok := snapshot.Variables[VariableName(strings.TrimPrefix(b.Failure.Path, "/variables/"))]
			if !ok {
				return invalid("initial failure names unknown variable")
			}
			if err := validateFailureSpan(b.Failure, binding); err != nil {
				return err
			}
		} else if b.Failure.SourceStart != nil {
			return invalid("initial failure span has no binding")
		}
		return nil
	default:
		return invalid("invalid initialization")
	}
}

// ValidateStepValueEvidenceWithPreparedSnapshot validates value ownership against the retained revision.
func ValidateStepValueEvidenceWithPreparedSnapshot(execution StepExecution, snapshot Definition, recorded bool) error {
	if err := validateStepExecutionDetails(execution.Details); err != nil {
		return err
	}
	step := findSnapshotStep(snapshot.Steps, execution.StepID)
	if step == nil || StepBodyKind(step.Body) != execution.Details.Kind() {
		return invalid("value evidence Step disagrees with snapshot")
	}
	path := stepBindingPath(snapshot.Steps, execution.StepID, "/steps")
	var failure *ValueFailure
	var inputs json.RawMessage
	switch d := execution.Details.(type) {
	case CommandExecutionDetails:
		inputs = d.Inputs
	case NotificationExecutionDetails:
		inputs = d.Inputs
	case FunctionExecutionDetails:
		inputs = d.Inputs
		failure = d.ValueFailure
		if recorded {
			if _, ok := d.State.(FunctionSucceeded); ok && len(d.Output) == 0 && d.ValueFailure == nil {
				return invalid("recorded Function success requires output or value failure")
			}
		}
	case VariablesExecutionDetails:
		return validateVariableOwnership(d.Values, step.Body.(SetVariablesStep).Assignments)
	case StateExecutionDetails:
		config := step.Body.(ReadStateStep)
		if d.UnusableReason != nil && config.OnUnusable != ReadStateNull {
			return invalid("State fallback disagrees with snapshot")
		}
		return validateCapturedStatePredicate(config, d.ReadAt, d.Observation, d.UnusableReason)
	case ValueFailureExecutionDetails:
		failure = &d.Failure
	case BranchExecutionDetails:
		failure = d.ValueFailure
	case DelayExecutionDetails:
	}
	if failure != nil {
		if failure.Path != path && !strings.HasPrefix(failure.Path, path+"/") {
			return invalid("value failure path outside Step")
		}
		suffix := strings.TrimPrefix(failure.Path, path)
		if failure.FailureCode == "read_state_read_failed" || failure.FailureCode == "read_state_snapshot_incomplete" ||
			failure.FailureCode == "read_state_corrupt" ||
			failure.FailureCode == "read_state_unusable" {
			if _, ok := step.Body.(ReadStateStep); !ok {
				return invalid("State failure belongs to a read_state Step")
			}
		}
		var binding *ValueBinding
		switch config := step.Body.(type) {
		case CommandStep:
			if suffix != "" && suffix != "/parameters" {
				return invalid("invalid Command failure path")
			}
			if suffix == "/parameters" {
				binding = &config.Parameters
			}
		case FunctionStep:
			if suffix != "" && suffix != "/arguments" {
				return invalid("invalid Function failure path")
			}
			if _, ok := execution.Details.(FunctionExecutionDetails); ok && suffix != "" {
				return invalid("post-success Function failure must identify Step")
			}
			if suffix == "/arguments" {
				binding = &config.Arguments
			}
		case NotificationStep:
			if suffix != "" && suffix != "/title" && suffix != "/message" {
				return invalid("invalid Notification failure path")
			}
			if suffix == "/title" {
				binding = &config.Title
			}
			if suffix == "/message" {
				binding = &config.Message
			}
		case ReadStateStep:
			if suffix != "" {
				return invalid("invalid State failure path")
			}
		case SetVariablesStep:
			if suffix != "" && suffix != "/assignments" {
				if !strings.HasPrefix(suffix, "/assignments/") {
					return invalid("invalid assignment failure path")
				}
				if _, ok := config.Assignments[VariableName(strings.TrimPrefix(suffix, "/assignments/"))]; !ok {
					return invalid("assignment failure names unknown variable")
				}
				value := config.Assignments[VariableName(strings.TrimPrefix(suffix, "/assignments/"))]
				binding = &value
			}
		case IfStep, ChooseStep:
			if suffix != "" && !strings.Contains(suffix, "/conditions") {
				return invalid("branch failure does not identify a Condition")
			}
		case DelayStep:
			return invalid("Delay cannot have value failure")
		}
		if failure.SourceStart != nil {
			if binding != nil {
				if err := validateFailureSpan(*failure, *binding); err != nil {
					return err
				}
			} else if _, branch := execution.Details.(BranchExecutionDetails); !branch {
				return invalid("failure span has no binding")
			}
		}
		if failure.State != nil {
			config, ok := step.Body.(ReadStateStep)
			if !ok || config.EntityID != failure.State.EntityID || config.Pointer != failure.State.Pointer ||
				config.OnUnusable != ReadStateFail {
				return invalid("State failure disagrees with snapshot")
			}
			if err := validateCapturedStatePredicate(
				config,
				failure.State.ReadAt,
				failure.State.Observation,
				&failure.State.UnusableReason,
			); err != nil {
				return err
			}
		}
	}
	if inputs == nil {
		if recorded {
			switch execution.Details.(type) {
			case CommandExecutionDetails, NotificationExecutionDetails, FunctionExecutionDetails:
				return invalid("recorded effect requires inputs")
			}
		}
		return nil
	}
	fields := map[string]ValueBinding{}
	switch b := step.Body.(type) {
	case CommandStep:
		fields["parameters"] = b.Parameters
	case FunctionStep:
		fields["arguments"] = b.Arguments
	case NotificationStep:
		fields["title"], fields["message"] = b.Title, b.Message
	default:
		return invalid("inputs on non-effect Step")
	}
	return validateAssignmentOwnership(inputs, fieldsToVariables(fields))
}

func validateFailureSpan(failure ValueFailure, binding ValueBinding) error {
	if failure.SourceStart == nil {
		return nil
	}
	expr, ok := binding.Body.(ExpressionValue)
	if !ok || *failure.SourceEnd > len(expr.Source) {
		return invalid("failure span disagrees with binding")
	}
	return nil
}

// ValidateRunValueTraceWithPreparedSnapshot checks initialization and every selected execution.
func ValidateRunValueTraceWithPreparedSnapshot(
	snapshot Definition,
	matches []TriggerID,
	status RunStatus,
	initialization *RunValueInitialization,
	trace []StepExecution,
	recorded bool,
) error {
	if !recorded && initialization != nil {
		return invalid("legacy Run cannot contain initialization")
	}
	if recorded {
		if initialization == nil {
			if len(trace) > 0 || status == RunSucceeded {
				return invalid("recorded Run requires initialization")
			}
		} else {
			if err := ValidateRunValueInitializationWithPreparedSnapshot(*initialization, snapshot); err != nil {
				return err
			}
			if _, failed := initialization.Body.(InitializationFailed); failed &&
				(len(trace) > 0 || status != RunFailed) {
				return invalid("initialization failure disagrees with trace")
			}
		}
	}
	if err := ValidateStepExecutionTraceWithPreparedSnapshot(snapshot, matches, status, trace); err != nil {
		return err
	}
	for _, execution := range trace {
		if err := ValidateStepValueEvidenceWithPreparedSnapshot(execution, snapshot, recorded); err != nil {
			return err
		}
	}
	return nil
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
			right, otherErr := exprvalue.NormalizeEvidence(value)
			if err != nil || otherErr != nil || !bytes.Equal(left.JSON, right) {
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
