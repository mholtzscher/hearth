package automations

import "github.com/mholtzscher/hearth/internal/modules/devices"

// encodeCommandExecution preserves the durable Command evidence wire shape.
func encodeCommandExecution(details CommandExecutionDetails) (StepExecutionEnvelope, any, error) {
	env := StepExecutionEnvelope{Kind: StepKindCommand, StartedAt: details.StartedAt}
	completed, err := CommandStateCompletion(details.State)
	if err != nil {
		return env, nil, err
	}
	env.CompletedAt = completed
	w, err := commandStateToWire(details.State)
	if err != nil {
		return env, nil, err
	}
	switch details.State.(type) {
	case CommandRunning:
		env.Phase = executionRunning
	case CommandSucceeded:
		env.Phase = executionSucceeded
	case CommandFailed:
		env.Phase = executionFailed
	case CommandInterrupted:
		env.Phase = executionInterrupted
	default:
		return env, nil, invalid("invalid Command execution state %T", details.State)
	}
	return env, w, nil
}

func commandStateToWire(state CommandExecutionState) (commandWire, error) {
	w := commandWire{}
	if err := ValidateCommandExecutionState(state); err != nil {
		return w, err
	}
	var reservation *CommandReservation
	switch s := state.(type) {
	case CommandRunning:
		reservation = &s.Reservation
	case CommandSucceeded:
		reservation = &s.Reservation
	case CommandFailed:
		reservation = &s.Reservation
	case CommandInterrupted:
		reservation = s.Reservation
	}
	if reservation != nil {
		w.CommandID = &reservation.CommandID
		w.CorrelationID = &reservation.CorrelationID
	}
	switch s := state.(type) {
	case CommandRunning:
		// Reservation is already encoded above; running states have no terminal evidence.
	case CommandSucceeded:
		switch outcome := s.Outcome.(type) {
		case SatisfiedCommand:
			w.CommandOutcome = "satisfied"
			w.VerifiedCommandID = &outcome.VerifiedCommandID
		case DispatchedCommand:
			w.CommandOutcome = "dispatched"
			w.VerifiedCommandID = &outcome.VerifiedCommandID
		}
	case CommandFailed:
		w.ReasonCode = s.FailureCode
		w.VerifiedCommandID = s.VerifiedCommandID
	case CommandInterrupted:
		w.ReasonCode = s.FailureCode
		w.VerifiedCommandID = s.VerifiedCommandID
	}
	return w, nil
}

// decodeCommandExecution strictly decodes one Command payload and independently validates its state.
func decodeCommandExecution(env StepExecutionEnvelope) (StepExecutionDetails, error) {
	if err := validateCommandExecutionEnvelope(env); err != nil {
		return nil, err
	}
	var w commandWire
	if err := decodeWire(env.EvidenceJSON, &w); err != nil {
		return nil, err
	}
	reservation, err := commandReservationFromWire(w)
	if err != nil {
		return nil, err
	}
	if err = validateCommandWireVerifiedID(w, reservation); err != nil {
		return nil, err
	}
	state, err := commandStateFromWire(env, w, reservation)
	if err != nil {
		return nil, err
	}
	if err = ValidateCommandExecutionState(state); err != nil {
		return nil, err
	}
	completed, err := CommandStateCompletion(state)
	if err != nil {
		return nil, err
	}
	if (completed == nil) != (env.CompletedAt == nil) || completed != nil && !completed.Equal(*env.CompletedAt) {
		return nil, invalid("Command state completion disagrees with envelope")
	}
	return CommandExecutionDetails{StartedAt: env.StartedAt, State: state}, nil
}

func validateCommandExecutionEnvelope(env StepExecutionEnvelope) error {
	if env.Kind != StepKindCommand || !isValidExecutionTimestamp(env.StartedAt) ||
		env.CompletedAt != nil && !isValidExecutionTimestamp(*env.CompletedAt) {
		return invalid("invalid Command execution envelope")
	}
	if env.Phase != executionRunning && env.CompletedAt == nil {
		return invalid("terminal Command requires completion time")
	}
	return nil
}

func commandReservationFromWire(w commandWire) (*CommandReservation, error) {
	if (w.CommandID == nil) != (w.CorrelationID == nil) {
		return nil, invalid("partial Command reservation")
	}
	if w.CommandID == nil {
		return nil, nil //nolint:nilnil // Pre-reservation interruption carries no reservation.
	}
	if _, err := devices.ParseCommandID(string(*w.CommandID)); err != nil {
		return nil, err
	}
	if _, err := devices.ParseCorrelationID(string(*w.CorrelationID)); err != nil {
		return nil, err
	}
	reservation := CommandReservation{CommandID: *w.CommandID, CorrelationID: *w.CorrelationID}
	return &reservation, nil
}

func validateCommandWireVerifiedID(w commandWire, reservation *CommandReservation) error {
	if w.VerifiedCommandID == nil {
		return nil
	}
	if _, err := devices.ParseCommandID(string(*w.VerifiedCommandID)); err != nil {
		return err
	}
	if reservation == nil || *w.VerifiedCommandID != reservation.CommandID {
		return invalid("verified Command does not match reservation")
	}
	return nil
}

func commandStateFromWire(
	env StepExecutionEnvelope,
	w commandWire,
	reservation *CommandReservation,
) (CommandExecutionState, error) {
	switch env.Phase {
	case executionRunning:
		return runningCommandFromWire(w, reservation)
	case executionSucceeded:
		return successfulCommandFromWire(env, w, reservation)
	case executionFailed:
		return failedCommandFromWire(env, w, reservation)
	case executionInterrupted:
		return interruptedCommandFromWire(env, w, reservation)
	default:
		return nil, invalid("invalid Command phase")
	}
}

func runningCommandFromWire(w commandWire, reservation *CommandReservation) (CommandExecutionState, error) {
	if reservation == nil || w.VerifiedCommandID != nil || w.CommandOutcome != "" || w.ReasonCode != "" {
		return nil, invalid("invalid running Command shape")
	}
	return CommandRunning{Reservation: *reservation}, nil
}

func successfulCommandFromWire(
	env StepExecutionEnvelope,
	w commandWire,
	reservation *CommandReservation,
) (CommandExecutionState, error) {
	if reservation == nil || w.VerifiedCommandID == nil || w.ReasonCode != "" {
		return nil, invalid("successful Command requires verified link")
	}
	state := CommandSucceeded{CompletedAt: *env.CompletedAt, Reservation: *reservation}
	switch w.CommandOutcome {
	case "satisfied":
		state.Outcome = SatisfiedCommand{VerifiedCommandID: *w.VerifiedCommandID}
	case "dispatched":
		state.Outcome = DispatchedCommand{VerifiedCommandID: *w.VerifiedCommandID}
	default:
		return nil, invalid("invalid successful Command outcome")
	}
	return state, nil
}

func failedCommandFromWire(
	env StepExecutionEnvelope,
	w commandWire,
	reservation *CommandReservation,
) (CommandExecutionState, error) {
	if reservation == nil || w.ReasonCode == "" || w.CommandOutcome != "" {
		return nil, invalid("invalid failed Command shape")
	}
	return CommandFailed{
		CompletedAt: *env.CompletedAt, Reservation: *reservation,
		FailureCode: w.ReasonCode, VerifiedCommandID: w.VerifiedCommandID,
	}, nil
}

func interruptedCommandFromWire(
	env StepExecutionEnvelope,
	w commandWire,
	reservation *CommandReservation,
) (CommandExecutionState, error) {
	if w.ReasonCode == "" || w.CommandOutcome != "" {
		return nil, invalid("invalid interrupted Command shape")
	}
	return CommandInterrupted{
		CompletedAt: *env.CompletedAt, Reservation: reservation,
		FailureCode: w.ReasonCode, VerifiedCommandID: w.VerifiedCommandID,
	}, nil
}
