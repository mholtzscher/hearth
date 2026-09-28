package automations

import (
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// RunID is the durable identity of one Automation Run (arn_ UUIDv7), distinct
// from the adapter run_ identity.
type RunID string

// ValidateRunConditionDecision checks one Run's decision against its admission
// source and outcome. Envelope coherence holds by construction; only the
// record-kind rules remain.
func ValidateRunConditionDecision(run Run) error {
	decision := run.ConditionDecision
	switch decision.DecisionMode() {
	case ConditionDecisionNotConfigured, ConditionDecisionEvaluated:
	case ConditionDecisionBypassed:
		if run.Source != RunSourceManual {
			return invalid("condition decision: only a manual Run may bypass conditions")
		}
	case ConditionDecisionNotEvaluated:
		return invalid("condition decision: a Run never records a not_evaluated decision")
	default:
		return invalid("condition decision: unknown mode %q", decision.DecisionMode())
	}
	if evaluation := decision.DecisionEvaluation(); evaluation != nil && evaluation.Result != ConditionTrue {
		return invalid("condition decision: an evaluated Run requires a true root result")
	}
	return nil
}

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

// ValidateRun rejects an impossible Run identity, provenance, snapshot, status, timestamps, or Steps.
func ValidateRun(run Run) error {
	if _, err := ParseRunID(string(run.ID)); err != nil {
		return err
	}
	if _, err := ParseAutomationID(string(run.AutomationID)); err != nil {
		return err
	}
	if run.Revision < 1 {
		return invalid("run %q: revision must be at least 1", run.ID)
	}
	if _, err := NormalizeDefinition(run.Snapshot); err != nil {
		return invalid("run %q: definition snapshot is invalid", run.ID)
	}
	if err := validateRunProvenance(run); err != nil {
		return err
	}
	if err := ValidateRunConditionDecision(run); err != nil {
		return err
	}
	if run.StartedAt.IsZero() {
		return invalid("run %q: start time is required", run.ID)
	}
	if err := validateRunStatus(run); err != nil {
		return err
	}
	if len(run.Steps) != len(run.Snapshot.Steps) {
		return invalid("run %q: step count does not match the definition snapshot", run.ID)
	}
	for position, step := range run.Steps {
		if err := validateStepAttempt(run, position, step); err != nil {
			return err
		}
	}
	return nil
}
func validateRunProvenance(run Run) error {
	switch run.Source {
	case RunSourceDeviceFact:
		if err := validateDeviceFactRunProvenance(run); err != nil {
			return err
		}
	case RunSourceManual:
		if err := validateManualRunProvenance(run); err != nil {
			return err
		}
	case RunSourceHeldState:
		if err := validateHeldStateRunProvenance(run); err != nil {
			return err
		}
	default:
		return invalid("run %q: unknown source %q", run.ID, run.Source)
	}
	if invalidMatchedTriggerCount(run) {
		return invalid("run %q: matched trigger IDs must be empty iff the Run is manual", run.ID)
	}
	if err := validateMatchedTriggerIDs(run); err != nil {
		return err
	}
	if run.Source == RunSourceHeldState {
		if run.MatchedTriggerIDs[0] != run.HeldState.TriggerID {
			return invalid("run %q: held-state evidence trigger does not match admission trigger", run.ID)
		}
		for _, trigger := range run.Snapshot.Triggers {
			if trigger.ID == run.HeldState.TriggerID && trigger.Kind == TriggerKindHeldState {
				return nil
			}
		}
		return invalid("run %q: held-state evidence trigger is absent from snapshot", run.ID)
	}
	return nil
}

func validateDeviceFactRunProvenance(run Run) error {
	if run.Fact == nil || run.HeldState != nil {
		return invalid("run %q: device fact Run requires Fact evidence", run.ID)
	}
	return ValidateDeviceFactSummary(*run.Fact)
}

func validateManualRunProvenance(run Run) error {
	if run.Fact != nil || run.HeldState != nil {
		return invalid("run %q: manual Run carries admission evidence", run.ID)
	}
	return nil
}

func validateHeldStateRunProvenance(run Run) error {
	if run.Fact != nil || run.HeldState == nil {
		return invalid("run %q: held-state Run requires hold evidence and no Fact", run.ID)
	}
	return validateHeldStateEvidence(*run.HeldState)
}

func invalidMatchedTriggerCount(run Run) bool {
	return run.Source == RunSourceManual && len(run.MatchedTriggerIDs) != 0 ||
		run.Source == RunSourceDeviceFact && len(run.MatchedTriggerIDs) == 0 ||
		run.Source == RunSourceHeldState && len(run.MatchedTriggerIDs) != 1
}

func validateMatchedTriggerIDs(run Run) error {
	seen := make(map[TriggerID]bool, len(run.MatchedTriggerIDs))
	next := 0
	for _, trigger := range run.Snapshot.Triggers {
		if seen[trigger.ID] {
			return invalid("run %q: snapshot trigger IDs must be unique", run.ID)
		}
		seen[trigger.ID] = true
		if next < len(run.MatchedTriggerIDs) && trigger.ID == run.MatchedTriggerIDs[next] {
			next++
		}
	}
	if next != len(run.MatchedTriggerIDs) {
		return invalid("run %q: matched trigger IDs must be a subset of the snapshot in definition order", run.ID)
	}
	return nil
}

func validateRunStatus(run Run) error {
	switch run.Status {
	case RunRunning, RunSucceeded, RunFailed, RunInterrupted:
	default:
		return invalid("run %q: unknown status %q", run.ID, run.Status)
	}
	if run.Status == RunRunning && run.CompletedAt != nil {
		return invalid("run %q: running Run carries a completion time", run.ID)
	}
	if run.Status != RunRunning && run.CompletedAt == nil {
		return invalid("run %q: terminal Run requires a completion time", run.ID)
	}
	if (run.Status == RunRunning || run.Status == RunSucceeded) && run.FailureCode != nil {
		return invalid("run %q: non-failing Run carries a failure code", run.ID)
	}
	if (run.Status == RunFailed || run.Status == RunInterrupted) && run.FailureCode == nil {
		return invalid("run %q: failed or interrupted Run requires a failure code", run.ID)
	}
	return nil
}

func validateStepAttempt(run Run, position int, step StepAttempt) error {
	if step.Position != position {
		return invalid("run %q: step positions must be contiguous and zero-based", run.ID)
	}
	if step.StepID != run.Snapshot.Steps[position].ID {
		return invalid("run %q: step identity does not match the definition snapshot", run.ID)
	}
	switch step.Status {
	case StepNotAttempted:
		if step.ReservedCommandID != nil || step.ReservedCorrelationID != nil ||
			step.VerifiedCommandID != nil || step.StartedAt != nil || step.CompletedAt != nil {
			return invalid("run %q: not attempted step carries attempt evidence", run.ID)
		}
	case StepRunning:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt != nil || step.VerifiedCommandID != nil {
			return invalid("run %q: running step requires reserved identities and no verification", run.ID)
		}
	case StepSatisfied, StepDispatched:
		if step.ReservedCommandID == nil || step.ReservedCorrelationID == nil ||
			step.StartedAt == nil || step.CompletedAt == nil || step.VerifiedCommandID == nil {
			return invalid("run %q: successful step requires verified Command evidence", run.ID)
		}
	case StepFailed, StepInterrupted:
		if step.StartedAt == nil || step.CompletedAt == nil || step.FailureCode == nil {
			return invalid("run %q: terminal failure requires start, completion, and a failure code", run.ID)
		}
	default:
		return invalid("run %q: unknown step status %q", run.ID, step.Status)
	}
	return nil
}
