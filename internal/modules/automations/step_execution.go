package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// Native lifecycle labels are projections of the execution variants, not stored Go discriminators.
const (
	executionRunning     = "running"
	executionSucceeded   = "succeeded"
	executionFailed      = "failed"
	executionInterrupted = "interrupted"
)

// InitialStepEvidence is the closed set of durable first-reach records.
//
//sumtype:decl
type InitialStepEvidence interface{ isInitialStepEvidence() }

type CommandStarted struct {
	StartedAt   time.Time
	Reservation CommandReservation
}
type DelayStarted struct{ StartedAt time.Time }
type NotificationStarted struct {
	StartedAt time.Time
	AttemptID notifications.AttemptID
}
type FunctionStarted struct {
	StartedAt    time.Time
	InvocationID adapters.InvocationID
}
type BranchDecided struct{ Decision BranchDecision }

// CommandStopped records an interruption before a Command identity was reserved.
type CommandStopped struct {
	At          time.Time
	FailureCode string
}

func (CommandStarted) isInitialStepEvidence()      {}
func (DelayStarted) isInitialStepEvidence()        {}
func (NotificationStarted) isInitialStepEvidence() {}
func (FunctionStarted) isInitialStepEvidence()     {}
func (BranchDecided) isInitialStepEvidence()       {}
func (CommandStopped) isInitialStepEvidence()      {}

type StepExecutionRecord struct {
	RunID    RunID
	StepID   StepID
	Evidence InitialStepEvidence
}

// CompletedStepEvidence is the closed set of completion evidence accepted by CompleteStep.
//
//sumtype:decl
type CompletedStepEvidence interface{ isCompletedStepEvidence() }
type CommandFinished struct {
	State CommandTerminalState
}
type DelayFinished struct {
	State DelayTerminalState
}
type NotificationFinished struct {
	AttemptID notifications.AttemptID
	State     NotificationTerminalState
}
type FunctionFinished struct {
	InvocationID adapters.InvocationID
	State        FunctionTerminalState
}

func (CommandFinished) isCompletedStepEvidence()      {}
func (DelayFinished) isCompletedStepEvidence()        {}
func (NotificationFinished) isCompletedStepEvidence() {}
func (FunctionFinished) isCompletedStepEvidence()     {}

type StepExecutionCompletion struct {
	RunID    RunID
	StepID   StepID
	Evidence CompletedStepEvidence
}

// StepExecutionDetails is a typed lifecycle variant; kind and phase are derived from it.
//
//sumtype:decl
type StepExecutionDetails interface{ isStepExecutionDetails() }
type CommandExecutionDetails struct {
	StartedAt time.Time
	State     CommandExecutionState
}
type DelayExecutionDetails struct {
	StartedAt time.Time
	State     DelayExecutionState
}
type NotificationExecutionDetails struct {
	StartedAt time.Time
	AttemptID notifications.AttemptID
	State     NotificationExecutionState
}
type FunctionExecutionDetails struct {
	StartedAt    time.Time
	InvocationID adapters.InvocationID
	State        FunctionExecutionState
}
type BranchExecutionDetails struct{ Decision BranchDecision }

func (CommandExecutionDetails) isStepExecutionDetails()      {}
func (DelayExecutionDetails) isStepExecutionDetails()        {}
func (NotificationExecutionDetails) isStepExecutionDetails() {}
func (FunctionExecutionDetails) isStepExecutionDetails()     {}
func (BranchExecutionDetails) isStepExecutionDetails()       {}

type StepExecution struct {
	StepID   StepID
	Sequence int
	Details  StepExecutionDetails
}

// RecordStepDetails validates a reached record and converts it to the canonical trace value.
func RecordStepDetails(record StepExecutionRecord) (StepExecutionDetails, error) {
	if _, err := ParseRunID(string(record.RunID)); err != nil {
		return nil, err
	}
	if _, err := ParseStepID(string(record.StepID)); err != nil {
		return nil, err
	}
	var details StepExecutionDetails
	switch e := record.Evidence.(type) {
	case CommandStarted:
		details = CommandExecutionDetails{StartedAt: e.StartedAt, State: CommandRunning{Reservation: e.Reservation}}
	case DelayStarted:
		details = DelayExecutionDetails{StartedAt: e.StartedAt, State: DelayRunning{}}
	case NotificationStarted:
		details = NotificationExecutionDetails{
			StartedAt: e.StartedAt,
			AttemptID: e.AttemptID,
			State:     NotificationRunning{},
		}
	case FunctionStarted:
		details = FunctionExecutionDetails{
			StartedAt:    e.StartedAt,
			InvocationID: e.InvocationID,
			State:        FunctionRunning{},
		}
	case BranchDecided:
		d := e.Decision
		if d.StepID != record.StepID {
			return nil, invalid("branch decision Step identity mismatch")
		}
		if err := validateBranchDecisionShape(d); err != nil {
			return nil, err
		}
		details = BranchExecutionDetails{Decision: d}
	case CommandStopped:
		if !isValidExecutionTimestamp(e.At) || !isInterruptionCode(e.FailureCode) {
			return nil, invalid("invalid pre-reservation command interruption")
		}
		details = CommandExecutionDetails{StartedAt: e.At,
			State: CommandInterrupted{CompletedAt: e.At, FailureCode: e.FailureCode}}
	default:
		return nil, invalid("unsupported reached evidence %T", record.Evidence)
	}
	if _, err := EncodeStepExecutionDetails(details); err != nil {
		return nil, err
	}
	return details, nil
}

// CompleteStepDetails validates and applies terminal evidence without changing the reservation.
func CompleteStepDetails(
	details StepExecutionDetails,
	completion StepExecutionCompletion,
) (StepExecutionDetails, error) {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return nil, err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return nil, err
	}
	if _, err := EncodeStepExecutionDetails(details); err != nil {
		return nil, err
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
		return nil, invalid("branch selection is insert-once")
	default:
		return nil, invalid("execution details are not completable")
	}
	if err != nil {
		return nil, err
	}
	if _, err = EncodeStepExecutionDetails(completed); err != nil {
		return nil, err
	}
	return completed, nil
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
	return b, nil
}

func executionDetailsKind(details StepExecutionDetails) StepKind {
	switch details := details.(type) {
	case CommandExecutionDetails:
		return StepKindCommand
	case DelayExecutionDetails:
		return StepKindDelay
	case NotificationExecutionDetails:
		return StepKindNotification
	case FunctionExecutionDetails:
		return StepKindFunction
	case BranchExecutionDetails:
		return details.Decision.Kind()
	default:
		return ""
	}
}

// StepExecutionRunFailure derives the Run failure or interruption from typed execution details.
func StepExecutionRunFailure(details StepExecutionDetails) (RunOutcome, bool) {
	switch details := details.(type) {
	case CommandExecutionDetails:
		switch outcome := details.State.(type) {
		case CommandFailed:
			return FailedRun{FailureCode: outcome.FailureCode}, true
		case CommandInterrupted:
			return InterruptedRun{FailureCode: outcome.FailureCode}, true
		case CommandRunning, CommandSucceeded:
			return nil, false
		default:
			return nil, false
		}
	case DelayExecutionDetails:
		if state, interrupted := details.State.(DelayInterrupted); interrupted {
			return InterruptedRun{FailureCode: state.FailureCode}, true
		}
	case NotificationExecutionDetails:
		switch state := details.State.(type) {
		case NotificationInterrupted:
			return InterruptedRun{FailureCode: state.ReasonCode}, true
		case NotificationFailed:
			switch evidence := state.Evidence.(type) {
			case NotificationAdmissionRejected:
				return FailedRun{FailureCode: evidence.ReasonCode}, true
			case NotificationAttemptFailed:
				return FailedRun{FailureCode: evidence.ReasonCode}, true
			default:
				return nil, false
			}
		case NotificationRunning, NotificationSucceeded:
			return nil, false
		default:
			return nil, false
		}
	case FunctionExecutionDetails:
		switch state := details.State.(type) {
		case FunctionFailed:
			return FailedRun{FailureCode: functionFailureCode(state.Evidence)}, true
		case FunctionInterrupted:
			return InterruptedRun{FailureCode: state.ReasonCode}, true
		case FunctionRunning, FunctionSucceeded:
			return nil, false
		default:
			return nil, false
		}
	case BranchExecutionDetails:
		if code := details.Decision.FailureCode(); code != nil {
			return FailedRun{FailureCode: *code}, true
		}
	default:
		return nil, false
	}
	return nil, false
}

func functionFailureCode(failure FunctionFailure) string {
	switch failure := failure.(type) {
	case FunctionAdmissionRejected:
		return failure.ReasonCode
	case FunctionInvocationFailed:
		switch evidence := failure.Outcome.(type) {
		case adapters.Failed:
			return evidence.ReasonCode
		case adapters.NotExecuted:
			return evidence.ReasonCode
		case adapters.Unknown:
			return evidence.ReasonCode
		case adapters.Succeeded:
			return FailureInternalError
		default:
			return FailureInternalError
		}
	default:
		return FailureInternalError
	}
}

func isInterruptionCode(code string) bool {
	return code == FailureCoreStopping || code == FailureCoreRestarted || code == FailureExecutorFault
}

func isValidExecutionTimestamp(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0
}
