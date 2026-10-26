package automations

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// RunID is the durable identity of one Automation Run (arn_ UUIDv7), distinct
// from the adapter run_ identity.
type RunID string

// ValidateCommandStepCompletion rejects a Step completion that is not a terminal outcome or lacks its required evidence.
func ValidateCommandStepCompletion(completion CommandStepCompletion) error {
	var verified *devices.CommandID
	switch outcome := completion.Outcome.(type) {
	case SatisfiedCommand:
		verified = &outcome.VerifiedCommandID
	case DispatchedCommand:
		verified = &outcome.VerifiedCommandID
	case FailedCommand:
		if outcome.FailureCode == "" {
			return invalid("failing step requires a failure code")
		}
		verified = outcome.VerifiedCommandID
	case InterruptedCommand:
		if outcome.FailureCode == "" {
			return invalid("interrupted step requires a failure code")
		}
		verified = outcome.VerifiedCommandID
	default:
		return invalid("invalid step outcome %T", completion.Outcome)
	}
	if verified != nil {
		if _, err := devices.ParseCommandID(string(*verified)); err != nil {
			return fmt.Errorf("%w: verified command ID: %w", ErrInvalidAutomation, err)
		}
	}
	return nil
}

// ValidateRunCompletion rejects a Run completion that is not a terminal outcome or lacks its required evidence.
func ValidateRunCompletion(completion RunCompletion) error {
	switch outcome := completion.Outcome.(type) {
	case SucceededRun:
	case FailedRun:
		if outcome.FailureCode == "" {
			return invalid("failing Run requires a failure code")
		}
	case InterruptedRun:
		if outcome.FailureCode == "" {
			return invalid("interrupted Run requires a failure code")
		}
	default:
		return invalid("invalid Run outcome %T", completion.Outcome)
	}
	return nil
}

// RunSource distinguishes how a Run was admitted and records the same provenance on a Skip.
type RunSource string

const (
	// RunSourceDeviceFact marks a Run admitted by a matching Device Fact.
	RunSourceDeviceFact RunSource = "device_fact"
	// RunSourceManual marks a Run admitted by an operator request.
	RunSourceManual RunSource = "manual"
	// RunSourceHeldState marks a Run admitted when a State predicate elapsed.
	RunSourceHeldState RunSource = "held_state"
	// RunSourceSchedule marks a Run admitted by a household-local cron match.
	RunSourceSchedule RunSource = "schedule"
)

// RunStatus is the durable state of one Automation Run.
type RunStatus string

const (
	// RunRunning marks an admitted Run whose Steps are still being executed.
	RunRunning RunStatus = "running"
	// RunSucceeded marks a Run whose selected path has complete successful evidence.
	RunSucceeded RunStatus = "succeeded"
	// RunFailed marks a Run stopped by the first failed Step.
	RunFailed RunStatus = "failed"
	// RunInterrupted marks a Run stopped by interruption rather than an established
	// Step failure, including core restart, drain, and executor faults.
	RunInterrupted RunStatus = "interrupted"
)

// CommandStepStatus labels an established terminal Command outcome.
type CommandStepStatus string

const (
	// CommandStepSatisfied marks a Step whose verified Command reached satisfied.
	CommandStepSatisfied CommandStepStatus = "satisfied"
	// CommandStepDispatched marks a Step whose verified Command reached dispatched.
	CommandStepDispatched CommandStepStatus = "dispatched"
	// CommandStepFailed marks a Step whose verified or confirmed Command failed.
	CommandStepFailed CommandStepStatus = "failed"
	// CommandStepInterrupted marks a Step stopped without a durable Command outcome.
	CommandStepInterrupted CommandStepStatus = "interrupted"
)

// Run retains an immutable definition, admission provenance, and one sparse execution trace.
type Run struct {
	ID                RunID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Snapshot          Definition
	Cause             AdmissionCause
	MatchedTriggerIDs []TriggerID
	ConditionDecision ConditionDecision
	State             RunState
	StartedAt         time.Time
	Executions        []StepExecution
}

// CommandStepStart reserves and records one Step's Command identity before execution.
type CommandStepStart struct {
	RunID         RunID
	StepID        StepID
	Position      int
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// CommandStepCompletion records one Step's established terminal outcome.
type CommandStepCompletion struct {
	RunID    RunID
	Position int
	Outcome  CommandOutcome
}

// RunCompletion records one Run's established terminal state.
type RunCompletion struct {
	RunID   RunID
	Outcome RunOutcome
}

// NewRunSnapshot builds one Run from a persisted definition record, an
// already-minted identity, and its committed admission Condition decision. It
// takes ownership of record.Definition and cause, copies matchedTriggerIDs, and
// starts with an empty execution trace. Only durably reached Steps gain executions.
// record.Definition must be an unchanged normalized definition.
func NewRunSnapshot(
	record Record,
	runID RunID,
	cause AdmissionCause,
	matchedTriggerIDs []TriggerID,
	conditionDecision ConditionDecision,
	admittedAt time.Time,
) Run {
	return Run{
		ID:                runID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Snapshot:          record.Definition,
		Cause:             cause,
		MatchedTriggerIDs: append([]TriggerID(nil), matchedTriggerIDs...),
		ConditionDecision: conditionDecision,
		State:             RunningRun{},
		StartedAt:         admittedAt.UTC(),
		Executions:        []StepExecution{},
	}
}
