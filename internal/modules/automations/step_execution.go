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
	// Lifecycle projections consume validated details and never encode evidence.
	Kind() StepKind
	Phase() string
	StartTime() time.Time
	CompletionTime() *time.Time
	RunFailure() (RunOutcome, bool)
	// encodeValidated returns kind-owned evidence after the caller validates this value.
	encodeValidated() (any, error)
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

func isInterruptionCode(code string) bool {
	return code == FailureCoreStopping || code == FailureCoreRestarted || code == FailureExecutorFault
}

func isValidExecutionTimestamp(at time.Time) bool {
	_, offset := at.Zone()
	return !at.IsZero() && offset == 0
}
