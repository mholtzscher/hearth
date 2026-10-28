package automations

import (
	"bytes"
	"encoding/json"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// EncodeRunValueInitializationWithPreparedSnapshot checks new initialization,
// including literal results under the current numeric policy, before persistence.
func EncodeRunValueInitializationWithPreparedSnapshot(init RunValueInitialization, snapshot Definition) (json.RawMessage, error) {
	raw, err := EncodeRunValueInitialization(init)
	if err != nil {
		return nil, err
	}
	if err = validateInitializationOwnership(init, snapshot); err != nil {
		return nil, err
	}
	if values, ok := init.Body.(ValuesInitialized); ok {
		if err = validateVariableLiterals(values.Values, snapshot.Variables); err != nil {
			return nil, err
		}
	}
	return raw, nil
}

// DecodeRunValueInitializationWithPreparedSnapshot checks retained initialization
// without reinterpreting historical literal results under today's numeric policy.
func DecodeRunValueInitializationWithPreparedSnapshot(raw json.RawMessage, snapshot Definition) (RunValueInitialization, error) {
	init, _, err := DecodeRunValueInitialization(raw)
	if err != nil {
		return RunValueInitialization{}, err
	}
	return init, validateInitializationOwnership(init, snapshot)
}

func validateInitializationOwnership(init RunValueInitialization, snapshot Definition) error {
	switch body := init.Body.(type) {
	case ValuesInitialized:
		return validateVariableOwnership(body.Values, snapshot.Variables)
	case InitializationFailed:
		if body.Failure.Field == "trigger" {
			return nil
		}
		return validateVariableFailureField(snapshot.Variables, body.Failure.Field, "variables")
	default:
		return invalid("invalid initialization")
	}
}

// ValidateStepValueOwnershipWithPreparedSnapshot checks new write ownership,
// consuming intrinsically validated details and a prepared definition.
func ValidateStepValueOwnershipWithPreparedSnapshot(execution StepExecution, snapshot Definition, recorded bool) error {
	step := findSnapshotStep(snapshot.Steps, execution.StepID)
	if step == nil || StepBodyKind(step.Body) != execution.Details.Kind() {
		return invalid("value evidence Step disagrees with snapshot")
	}
	if err := validateStepValueOwnership(execution, *step, recorded); err != nil {
		return err
	}
	if values, ok := execution.Details.(VariablesExecutionDetails); ok {
		return validateVariableLiterals(values.Values, step.Body.(SetVariablesStep).Assignments)
	}
	return nil
}

func validateStepValueOwnership(execution StepExecution, step Step, recorded bool) error {
	if !recorded && hasStepValues(execution.Details) {
		return invalid("legacy Run has value execution evidence")
	}
	var failure *ValueFailure
	inputsPresent := true
	switch d := execution.Details.(type) {
	case CommandExecutionDetails:
		if stopped, ok := d.State.(CommandInterrupted); ok && stopped.Reservation == nil && d.Parameters == nil {
			return nil // Drain can stop a Command before resolution and reservation.
		}
		inputsPresent = d.Parameters != nil
	case NotificationExecutionDetails:
		inputsPresent = d.Message != nil
	case FunctionExecutionDetails:
		inputsPresent = d.Arguments != nil
		failure = d.ValueFailure
		_, succeeded := d.State.(FunctionSucceeded)
		if recorded && succeeded && len(d.Output) == 0 && failure == nil {
			return invalid("recorded Function success requires output or value failure")
		}
	case VariablesExecutionDetails:
		config, ok := step.Body.(SetVariablesStep)
		if !ok {
			return invalid("assignment disagrees with Step representation")
		}
		return validateVariableOwnership(d.Values, config.Assignments)
	case StateExecutionDetails:
		config, ok := step.Body.(ReadStateStep)
		if !ok || d.UnusableReason != nil && config.OnUnusable != ReadStateNull {
			return invalid("State capture disagrees with snapshot")
		}
		return validateCapturedStatePredicate(config, d.ReadAt, d.Observation, d.UnusableReason)
	case ValueFailureExecutionDetails:
		failure = &d.Failure
	case BranchExecutionDetails:
		if d.Decision.StepID != execution.StepID {
			return invalid("branch decision Step identity mismatch")
		}
		failure = d.ValueFailure
	case DelayExecutionDetails:
	}
	if failure != nil {
		if err := validateStepFailureOwnership(execution.Details, step, *failure); err != nil {
			return err
		}
	}
	if recorded && !inputsPresent {
		return invalid("recorded effect requires inputs")
	}
	return nil
}

func hasStepValues(details StepExecutionDetails) bool {
	switch d := details.(type) {
	case CommandExecutionDetails:
		return d.Parameters != nil
	case NotificationExecutionDetails:
		return d.Message != nil
	case FunctionExecutionDetails:
		return d.Arguments != nil || d.Output != nil || d.ValueFailure != nil
	case BranchExecutionDetails:
		return d.ValueFailure != nil
	case VariablesExecutionDetails, StateExecutionDetails, ValueFailureExecutionDetails:
		return true
	case DelayExecutionDetails:
		return false
	default:
		return false
	}
}

func validateStepFailureOwnership(details StepExecutionDetails, step Step, failure ValueFailure) error {
	if failure.FailureCode == failureStateReadFailed || failure.FailureCode == failureStateSnapshotIncomplete ||
		failure.FailureCode == failureStateCorrupt || failure.FailureCode == failureStateUnusable {
		if _, ok := step.Body.(ReadStateStep); !ok {
			return invalid("State failure belongs to a read_state Step")
		}
	}
	if branch, ok := details.(BranchExecutionDetails); ok {
		return validateBranchFailureCondition(step, branch, failure)
	}
	if failure.ConditionID != "" {
		return invalid("nonbranch failure cannot name a Condition")
	}
	if _, completed := details.(FunctionExecutionDetails); completed && failure.Field != "" {
		return invalid("post-success Function failure must identify Step")
	}
	switch config := step.Body.(type) {
	case CommandStep:
		if failure.Field != "" && failure.Field != "parameters" {
			return invalid("invalid Command failure field")
		}
	case FunctionStep:
		if failure.Field != "" && failure.Field != "arguments" {
			return invalid("invalid Function failure field")
		}
	case NotificationStep:
		if failure.Field != "" && failure.Field != "title" && failure.Field != "message" {
			return invalid("invalid Notification failure field")
		}
	case SetVariablesStep:
		return validateVariableFailureField(config.Assignments, failure.Field, "assignments")
	case ReadStateStep:
		if failure.Field != "" {
			return invalid("invalid State failure field")
		}
		if failure.State != nil {
			state := failure.State
			if config.EntityID != state.EntityID || config.Pointer != state.Pointer || config.OnUnusable != ReadStateFail {
				return invalid("State failure disagrees with snapshot")
			}
			return validateCapturedStatePredicate(config, state.ReadAt, state.Observation, &state.UnusableReason)
		}
	case IfStep, ChooseStep, DelayStep:
		return invalid("value failure disagrees with Step representation")
	}
	return nil
}

func validateVariableFailureField(bindings map[VariableName]ValueBinding, field, scope string) error {
	if field == "" || field == scope {
		return nil
	}
	name, found := strings.CutPrefix(field, scope+".")
	if _, exists := bindings[VariableName(name)]; !found || !exists {
		return invalid("failure names unknown variable")
	}
	return nil
}

func validateBranchFailureCondition(step Step, details BranchExecutionDetails, failure ValueFailure) error {
	if failure.Field != "" {
		return invalid("branch failure cannot name an input field")
	}
	// Old history can contain a whole-branch quota failure; new writes never emit it.
	if failure.FailureCode == failureRunDataLimit && failure.ConditionID == "" {
		return nil
	}
	var root Condition
	switch config := step.Body.(type) {
	case IfStep:
		root = config.Conditions
	case ChooseStep:
		decision, ok := details.Decision.Body.(ChooseDecision)
		if !ok {
			return invalid("invalid Choose failure")
		}
		result, ok := decision.Result.(ChooseError)
		if !ok || len(result.Evaluations) >= len(config.Branches) {
			return invalid("invalid Choose error prefix")
		}
		root = config.Branches[len(result.Evaluations)].Conditions
	case CommandStep, DelayStep, FunctionStep, NotificationStep, ReadStateStep, SetVariablesStep:
		return invalid("Condition failure belongs to a branch")
	}
	if !hasExpressionCondition(root, failure.ConditionID) {
		return invalid("failure does not identify an expression in the failing Condition root")
	}
	return nil
}

func validateCapturedStatePredicate(step ReadStateStep, at time.Time, observation *ObservationEvidence, reason *ConditionUnknownReason) error {
	if observation == nil {
		return nil
	}
	ageReason, unusable := boundedEvidenceUnknownReason(EntityStateCondition{MaxAgeSeconds: step.MaxAgeSeconds}, observation.ObservedAt, at)
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
	for name := range bindings {
		if _, exists := values[string(name)]; !exists {
			return invalid("assignment field is absent")
		}
	}
	return nil
}

// New writes must match their literal bindings. Historical reads check ownership
// and shape without re-deriving results under a potentially changed numeric policy.
func validateVariableLiterals(raw json.RawMessage, bindings map[VariableName]ValueBinding) error {
	var values map[string]json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return invalid("invalid assigned values")
	}
	for name, binding := range bindings {
		if literal, ok := binding.Body.(LiteralValue); ok {
			left, err := exprvalue.DecodeValue(literal.Value)
			right, otherErr := exprvalue.NormalizeEvidence(values[string(name)])
			if err != nil || otherErr != nil || !bytes.Equal(left.JSON, right) {
				return invalid("assigned literal disagrees with snapshot")
			}
		}
	}
	return nil
}
