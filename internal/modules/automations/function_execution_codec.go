package automations

import (
	"encoding/json"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

type functionWire struct {
	Inputs            json.RawMessage       `json:"inputs,omitempty"`
	Output            json.RawMessage       `json:"output,omitempty"`
	ValueFailure      *ValueFailure         `json:"value_failure,omitempty"`
	InvocationID      adapters.InvocationID `json:"invocation_id"`
	CatalogRevision   adapters.CatalogID    `json:"catalog_revision,omitempty"`
	InvocationOutcome string                `json:"invocation_outcome,omitempty"`
	ReasonCode        string                `json:"reason_code,omitempty"`
}

func (details FunctionExecutionDetails) encodeValidated() (any, error) {
	wire := functionWire{
		InvocationID: details.InvocationID,
		Inputs:       details.Inputs,
		Output:       details.Output,
		ValueFailure: details.ValueFailure,
	}
	switch state := details.State.(type) {
	case FunctionRunning:
	case FunctionSucceeded:
		wire.CatalogRevision = state.CatalogRevision
		wire.InvocationOutcome = executionSucceeded
	case FunctionFailed:
		switch evidence := state.Evidence.(type) {
		case FunctionAdmissionRejected:
			wire.ReasonCode = evidence.ReasonCode
		case FunctionInvocationFailed:
			wire.CatalogRevision = evidence.CatalogRevision
			var err error
			wire, err = functionFailureOutcomeToWire(wire, evidence.Outcome)
			if err != nil {
				return nil, err
			}
		default:
			return nil, invalid("invalid Function failure")
		}
	case FunctionInterrupted:
		wire.ReasonCode = state.ReasonCode
	default:
		return nil, invalid("invalid Function state")
	}
	return wire, nil
}

func decodeFunctionExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	var wire functionWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return nil, err
	}
	details := FunctionExecutionDetails{
		StartedAt:    env.StartedAt,
		InvocationID: wire.InvocationID,
		Inputs:       wire.Inputs,
		Output:       wire.Output,
		ValueFailure: wire.ValueFailure,
	}
	switch env.Phase {
	case executionRunning:
		if env.CompletedAt != nil || wire.CatalogRevision != "" || wire.InvocationOutcome != "" ||
			wire.ReasonCode != "" {
			return nil, invalid("invalid running Function")
		}
		details.State = FunctionRunning{}
	case executionSucceeded:
		if env.CompletedAt == nil || wire.CatalogRevision == "" || wire.InvocationOutcome != executionSucceeded ||
			wire.ReasonCode != "" {
			return nil, invalid("invalid successful Function")
		}
		details.State = FunctionSucceeded{CompletedAt: *env.CompletedAt, CatalogRevision: wire.CatalogRevision}
	case executionFailed:
		if wire.InvocationOutcome == executionSucceeded && wire.ValueFailure != nil && env.CompletedAt != nil &&
			wire.ReasonCode == "" {
			details.State = FunctionSucceeded{CompletedAt: *env.CompletedAt, CatalogRevision: wire.CatalogRevision}
			break
		}
		state, err := failedFunctionFromWire(env, wire)
		if err != nil {
			return nil, err
		}
		details.State = state
	case executionInterrupted:
		if env.CompletedAt == nil || wire.ReasonCode == "" || wire.CatalogRevision != "" ||
			wire.InvocationOutcome != "" {
			return nil, invalid("invalid interrupted Function")
		}
		details.State = FunctionInterrupted{CompletedAt: *env.CompletedAt, ReasonCode: wire.ReasonCode}
	default:
		return nil, invalid("invalid Function phase")
	}
	return details, nil
}

func failedFunctionFromWire(env StepExecutionEnvelope, wire functionWire) (FunctionTerminalState, error) {
	if env.CompletedAt == nil || wire.ReasonCode == "" {
		return nil, invalid("invalid failed Function")
	}
	if wire.InvocationOutcome == "" {
		if wire.CatalogRevision != "" {
			return nil, invalid("admission rejection has catalog")
		}
		return FunctionFailed{CompletedAt: *env.CompletedAt,
			Evidence: FunctionAdmissionRejected{ReasonCode: wire.ReasonCode}}, nil
	}
	outcome, err := functionFailureOutcomeFromWire(wire)
	if err != nil {
		return nil, err
	}
	return FunctionFailed{CompletedAt: *env.CompletedAt,
		Evidence: FunctionInvocationFailed{CatalogRevision: wire.CatalogRevision, Outcome: outcome}}, nil
}

func functionFailureOutcomeToWire(wire functionWire, evidence adapters.Evidence) (functionWire, error) {
	switch outcome := evidence.(type) {
	case adapters.Succeeded:
		return wire, invalid("invalid failed Function outcome")
	case adapters.Failed:
		wire.InvocationOutcome = executionFailed
		wire.ReasonCode = outcome.ReasonCode
	case adapters.NotExecuted:
		wire.InvocationOutcome = functionOutcomeNotExecuted
		wire.ReasonCode = outcome.ReasonCode
	case adapters.Unknown:
		wire.InvocationOutcome = string(BranchUnknown)
		wire.ReasonCode = outcome.ReasonCode
	default:
		return wire, invalid("invalid failed Function outcome")
	}
	return wire, nil
}

func functionFailureOutcomeFromWire(wire functionWire) (adapters.Evidence, error) {
	switch wire.InvocationOutcome {
	case executionFailed:
		return adapters.Failed{ReasonCode: wire.ReasonCode}, nil
	case functionOutcomeNotExecuted:
		return adapters.NotExecuted{ReasonCode: wire.ReasonCode}, nil
	case string(BranchUnknown):
		return adapters.Unknown{ReasonCode: wire.ReasonCode}, nil
	default:
		return nil, invalid("invalid invocation outcome")
	}
}
