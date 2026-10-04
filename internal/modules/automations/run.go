package automations

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// RunID is the durable identity of one Automation Run (arn_ UUIDv7), distinct
// from the adapter run_ identity.
type RunID string

// ValidateStepCompletion rejects a Step completion that is not a terminal outcome or lacks its required evidence.
func ValidateStepCompletion(completion StepCompletion) error {
	var verified *devices.CommandID
	switch outcome := completion.Outcome.(type) {
	case SatisfiedStep:
		verified = &outcome.VerifiedCommandID
	case DispatchedStep:
		verified = &outcome.VerifiedCommandID
	case FailedStep:
		if outcome.FailureCode == "" {
			return invalid("failing step requires a failure code")
		}
		verified = outcome.VerifiedCommandID
	case InterruptedStep:
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
	// RunSucceeded marks a Run whose every Step reached a successful outcome.
	RunSucceeded RunStatus = "succeeded"
	// RunFailed marks a Run stopped by the first failed Step.
	RunFailed RunStatus = "failed"
	// RunInterrupted marks a Run that stopped without establishing a Command
	// outcome, including core restart, drain, and executor faults.
	RunInterrupted RunStatus = "interrupted"
)

// StepStatus is the durable state of one ordered Run Step.
type StepStatus string

const (
	// StepNotAttempted marks a Step that never started.
	StepNotAttempted StepStatus = "not_attempted"
	// StepRunning marks a Step whose Command identity is reserved and recorded.
	StepRunning StepStatus = "running"
	// StepSatisfied marks a Step whose verified Command reached satisfied.
	StepSatisfied StepStatus = "satisfied"
	// StepDispatched marks a Step whose verified Command reached dispatched.
	StepDispatched StepStatus = "dispatched"
	// StepFailed marks a Step whose verified or confirmed Command failed.
	StepFailed StepStatus = "failed"
	// StepInterrupted marks a Step stopped without a durable Command outcome.
	StepInterrupted StepStatus = "interrupted"
)

// StepAttempt records a Command leaf's execution state at its stable position.
// Reserved identities are private; only verified Command links are exposed.
type StepAttempt struct {
	Position int
	StepID   StepID
	State    StepAttemptState
}

// Run tracks execution of an immutable definition snapshot with admission provenance and ordered Step attempts.
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
	Steps             []StepAttempt
	BranchDecisions   []BranchDecision
}

// StepStart reserves and records one Step's Command identity before execution.
type StepStart struct {
	RunID         RunID
	Position      int
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// StepCompletion records one Step's established terminal outcome.
type StepCompletion struct {
	RunID    RunID
	Position int
	Outcome  StepOutcome
}

// RunCompletion records one Run's established terminal state.
type RunCompletion struct {
	RunID   RunID
	Outcome RunOutcome
}

// NewRunSnapshot builds one Run from a persisted definition record, an
// already-minted identity, and its committed admission Condition decision. It
// takes ownership of record.Definition and cause, copies matchedTriggerIDs, and
// starts every Command leaf at not_attempted in stable definition order.
// record.Definition must be an unchanged normalized definition.
func NewRunSnapshot(
	record Record,
	runID RunID,
	cause AdmissionCause,
	matchedTriggerIDs []TriggerID,
	conditionDecision ConditionDecision,
	admittedAt time.Time,
) Run {
	leaves := CommandLeaves(record.Definition.Steps)
	steps := make([]StepAttempt, len(leaves))
	for position, step := range leaves {
		steps[position] = StepAttempt{
			Position: position,
			StepID:   step.ID,
			State:    NotAttemptedStep{},
		}
	}
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
		Steps:             steps,
		BranchDecisions:   make([]BranchDecision, 0),
	}
}
