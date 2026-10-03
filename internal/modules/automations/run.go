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
	switch completion.Status {
	case StepNotAttempted, StepRunning:
		return fmt.Errorf("%w: step completion status %q is not terminal", ErrInvalidAutomation, completion.Status)
	case StepSatisfied, StepDispatched:
		if completion.VerifiedCommandID == nil || completion.FailureCode != nil {
			return fmt.Errorf(
				"%w: successful step requires a verified Command and no failure code",
				ErrInvalidAutomation,
			)
		}
	case StepFailed, StepInterrupted:
		if completion.FailureCode == nil {
			return fmt.Errorf("%w: failing step requires a failure code", ErrInvalidAutomation)
		}
	default:
		return fmt.Errorf("%w: unknown step completion status %q", ErrInvalidAutomation, completion.Status)
	}
	if completion.VerifiedCommandID != nil {
		if _, err := devices.ParseCommandID(string(*completion.VerifiedCommandID)); err != nil {
			return fmt.Errorf("%w: verified command ID: %w", ErrInvalidAutomation, err)
		}
	}
	return nil
}

// ValidateRunCompletion rejects a Run completion that is not a terminal outcome or lacks its required evidence.
func ValidateRunCompletion(completion RunCompletion) error {
	switch completion.Status {
	case RunSucceeded:
		if completion.FailureCode != nil {
			return invalid("succeeded Run carries a failure code")
		}
	case RunFailed, RunInterrupted:
		if completion.FailureCode == nil {
			return invalid("failing Run requires a failure code")
		}
	case RunRunning:
		return invalid("run completion status %q is not terminal", completion.Status)
	default:
		return invalid("run completion status %q is not terminal", completion.Status)
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

// StepAttempt records an ordered Step's execution state.
// Reserved identities are private; only verified Command links are exposed.
type StepAttempt struct {
	Position              int
	StepID                StepID
	Status                StepStatus
	ReservedCommandID     *devices.CommandID
	ReservedCorrelationID *devices.CorrelationID
	VerifiedCommandID     *devices.CommandID
	FailureCode           *string
	StartedAt             *time.Time
	CompletedAt           *time.Time
}

// Run tracks execution of an immutable definition snapshot with admission provenance and ordered Step attempts.
type Run struct {
	ID                RunID
	AutomationID      AutomationID
	AutomationName    string
	Revision          int64
	Snapshot          Definition
	Source            RunSource
	Fact              *DeviceFactSummary // non-nil iff Source is RunSourceDeviceFact
	HeldState         *HeldStateEvidence // non-nil iff Source is RunSourceHeldState
	MatchedTriggerIDs []TriggerID        // empty iff Source is RunSourceManual
	ConditionDecision ConditionDecision
	Status            RunStatus
	FailureCode       *string
	StartedAt         time.Time
	CompletedAt       *time.Time
	Steps             []StepAttempt
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
	RunID             RunID
	Position          int
	Status            StepStatus
	VerifiedCommandID *devices.CommandID
	FailureCode       *string
}

// RunCompletion records one Run's established terminal state.
type RunCompletion struct {
	RunID       RunID
	Status      RunStatus
	FailureCode *string
}

// NewRunSnapshot builds one Run from a persisted definition record, an
// already-minted identity, and its committed admission Condition decision. It
// takes ownership of record.Definition and fact, copies matchedTriggerIDs, and
// starts every Step at not_attempted in definition order.
func NewRunSnapshot(
	record Record,
	runID RunID,
	source RunSource,
	fact *DeviceFactSummary,
	matchedTriggerIDs []TriggerID,
	conditionDecision ConditionDecision,
	admittedAt time.Time,
) Run {
	steps := make([]StepAttempt, len(record.Definition.Steps))
	for position, step := range record.Definition.Steps {
		steps[position] = StepAttempt{
			Position: position,
			StepID:   step.ID,
			Status:   StepNotAttempted,
		}
	}
	return Run{
		ID:                runID,
		AutomationID:      record.ID,
		AutomationName:    record.Definition.Name,
		Revision:          record.Revision,
		Snapshot:          record.Definition,
		Source:            source,
		Fact:              fact,
		MatchedTriggerIDs: append([]TriggerID(nil), matchedTriggerIDs...),
		ConditionDecision: conditionDecision,
		Status:            RunRunning,
		StartedAt:         admittedAt.UTC(),
		Steps:             steps,
	}
}
