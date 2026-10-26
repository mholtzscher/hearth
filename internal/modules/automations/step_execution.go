package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/devices"
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
	CompletedAt time.Time
	Reservation CommandReservation
	Outcome     CommandOutcome
}
type DelayFinished struct {
	CompletedAt time.Time
	FailureCode *string
}
type NotificationFinished struct {
	CompletedAt time.Time
	AttemptID   notifications.AttemptID
	Outcome     *notifications.Outcome
	ReasonCode  *string
}
type FunctionFinished struct {
	CompletedAt  time.Time
	InvocationID adapters.InvocationID
	State        FunctionExecutionState
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
	StartedAt   time.Time
	CompletedAt *time.Time
	Reservation *CommandReservation
	Outcome     CommandOutcome
}
type DelayExecutionDetails struct {
	StartedAt   time.Time
	CompletedAt *time.Time
	FailureCode *string
}
type NotificationExecutionDetails struct {
	StartedAt   time.Time
	CompletedAt *time.Time
	AttemptID   notifications.AttemptID
	Outcome     *notifications.Outcome
	ReasonCode  *string
}
type FunctionExecutionDetails struct {
	StartedAt    time.Time
	CompletedAt  *time.Time
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
	switch e := record.Evidence.(type) {
	case CommandStarted:
		return commandStartedDetails(e)
	case DelayStarted:
		if !validDelayTimestamp(e.StartedAt) {
			return nil, invalid("invalid delay start time")
		}
		return DelayExecutionDetails{StartedAt: e.StartedAt}, nil
	case NotificationStarted:
		if err := ValidateNotificationStart(
			NotificationStart{
				RunID:     record.RunID,
				StepID:    record.StepID,
				AttemptID: e.AttemptID,
				StartedAt: e.StartedAt,
			},
		); err != nil {
			return nil, err
		}
		return NotificationExecutionDetails{StartedAt: e.StartedAt, AttemptID: e.AttemptID}, nil
	case FunctionStarted:
		if err := ValidateFunctionStart(
			FunctionStart{
				RunID:        record.RunID,
				StepID:       record.StepID,
				InvocationID: e.InvocationID,
				StartedAt:    e.StartedAt,
			},
		); err != nil {
			return nil, err
		}
		return FunctionExecutionDetails{
			StartedAt:    e.StartedAt,
			InvocationID: e.InvocationID,
			State:        FunctionRunning{},
		}, nil
	case BranchDecided:
		d := e.Decision
		if d.StepID != record.StepID {
			return nil, invalid("branch decision Step identity mismatch")
		}
		if err := validateBranchDecisionShape(d); err != nil {
			return nil, err
		}
		return BranchExecutionDetails{Decision: d}, nil
	case CommandStopped:
		if !validDelayTimestamp(e.At) || !isInterruptionCode(e.FailureCode) {
			return nil, invalid("invalid pre-reservation command interruption")
		}
		return CommandExecutionDetails{
			StartedAt:   e.At,
			CompletedAt: &e.At,
			Outcome:     InterruptedCommand{FailureCode: e.FailureCode},
		}, nil
	default:
		return nil, invalid("unsupported reached evidence %T", record.Evidence)
	}
}

func commandStartedDetails(start CommandStarted) (CommandExecutionDetails, error) {
	if !validDelayTimestamp(start.StartedAt) {
		return CommandExecutionDetails{}, invalid("invalid Command start time")
	}
	if _, err := devices.ParseCommandID(string(start.Reservation.CommandID)); err != nil {
		return CommandExecutionDetails{}, err
	}
	if _, err := devices.ParseCorrelationID(string(start.Reservation.CorrelationID)); err != nil {
		return CommandExecutionDetails{}, err
	}
	return CommandExecutionDetails{StartedAt: start.StartedAt, Reservation: &start.Reservation}, nil
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
	switch b := details.(type) {
	case CommandExecutionDetails:
		return completeCommandDetails(b, completion.Evidence)
	case DelayExecutionDetails:
		return completeDelayDetails(b, completion)
	case NotificationExecutionDetails:
		return completeNotificationDetails(b, completion)
	case FunctionExecutionDetails:
		return completeFunctionDetails(b, completion)
	case BranchExecutionDetails:
		return nil, invalid("branch selection is insert-once")
	default:
		return nil, invalid("execution body is not completable")
	}
}

func completeCommandDetails(
	b CommandExecutionDetails,
	evidence CompletedStepEvidence,
) (CommandExecutionDetails, error) {
	c, ok := evidence.(CommandFinished)
	if !ok || b.CompletedAt != nil || b.Reservation == nil || *b.Reservation != c.Reservation {
		return CommandExecutionDetails{}, invalid("command completion reservation mismatch")
	}
	at := c.CompletedAt
	if !validDelayTimestamp(at) {
		return CommandExecutionDetails{}, invalid("invalid command completion time")
	}
	if err := ValidateStepCompletion(StepCompletion{Outcome: c.Outcome}); err != nil {
		return CommandExecutionDetails{}, err
	}
	verified, _ := CommandOutcomeEvidence(c.Outcome)
	if verified != nil && *verified != c.Reservation.CommandID {
		return CommandExecutionDetails{}, invalid("verified Command disagrees with reservation")
	}
	b.CompletedAt = &at
	b.Outcome = c.Outcome
	return b, nil
}

func completeDelayDetails(b DelayExecutionDetails, completion StepExecutionCompletion) (DelayExecutionDetails, error) {
	c, ok := completion.Evidence.(DelayFinished)
	if !ok || b.CompletedAt != nil {
		return DelayExecutionDetails{}, invalid("delay completion requires running delay")
	}
	at := c.CompletedAt
	status := DelayCompleted
	if c.FailureCode != nil {
		status = DelayInterrupted
	}
	if err := ValidateDelayCompletion(
		DelayCompletion{
			RunID:       completion.RunID,
			StepID:      completion.StepID,
			Status:      status,
			CompletedAt: at,
			FailureCode: c.FailureCode,
		},
	); err != nil {
		return DelayExecutionDetails{}, err
	}
	b.CompletedAt = &at
	b.FailureCode = c.FailureCode
	return b, nil
}

func completeNotificationDetails(
	b NotificationExecutionDetails,
	completion StepExecutionCompletion,
) (NotificationExecutionDetails, error) {
	c, ok := completion.Evidence.(NotificationFinished)
	if !ok || b.CompletedAt != nil || c.AttemptID != b.AttemptID {
		return NotificationExecutionDetails{}, invalid("notification reservation mismatch")
	}
	at := c.CompletedAt
	status := NotificationFailed
	if c.Outcome != nil && *c.Outcome == notifications.Accepted {
		status = NotificationAccepted
	}
	if c.Outcome == nil && c.ReasonCode != nil && isInterruptionCode(*c.ReasonCode) {
		status = NotificationInterrupted
	}
	if err := ValidateNotificationCompletion(
		NotificationCompletion{
			RunID:       completion.RunID,
			StepID:      completion.StepID,
			AttemptID:   c.AttemptID,
			Status:      status,
			CompletedAt: at,
			Outcome:     c.Outcome,
			ReasonCode:  c.ReasonCode,
		},
	); err != nil {
		return NotificationExecutionDetails{}, err
	}
	b.CompletedAt = &at
	b.Outcome = c.Outcome
	b.ReasonCode = c.ReasonCode
	return b, nil
}

func completeFunctionDetails(
	b FunctionExecutionDetails,
	completion StepExecutionCompletion,
) (FunctionExecutionDetails, error) {
	c, ok := completion.Evidence.(FunctionFinished)
	if !ok || b.CompletedAt != nil || c.InvocationID != b.InvocationID {
		return FunctionExecutionDetails{}, invalid("function reservation mismatch")
	}
	if err := ValidateFunctionCompletion(
		FunctionCompletion{
			RunID:        completion.RunID,
			StepID:       completion.StepID,
			InvocationID: c.InvocationID,
			State:        c.State,
		},
	); err != nil {
		return FunctionExecutionDetails{}, err
	}
	at := FunctionTerminalTime(c.State)
	if at == nil {
		return FunctionExecutionDetails{}, invalid("function completion is running")
	}
	if !c.CompletedAt.Equal(*at) {
		return FunctionExecutionDetails{}, invalid("Function completion timestamps disagree")
	}
	b.CompletedAt = at
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
		switch outcome := details.Outcome.(type) {
		case FailedCommand:
			return FailedRun{FailureCode: outcome.FailureCode}, true
		case InterruptedCommand:
			return InterruptedRun{FailureCode: outcome.FailureCode}, true
		case SatisfiedCommand, DispatchedCommand:
			return nil, false
		default:
			return nil, false
		}
	case DelayExecutionDetails:
		if details.FailureCode != nil {
			return InterruptedRun{FailureCode: *details.FailureCode}, true
		}
	case NotificationExecutionDetails:
		if details.CompletedAt != nil && details.ReasonCode != nil {
			if details.Outcome == nil && isInterruptionCode(*details.ReasonCode) {
				return InterruptedRun{FailureCode: *details.ReasonCode}, true
			}
			return FailedRun{FailureCode: *details.ReasonCode}, true
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
