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
type StepExecutionDetails interface {
	// Validate checks this value's identities, timestamps, and lifecycle evidence,
	// without consulting a definition, previous execution, or live dependency.
	Validate() error
	isStepExecutionDetails()
}
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

// validateStepExecutionDetails accepts only the supported concrete value variants.
// Pointer representations are not part of the execution contract.
func validateStepExecutionDetails(details StepExecutionDetails) error {
	switch details.(type) {
	case CommandExecutionDetails, DelayExecutionDetails, NotificationExecutionDetails,
		FunctionExecutionDetails, BranchExecutionDetails:
		return details.Validate()
	default:
		return invalid("unsupported execution details %T", details)
	}
}

// executionDetailsPhase projects already validated details without encoding evidence.
func executionDetailsPhase(details StepExecutionDetails) string {
	if outcome, failed := StepExecutionRunFailure(details); failed {
		return string(RunOutcomeStatus(outcome))
	}
	if executionDetailsCompletion(details) != nil {
		return executionSucceeded
	}
	return executionRunning
}

// executionDetailsCompletion consumes validated details; running values have no completion.
func executionDetailsCompletion(details StepExecutionDetails) *time.Time {
	switch details := details.(type) {
	case CommandExecutionDetails:
		return commandStateCompletion(details.State)
	case DelayExecutionDetails:
		return delayStateCompletion(details.State)
	case NotificationExecutionDetails:
		return notificationStateCompletion(details.State)
	case FunctionExecutionDetails:
		return FunctionTerminalTime(details.State)
	case BranchExecutionDetails:
		return &details.Decision.EvaluatedAt
	default:
		panic("unsupported validated execution details")
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
