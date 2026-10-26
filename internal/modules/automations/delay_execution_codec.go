package automations

type delayWire struct {
	ReasonCode string `json:"reason_code,omitempty"`
}

func encodeDelayExecution(details DelayExecutionDetails) (StepExecutionEnvelope, any, error) {
	completion, err := DelayStateCompletion(details.State)
	if err != nil {
		return StepExecutionEnvelope{}, nil, err
	}
	env := StepExecutionEnvelope{
		Kind:        StepKindDelay,
		StartedAt:   details.StartedAt,
		CompletedAt: completion,
	}
	wire := delayWire{}
	switch state := details.State.(type) {
	case DelayRunning:
		env.Phase = executionRunning
	case DelaySucceeded:
		env.Phase = executionSucceeded
	case DelayInterrupted:
		env.Phase = executionInterrupted
		wire.ReasonCode = state.FailureCode
	default:
		return StepExecutionEnvelope{}, nil, invalid("unsupported Delay execution state %T", details.State)
	}
	return env, wire, nil
}

func decodeDelayExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	var wire delayWire
	if err := decodeWire(env.EvidenceJSON, &wire); err != nil {
		return DelayExecutionDetails{}, err
	}
	var state DelayExecutionState
	switch env.Phase {
	case executionRunning:
		if env.CompletedAt != nil || wire.ReasonCode != "" {
			return DelayExecutionDetails{}, invalid("invalid running Delay shape")
		}
		state = DelayRunning{}
	case executionSucceeded:
		if env.CompletedAt == nil || wire.ReasonCode != "" {
			return DelayExecutionDetails{}, invalid("invalid successful Delay shape")
		}
		state = DelaySucceeded{CompletedAt: *env.CompletedAt}
	case executionInterrupted:
		if env.CompletedAt == nil || wire.ReasonCode == "" {
			return DelayExecutionDetails{}, invalid("invalid interrupted Delay shape")
		}
		state = DelayInterrupted{CompletedAt: *env.CompletedAt, FailureCode: wire.ReasonCode}
	default:
		return DelayExecutionDetails{}, invalid("invalid Delay phase")
	}
	if err := ValidateDelayExecutionState(state); err != nil {
		return DelayExecutionDetails{}, err
	}
	return DelayExecutionDetails{StartedAt: env.StartedAt, State: state}, nil
}
