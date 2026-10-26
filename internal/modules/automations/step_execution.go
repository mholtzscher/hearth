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
	Outcome     StepOutcome
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

// StepExecutionBody is a typed lifecycle variant; kind and phase are derived from it.
//
//sumtype:decl
type StepExecutionBody interface{ isStepExecutionBody() }
type CommandExecution struct {
	StartedAt   time.Time
	CompletedAt *time.Time
	Reservation *CommandReservation
	Outcome     StepOutcome
}
type DelayExecutionBody struct {
	StartedAt   time.Time
	CompletedAt *time.Time
	FailureCode *string
}
type NotificationExecutionBody struct {
	StartedAt   time.Time
	CompletedAt *time.Time
	AttemptID   notifications.AttemptID
	Outcome     *notifications.Outcome
	ReasonCode  *string
}
type FunctionExecutionBody struct {
	StartedAt    time.Time
	CompletedAt  *time.Time
	InvocationID adapters.InvocationID
	State        FunctionExecutionState
}
type BranchExecutionBody struct{ Decision BranchDecision }

func (CommandExecution) isStepExecutionBody()          {}
func (DelayExecutionBody) isStepExecutionBody()        {}
func (NotificationExecutionBody) isStepExecutionBody() {}
func (FunctionExecutionBody) isStepExecutionBody()     {}
func (BranchExecutionBody) isStepExecutionBody()       {}

type StepExecution struct {
	StepID   StepID
	Sequence int
	Body     StepExecutionBody
}

// RecordStepBody validates a reached record and converts it to the canonical trace value.
func RecordStepBody(record StepExecutionRecord) (StepExecutionBody, error) {
	if _, err := ParseRunID(string(record.RunID)); err != nil {
		return nil, err
	}
	if _, err := ParseStepID(string(record.StepID)); err != nil {
		return nil, err
	}
	switch e := record.Evidence.(type) {
	case CommandStarted:
		return commandStartedBody(e)
	case DelayStarted:
		if !validDelayTimestamp(e.StartedAt) {
			return nil, invalid("invalid delay start time")
		}
		return DelayExecutionBody{StartedAt: e.StartedAt}, nil
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
		return NotificationExecutionBody{StartedAt: e.StartedAt, AttemptID: e.AttemptID}, nil
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
		return FunctionExecutionBody{
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
		return BranchExecutionBody{Decision: d}, nil
	case CommandStopped:
		if !validDelayTimestamp(e.At) || !interruptionCode(e.FailureCode) {
			return nil, invalid("invalid pre-reservation command interruption")
		}
		return CommandExecution{
			StartedAt:   e.At,
			CompletedAt: &e.At,
			Outcome:     InterruptedStep{FailureCode: e.FailureCode},
		}, nil
	default:
		return nil, invalid("unsupported reached evidence %T", record.Evidence)
	}
}

func commandStartedBody(start CommandStarted) (CommandExecution, error) {
	if !validDelayTimestamp(start.StartedAt) {
		return CommandExecution{}, invalid("invalid Command start time")
	}
	if _, err := devices.ParseCommandID(string(start.Reservation.CommandID)); err != nil {
		return CommandExecution{}, err
	}
	if _, err := devices.ParseCorrelationID(string(start.Reservation.CorrelationID)); err != nil {
		return CommandExecution{}, err
	}
	return CommandExecution{StartedAt: start.StartedAt, Reservation: &start.Reservation}, nil
}

// CompleteStepBody validates and applies terminal evidence without changing the reservation.
func CompleteStepBody(body StepExecutionBody, completion StepExecutionCompletion) (StepExecutionBody, error) {
	if _, err := ParseRunID(string(completion.RunID)); err != nil {
		return nil, err
	}
	if _, err := ParseStepID(string(completion.StepID)); err != nil {
		return nil, err
	}
	if _, err := EncodeStepExecutionBody(body); err != nil {
		return nil, err
	}
	switch b := body.(type) {
	case CommandExecution:
		return completeCommandBody(b, completion.Evidence)
	case DelayExecutionBody:
		return completeDelayBody(b, completion)
	case NotificationExecutionBody:
		return completeNotificationBody(b, completion)
	case FunctionExecutionBody:
		return completeFunctionBody(b, completion)
	case BranchExecutionBody:
		return nil, invalid("branch selection is insert-once")
	default:
		return nil, invalid("execution body is not completable")
	}
}

func completeCommandBody(b CommandExecution, evidence CompletedStepEvidence) (CommandExecution, error) {
	c, ok := evidence.(CommandFinished)
	if !ok || b.CompletedAt != nil || b.Reservation == nil || *b.Reservation != c.Reservation {
		return CommandExecution{}, invalid("command completion reservation mismatch")
	}
	at := c.CompletedAt
	if !validDelayTimestamp(at) {
		return CommandExecution{}, invalid("invalid command completion time")
	}
	if err := ValidateStepCompletion(StepCompletion{Outcome: c.Outcome}); err != nil {
		return CommandExecution{}, err
	}
	verified, _ := StepOutcomeEvidence(c.Outcome)
	if verified != nil && *verified != c.Reservation.CommandID {
		return CommandExecution{}, invalid("verified Command disagrees with reservation")
	}
	b.CompletedAt = &at
	b.Outcome = c.Outcome
	return b, nil
}

func completeDelayBody(b DelayExecutionBody, completion StepExecutionCompletion) (DelayExecutionBody, error) {
	c, ok := completion.Evidence.(DelayFinished)
	if !ok || b.CompletedAt != nil {
		return DelayExecutionBody{}, invalid("delay completion requires running delay")
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
		return DelayExecutionBody{}, err
	}
	b.CompletedAt = &at
	b.FailureCode = c.FailureCode
	return b, nil
}

func completeNotificationBody(
	b NotificationExecutionBody,
	completion StepExecutionCompletion,
) (NotificationExecutionBody, error) {
	c, ok := completion.Evidence.(NotificationFinished)
	if !ok || b.CompletedAt != nil || c.AttemptID != b.AttemptID {
		return NotificationExecutionBody{}, invalid("notification reservation mismatch")
	}
	at := c.CompletedAt
	status := NotificationFailed
	if c.Outcome != nil && *c.Outcome == notifications.Accepted {
		status = NotificationAccepted
	}
	if c.Outcome == nil && c.ReasonCode != nil && interruptionCode(*c.ReasonCode) {
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
		return NotificationExecutionBody{}, err
	}
	b.CompletedAt = &at
	b.Outcome = c.Outcome
	b.ReasonCode = c.ReasonCode
	return b, nil
}

func completeFunctionBody(b FunctionExecutionBody, completion StepExecutionCompletion) (FunctionExecutionBody, error) {
	c, ok := completion.Evidence.(FunctionFinished)
	if !ok || b.CompletedAt != nil || c.InvocationID != b.InvocationID {
		return FunctionExecutionBody{}, invalid("function reservation mismatch")
	}
	if err := ValidateFunctionCompletion(
		FunctionCompletion{
			RunID:        completion.RunID,
			StepID:       completion.StepID,
			InvocationID: c.InvocationID,
			State:        c.State,
		},
	); err != nil {
		return FunctionExecutionBody{}, err
	}
	at := FunctionTerminalTime(c.State)
	if at == nil {
		return FunctionExecutionBody{}, invalid("function completion is running")
	}
	if !c.CompletedAt.Equal(*at) {
		return FunctionExecutionBody{}, invalid("Function completion timestamps disagree")
	}
	b.CompletedAt = at
	b.State = c.State
	return b, nil
}
