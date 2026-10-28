package automations

// PrepareStepExecutionDetails validates reached evidence and returns its details and
// encoded envelope for one repository write. It does not persist anything.
func PrepareStepExecutionDetails(record StepExecutionRecord) (StepExecutionDetails, StepExecutionEnvelope, error) {
	if _, err := ParseRunID(string(record.RunID)); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if _, err := ParseStepID(string(record.StepID)); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	var details StepExecutionDetails
	switch e := record.Evidence.(type) {
	case CommandStarted:
		details = CommandExecutionDetails{
			StartedAt:  e.StartedAt,
			State:      CommandRunning{Reservation: e.Reservation},
			Parameters: e.Parameters,
		}
	case DelayStarted:
		details = DelayExecutionDetails{StartedAt: e.StartedAt, State: DelayRunning{}}
	case NotificationStarted:
		details = NotificationExecutionDetails{
			StartedAt: e.StartedAt,
			AttemptID: e.AttemptID,
			State:     NotificationRunning{},
			Message:   e.Message,
		}
	case FunctionStarted:
		details = FunctionExecutionDetails{
			StartedAt:    e.StartedAt,
			InvocationID: e.InvocationID,
			State:        FunctionRunning{},
			Arguments:    e.Arguments,
		}
	case BranchDecided:
		d := e.Decision
		if d.StepID != record.StepID {
			return nil, StepExecutionEnvelope{}, invalid("branch decision Step identity mismatch")
		}
		details = BranchExecutionDetails{Decision: d, ValueFailure: e.ValueFailure}
	case ValuesAssigned:
		details = VariablesExecutionDetails(e)
	case StateRead:
		details = StateExecutionDetails(e)
	case ValueResolutionFailed:
		details = ValueFailureExecutionDetails{StepKind: e.Kind, At: e.At, Failure: e.Failure}
	case CommandStopped:
		details = CommandExecutionDetails{StartedAt: e.At,
			State: CommandInterrupted{CompletedAt: e.At, FailureCode: e.FailureCode}}
	default:
		return nil, StepExecutionEnvelope{}, invalid("unsupported reached evidence %T", record.Evidence)
	}
	envelope, err := EncodeStepExecutionDetails(details)
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	return details, envelope, nil
}

// CompleteStepDetails validates and applies terminal evidence without changing the
// reservation, returning the completed details and encoded envelope for one write.
func CompleteStepDetails(
	details StepExecutionDetails,
	completion StepExecutionCompletion,
) (StepExecutionDetails, StepExecutionEnvelope, error) {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	if err := validateStepExecutionDetails(details); err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	var completed StepExecutionDetails
	var err error
	switch b := details.(type) {
	case CommandExecutionDetails:
		completed, err = completeCommandDetails(b, completion.Evidence)
	case DelayExecutionDetails:
		completed, err = completeDelayDetails(b, completion.Evidence)
	case NotificationExecutionDetails:
		completed, err = completeNotificationDetails(b, completion.Evidence)
	case FunctionExecutionDetails:
		completed, err = completeFunctionDetails(b, completion.Evidence)
	case BranchExecutionDetails:
		return nil, StepExecutionEnvelope{}, invalid("branch selection is insert-once")
	case StateExecutionDetails, ValueFailureExecutionDetails, VariablesExecutionDetails:
		return nil, StepExecutionEnvelope{}, invalid("data execution is insert-once")
	default:
		return nil, StepExecutionEnvelope{}, invalid("execution details are not completable")
	}
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	envelope, err := EncodeStepExecutionDetails(completed)
	if err != nil {
		return nil, StepExecutionEnvelope{}, err
	}
	return completed, envelope, nil
}

func completeCommandDetails(
	b CommandExecutionDetails,
	evidence CompletedStepEvidence,
) (CommandExecutionDetails, error) {
	c, ok := evidence.(CommandFinished)
	running, active := b.State.(CommandRunning)
	if !ok || !active || c.State == nil {
		return CommandExecutionDetails{}, invalid("command completion reservation mismatch")
	}
	reservation := commandTerminalReservation(c.State)
	if reservation == nil || *reservation != running.Reservation {
		return CommandExecutionDetails{}, invalid("command completion reservation mismatch")
	}
	b.State = c.State
	return b, nil
}

func commandTerminalReservation(state CommandTerminalState) *CommandReservation {
	switch state := state.(type) {
	case CommandSucceeded:
		return &state.Reservation
	case CommandFailed:
		return &state.Reservation
	case CommandInterrupted:
		return state.Reservation
	default:
		return nil
	}
}

func completeDelayDetails(b DelayExecutionDetails, evidence CompletedStepEvidence) (DelayExecutionDetails, error) {
	c, ok := evidence.(DelayFinished)
	_, active := b.State.(DelayRunning)
	if !ok || !active || c.State == nil {
		return DelayExecutionDetails{}, invalid("delay completion requires running delay")
	}
	b.State = c.State
	return b, nil
}

func completeNotificationDetails(
	b NotificationExecutionDetails,
	evidence CompletedStepEvidence,
) (NotificationExecutionDetails, error) {
	c, ok := evidence.(NotificationFinished)
	_, active := b.State.(NotificationRunning)
	if !ok || !active || c.State == nil || c.AttemptID != b.AttemptID {
		return NotificationExecutionDetails{}, invalid("notification reservation mismatch")
	}
	b.State = c.State
	return b, nil
}

func completeFunctionDetails(
	b FunctionExecutionDetails,
	evidence CompletedStepEvidence,
) (FunctionExecutionDetails, error) {
	c, ok := evidence.(FunctionFinished)
	_, active := b.State.(FunctionRunning)
	if !ok || !active || c.State == nil || c.InvocationID != b.InvocationID {
		return FunctionExecutionDetails{}, invalid("function reservation mismatch")
	}
	b.State = c.State
	b.Output = c.Output
	b.ValueFailure = c.ValueFailure
	return b, nil
}
