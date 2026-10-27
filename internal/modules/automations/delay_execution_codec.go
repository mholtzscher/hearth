package automations

type delayWire struct {
	ReasonCode string `json:"reason_code,omitempty"`
}

func (details DelayExecutionDetails) encodeValidated() (any, error) {
	wire := delayWire{}
	switch state := details.State.(type) {
	case DelayRunning, DelaySucceeded:
	case DelayInterrupted:
		wire.ReasonCode = state.FailureCode
	default:
		return nil, invalid("unsupported Delay execution state %T", details.State)
	}
	return wire, nil
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
	return DelayExecutionDetails{StartedAt: env.StartedAt, State: state}, nil
}
