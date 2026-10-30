package automations

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/mholtzscher/hearth/contracts/functionschema"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

var functionFingerprintPattern = regexp.MustCompile(`^sha256-v1:[0-9a-f]{64}$`)

// FunctionReferenceError identifies a safe save-time failure without exposing arguments.
type FunctionReferenceError struct {
	StepID     StepID
	ReasonCode string
	Path       string
}

func (*FunctionReferenceError) Error() string        { return "invalid Automation Function reference" }
func (*FunctionReferenceError) Is(target error) bool { return target == ErrInvalidAutomation }

func normalizeFunctionStep(id StepID, body FunctionStep) (Step, error) {
	if adapters.ValidateAdapterID(body.AdapterID) != nil || !adapters.ValidFunctionKey(body.FunctionKey) ||
		!functionFingerprintPattern.MatchString(string(body.ContractFingerprint)) {
		return Step{}, invalid("step %q: invalid Function identity or fingerprint", id)
	}
	binding, err := normalizeValueBinding(body.Arguments)
	if err != nil {
		return Step{}, bindingAt(err, bindingField(body.Arguments, "arguments"))
	}
	if literal, ok := binding.Body.(LiteralValue); ok {
		arguments, normalizeErr := functionschema.NormalizeObject(literal.Value)
		if normalizeErr != nil {
			return Step{}, invalid("invalid Function arguments")
		}
		binding.Body = LiteralValue{Value: arguments}
	}
	body.Arguments = binding
	return Step{ID: id, Body: body}, nil
}

func (step FunctionStep) contractInput() adapters.ContractInput {
	return adapters.ContractInput{AdapterID: step.AdapterID, FunctionKey: step.FunctionKey,
		ContractFingerprint: step.ContractFingerprint, Arguments: literalBindingJSON(step.Arguments)}
}

func validateFunctionReference(
	ctx context.Context,
	functions AutomationFunctions,
	step *Step,
	body FunctionStep,
) error {
	if functions == nil {
		return fmt.Errorf("step %q: Function validation is not configured", step.ID)
	}
	if _, literal := body.Arguments.Body.(LiteralValue); !literal {
		err := functions.ValidateContractReference(ctx, adapters.ContractReference{AdapterID: body.AdapterID,
			FunctionKey: body.FunctionKey, ContractFingerprint: body.ContractFingerprint})
		if err != nil {
			if reason := functionReferenceFailure(err); reason != "" {
				return &FunctionReferenceError{StepID: step.ID, ReasonCode: reason}
			}
			return fmt.Errorf("step %q Function: %w", step.ID, err)
		}
		return nil
	}
	arguments, err := functions.ValidateContractInput(ctx, body.contractInput())
	if err != nil {
		if reason := functionReferenceFailure(err); reason != "" {
			return &FunctionReferenceError{StepID: step.ID, ReasonCode: reason}
		}
		return fmt.Errorf("step %q Function: %w", step.ID, err)
	}
	body.Arguments = ValueBinding{Body: LiteralValue{Value: arguments}}
	step.Body = body
	return nil
}

// Save-time checks use retained discovery, not runtime admission. Operational
// causes outrank a joined input class and must remain available to their owner.
func functionReferenceFailure(err error) string {
	switch {
	case errors.Is(err, adapters.ErrFunctionPersistence),
		errors.Is(err, context.Canceled),
		errors.Is(err, context.DeadlineExceeded):
		return ""
	case errors.Is(err, adapters.ErrCatalogChanged),
		errors.Is(err, adapters.ErrAdapterUnavailable),
		errors.Is(err, adapters.ErrFunctionAdmissionClosed):
		return ""
	case errors.Is(err, adapters.ErrAdapterNotFound), errors.Is(err, adapters.ErrFunctionNotFound),
		errors.Is(err, adapters.ErrFunctionContractChanged), errors.Is(err, adapters.ErrInvalidFunctionInput):
		// Reuse existing reason tokens only after the save-specific gate.
		return functionAdmissionFailure(err)
	default:
		return ""
	}
}

// FunctionWithPreparedSnapshot resolves a Function leaf without consulting its Adapter.
func FunctionWithPreparedSnapshot(snapshot Definition, id StepID) (FunctionStep, error) {
	step := findSnapshotStep(snapshot.Steps, id)
	if step != nil {
		if body, ok := step.Body.(FunctionStep); ok {
			return body, nil
		}
	}
	return FunctionStep{}, invalid("snapshot Step %q is not a Function", id)
}
