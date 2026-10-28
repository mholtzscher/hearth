package automations

import (
	"encoding/json"
	"time"
)

type variablesExecutionWire struct {
	Values json.RawMessage `json:"values"`
}

type stateExecutionWire struct {
	Output         json.RawMessage         `json:"output"`
	ReadAt         time.Time               `json:"read_at"`
	Observation    *ObservationEvidence    `json:"observation,omitempty"`
	UnusableReason *ConditionUnknownReason `json:"unusable_reason,omitempty"`
}

func (details VariablesExecutionDetails) encodeValidated() (any, error) {
	return variablesExecutionWire{Values: details.Values}, nil
}

func (details StateExecutionDetails) encodeValidated() (any, error) {
	return stateExecutionWire{
		Output: details.Output, ReadAt: details.ReadAt,
		Observation: details.Observation, UnusableReason: details.UnusableReason,
	}, nil
}

func (details ValueFailureExecutionDetails) encodeValidated() (any, error) {
	return struct {
		Failure ValueFailure `json:"value_failure"`
	}{details.Failure}, nil
}

func decodeDataExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if env.Phase != executionSucceeded || env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("data Step must be instantaneous success")
	}
	switch env.Kind {
	case StepKindSetVariables:
		var wire variablesExecutionWire
		if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
			return nil, err
		}
		return VariablesExecutionDetails{At: env.StartedAt, Values: wire.Values}, nil
	case StepKindReadState:
		var wire stateExecutionWire
		if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
			return nil, err
		}
		return StateExecutionDetails{
			At:             env.StartedAt,
			Output:         wire.Output,
			ReadAt:         wire.ReadAt,
			Observation:    wire.Observation,
			UnusableReason: wire.UnusableReason,
		}, nil
	case StepKindCommand, StepKindNotification, StepKindFunction, StepKindIf, StepKindChoose, StepKindDelay:
		return nil, invalid("invalid data Step kind")
	}
	return nil, invalid("unknown data Step kind")
}
