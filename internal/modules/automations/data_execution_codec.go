package automations

import (
	"encoding/json"
	"time"
)

func decodeDataExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if env.Phase != executionSucceeded || env.CompletedAt == nil || !env.CompletedAt.Equal(env.StartedAt) {
		return nil, invalid("data Step must be instantaneous success")
	}
	switch env.Kind {
	case StepKindSetVariables:
		var wire struct {
			Values json.RawMessage `json:"values"`
		}
		if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
			return nil, err
		}
		return VariablesExecutionDetails{At: env.StartedAt, Values: wire.Values}, nil
	case StepKindReadState:
		var wire struct {
			Output         json.RawMessage         `json:"output"`
			ReadAt         time.Time               `json:"read_at"`
			Observation    *ObservationEvidence    `json:"observation,omitempty"`
			UnusableReason *ConditionUnknownReason `json:"unusable_reason,omitempty"`
		}
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
	default:
		return nil, invalid("invalid data Step kind")
	}
}
