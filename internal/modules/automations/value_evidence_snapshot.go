package automations

import (
	"bytes"
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations/internal/exprvalue"
)

// EncodeRunValueInitializationWithPreparedSnapshot validates arbitrary initialization
// and its snapshot ownership, returning the same encoding for persistence/accounting.
func EncodeRunValueInitializationWithPreparedSnapshot(
	init RunValueInitialization,
	snapshot Definition,
) (json.RawMessage, error) {
	raw, err := EncodeRunValueInitialization(init)
	if err != nil {
		return nil, err
	}
	if err = validateInitializationOwnership(init, snapshot); err != nil {
		return nil, err
	}
	return raw, nil
}

// DecodeRunValueInitializationWithPreparedSnapshot checks retained initialization
// and its ownership, consuming the decoder's intrinsic validation.
func DecodeRunValueInitializationWithPreparedSnapshot(
	raw json.RawMessage,
	snapshot Definition,
) (RunValueInitialization, error) {
	init, _, err := DecodeRunValueInitialization(raw)
	if err != nil {
		return RunValueInitialization{}, err
	}
	return init, validateInitializationOwnership(init, snapshot)
}

func validateInitializationOwnership(init RunValueInitialization, snapshot Definition) error {
	switch b := init.Body.(type) {
	case ValuesInitialized:
		return validateVariableOwnership(b.Values, snapshot.Variables)
	case InitializationFailed:
		if b.Failure.Path == "/trigger" {
			return validateFailureSpan(b.Failure, nil)
		}
		binding, err := variableFailureBinding(snapshot.Variables, b.Failure.Path, "/variables")
		if err != nil {
			return err
		}
		return validateFailureSpan(b.Failure, binding)
	default:
		return invalid("invalid initialization")
	}
}

// ValidateStepValueOwnershipWithPreparedSnapshot checks snapshot-relative ownership.
// It consumes unchanged, intrinsically validated details from preparation, completion,
// decoding or trace validation; it does not repeat those entry points' shape checks.
func ValidateStepValueOwnershipWithPreparedSnapshot(execution StepExecution, snapshot Definition, recorded bool) error {
	step := findSnapshotStep(snapshot.Steps, execution.StepID)
	if step == nil || StepBodyKind(step.Body) != execution.Details.Kind() {
		return invalid("value evidence Step disagrees with snapshot")
	}
	return validateStepValueOwnership(execution, *step, snapshot, recorded)
}

func validateStepValueOwnership(execution StepExecution, step Step, snapshot Definition, recorded bool) error {
	var failure *ValueFailure
	var inputs json.RawMessage
	switch d := execution.Details.(type) {
	case CommandExecutionDetails:
		if stopped, ok := d.State.(CommandInterrupted); ok && stopped.Reservation == nil && d.Inputs == nil {
			// Drain may stop a reached Command before input resolution/reservation.
			// Its closed state owns the absence of both identities and inputs.
			return nil
		}
		inputs = d.Inputs
	case NotificationExecutionDetails:
		inputs = d.Inputs
	case FunctionExecutionDetails:
		inputs = d.Inputs
		failure = d.ValueFailure
		_, succeeded := d.State.(FunctionSucceeded)
		if recorded && succeeded && len(d.Output) == 0 && d.ValueFailure == nil {
			return invalid("recorded Function success requires output or value failure")
		}
	case VariablesExecutionDetails:
		config, ok := step.Body.(SetVariablesStep)
		if !ok {
			return invalid("assignment disagrees with Step representation")
		}
		return validateVariableOwnership(d.Values, config.Assignments)
	case StateExecutionDetails:
		return validateStateCaptureOwnership(d, step)
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
		path := stepBindingPath(snapshot.Steps, execution.StepID)
		if err := validateStepFailureOwnership(execution.Details, step, path, *failure); err != nil {
			return err
		}
	}
	// Intrinsic validation already checks each kind's field set and input types.
	// Inputs are destination-normalized and need not equal definition literals.
	if recorded && inputs == nil {
		switch execution.Details.(type) {
		case CommandExecutionDetails, NotificationExecutionDetails, FunctionExecutionDetails:
			return invalid("recorded effect requires inputs")
		case BranchExecutionDetails, DelayExecutionDetails, StateExecutionDetails,
			ValueFailureExecutionDetails, VariablesExecutionDetails:
		}
	}
	return nil
}

func validateStateCaptureOwnership(details StateExecutionDetails, step Step) error {
	config, ok := step.Body.(ReadStateStep)
	if !ok {
		return invalid("State evidence disagrees with Step representation")
	}
	if details.UnusableReason != nil && config.OnUnusable != ReadStateNull {
		return invalid("State fallback disagrees with snapshot")
	}
	return validateCapturedStatePredicate(config, details.ReadAt, details.Observation, details.UnusableReason)
}

func validateStepFailureOwnership(details StepExecutionDetails, step Step, path string, failure ValueFailure) error {
	if failure.Path != path && !strings.HasPrefix(failure.Path, path+"/") {
		return invalid("value failure path outside Step")
	}
	if failure.FailureCode == failureStateReadFailed || failure.FailureCode == failureStateSnapshotIncomplete ||
		failure.FailureCode == failureStateCorrupt || failure.FailureCode == failureStateUnusable {
		if _, ok := step.Body.(ReadStateStep); !ok {
			return invalid("State failure belongs to a read_state Step")
		}
	}
	binding, err := stepFailureBinding(step, details, path, failure.Path)
	if err != nil {
		return err
	}
	if err = validateFailureSpan(failure, binding); err != nil {
		return err
	}
	if failure.State == nil {
		return nil
	}
	config, ok := step.Body.(ReadStateStep)
	if !ok || config.EntityID != failure.State.EntityID || config.Pointer != failure.State.Pointer ||
		config.OnUnusable != ReadStateFail {
		return invalid("State failure disagrees with snapshot")
	}
	return validateCapturedStatePredicate(
		config,
		failure.State.ReadAt,
		failure.State.Observation,
		&failure.State.UnusableReason,
	)
}

// stepFailureBinding validates the path even when the failure has no source span.
// A nil binding denotes a valid whole-Step/batch failure, never an unknown path.
//
//nolint:nilnil // Whole-Step and whole-batch paths intentionally have no individual binding.
func stepFailureBinding(step Step, details StepExecutionDetails, path, failurePath string) (*ValueBinding, error) {
	suffix := strings.TrimPrefix(failurePath, path)
	switch config := step.Body.(type) {
	case CommandStep:
		return singleEffectFailureBinding(suffix, "/"+bindingField(config.Parameters, "parameters"), config.Parameters)
	case FunctionStep:
		if _, completed := details.(FunctionExecutionDetails); completed && suffix != "" {
			return nil, invalid("post-success Function failure must identify Step")
		}
		return singleEffectFailureBinding(suffix, "/"+bindingField(config.Arguments, "arguments"), config.Arguments)
	case NotificationStep:
		switch suffix {
		case "":
			return nil, nil
		case "/" + bindingField(config.Title, "title"):
			return &config.Title, nil
		case "/" + bindingField(config.Message, "message"):
			return &config.Message, nil
		default:
			return nil, invalid("invalid Notification failure path")
		}
	case ReadStateStep:
		if suffix != "" {
			return nil, invalid("invalid State failure path")
		}
		return nil, nil
	case SetVariablesStep:
		return variableFailureBinding(config.Assignments, suffix, "/assignments")
	case IfStep, ChooseStep:
		branch, ok := details.(BranchExecutionDetails)
		if !ok {
			return nil, invalid("branch failure disagrees with Step representation")
		}
		return branchFailureBinding(step, branch, path, failurePath)
	case DelayStep:
		return nil, invalid("Delay cannot have value failure")
	default:
		return nil, invalid("unsupported failure Step")
	}
}

//nolint:nilnil // A whole-effect failure has no binding; only the named field can carry a span.
func singleEffectFailureBinding(suffix, field string, binding ValueBinding) (*ValueBinding, error) {
	switch suffix {
	case "":
		return nil, nil
	case field:
		return &binding, nil
	default:
		return nil, invalid("invalid effect failure path")
	}
}

//nolint:nilnil // Whole-assignment batches have no individual binding.
func variableFailureBinding(assignments map[VariableName]ValueBinding, suffix, field string) (*ValueBinding, error) {
	if suffix == "" || suffix == field {
		return nil, nil
	}
	name, found := strings.CutPrefix(suffix, field+"/")
	if !found {
		name, found = strings.CutPrefix(suffix, field+"_expr/")
	}
	if !found {
		return nil, invalid("invalid assignment failure path")
	}
	binding, exists := assignments[VariableName(name)]
	if !exists || suffix != bindingField(binding, field)+"/"+name {
		return nil, invalid("assignment failure names unknown variable")
	}
	return &binding, nil
}

// branchFailureBinding locates only the evaluated root; a Choose error occurs
// at the alternative immediately after its retained false prefix.
func branchFailureBinding(
	step Step,
	details BranchExecutionDetails,
	stepPath, failurePath string,
) (*ValueBinding, error) {
	var root Condition
	var rootPath string
	switch config := step.Body.(type) {
	case IfStep:
		if _, ok := details.Decision.Body.(IfDecision); !ok {
			return nil, invalid("invalid If decision")
		}
		root, rootPath = config.Conditions, stepPath+"/conditions"
	case ChooseStep:
		decision, ok := details.Decision.Body.(ChooseDecision)
		if !ok {
			return nil, invalid("invalid Choose decision")
		}
		result, ok := decision.Result.(ChooseError)
		if !ok || len(result.Evaluations) >= len(config.Branches) {
			return nil, invalid("invalid Choose error prefix")
		}
		for index, evaluation := range result.Evaluations {
			if evaluation.BranchID != config.Branches[index].ID {
				return nil, invalid("Choose error prefix disagrees with snapshot")
			}
		}
		index := len(result.Evaluations)
		root, rootPath = config.Branches[index].Conditions, stepPath+"/branches/"+strconv.Itoa(index)+"/conditions"
	case CommandStep, DelayStep, FunctionStep, NotificationStep, ReadStateStep, SetVariablesStep:
		return nil, invalid("value failure is not on a branch")
	default:
		return nil, invalid("value failure is not on a branch")
	}
	var binding *ValueBinding
	walkExpressionBindings(root, rootPath, func(_ ConditionID, path string, value ValueBinding) {
		if path == failurePath {
			binding = &value
		}
	})
	if binding == nil {
		return nil, invalid("branch failure does not identify an expression Condition binding")
	}
	return binding, nil
}

func validateFailureSpan(failure ValueFailure, binding *ValueBinding) error {
	if failure.SourceStart == nil {
		return nil
	}
	if binding == nil {
		return invalid("failure span has no binding")
	}
	expr, ok := binding.Body.(ExpressionValue)
	if !ok || *failure.SourceEnd > len(expr.Source) {
		return invalid("failure span disagrees with binding")
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
