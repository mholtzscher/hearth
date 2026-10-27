package automations

import "time"

// RunID is the durable identity of one Automation Run (arn_ UUIDv7), distinct
// from the adapter run_ identity.
type RunID string

// RunOutcome is one established terminal outcome.
//
//sumtype:decl
type RunOutcome interface{ isRunOutcome() }

// SucceededRun records successful completion of the selected execution path.
type SucceededRun struct{}

// FailedRun records a Command or branch failure.
type FailedRun struct{ FailureCode string }

// InterruptedRun records stopped execution without an established outcome.
type InterruptedRun struct{ FailureCode string }

func (SucceededRun) isRunOutcome()   {}
func (FailedRun) isRunOutcome()      {}
func (InterruptedRun) isRunOutcome() {}

// RunState is either active execution or a timestamped terminal outcome.
//
//sumtype:decl
type RunState interface{ isRunState() }

// RunningRun records active execution.
type RunningRun struct{}

// CompletedRun records when a terminal outcome was committed.
type CompletedRun struct {
	CompletedAt time.Time
	Outcome     RunOutcome
}

func (RunningRun) isRunState()   {}
func (CompletedRun) isRunState() {}

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

// Run retains an immutable definition, admission provenance, and one sparse execution trace.
type Run struct {
	ID                  RunID
	AutomationID        AutomationID
	AutomationName      string
	Revision            int64
	Snapshot            Definition
	Cause               AdmissionCause
	MatchedTriggerIDs   []TriggerID
	ConditionDecision   ConditionDecision
	State               RunState
	StartedAt           time.Time
	Executions          []StepExecution
	DataEvidence        string
	ValueInitialization *RunValueInitialization
}

const (
	RunDataRecorded          = "recorded"
	RunDataLegacyUnavailable = "legacy_unavailable"
)

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
		DataEvidence:      RunDataRecorded,
	}
}

// RunOutcomeStatus derives the persisted and public terminal status.
func RunOutcomeStatus(outcome RunOutcome) RunStatus {
	switch outcome.(type) {
	case SucceededRun:
		return RunSucceeded
	case FailedRun:
		return RunFailed
	case InterruptedRun:
		return RunInterrupted
	default:
		return ""
	}
}

// RunOutcomeFailureCode returns an owned optional failure label.
func RunOutcomeFailureCode(outcome RunOutcome) *string {
	switch outcome := outcome.(type) {
	case SucceededRun:
		return nil
	case FailedRun:
		return &outcome.FailureCode
	case InterruptedRun:
		return &outcome.FailureCode
	default:
		return nil
	}
}

// RunStateStatus derives the lifecycle status.
func RunStateStatus(state RunState) RunStatus {
	switch state := state.(type) {
	case RunningRun:
		return RunRunning
	case CompletedRun:
		return RunOutcomeStatus(state.Outcome)
	default:
		return ""
	}
}

// RunStateCompletion returns the optional completion timestamp and failure code.
func RunStateCompletion(state RunState) (*time.Time, *string) {
	switch state := state.(type) {
	case RunningRun:
		return nil, nil
	case CompletedRun:
		return &state.CompletedAt, RunOutcomeFailureCode(state.Outcome)
	default:
		return nil, nil
	}
}
