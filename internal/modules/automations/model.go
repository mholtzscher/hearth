package automations

import (
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationID is a canonical aut_-prefixed UUIDv7.
type AutomationID string

// AutomationRunID is a canonical arn_-prefixed UUIDv7, not an adapter runtime ID.
type AutomationRunID string

// AutomationTriggerID is an author-supplied slug, stable across definition edits.
type AutomationTriggerID string // author-supplied slug, unique within one definition

// AutomationTrigger carries one identified cron cause in definition order.
type AutomationTrigger struct {
	ID         AutomationTriggerID
	Kind       string // exactly "cron" in this version
	Expression string
}

// AutomationStep targets an Entity Operation with owned JSON object parameters.
type AutomationStep struct {
	EntityID      devices.EntityID
	OperationName devices.OperationName
	Parameters    devices.CommandParameters // owned JSON object bytes
}

// AutomationDefinition is the normalized, schema-backed automation document.
type AutomationDefinition struct {
	Name     string
	Enabled  bool
	Triggers []AutomationTrigger
	Steps    []AutomationStep
}

// AutomationRecord stores a live revision; names need not be unique.
type AutomationRecord struct {
	ID         AutomationID
	Revision   int64
	Definition AutomationDefinition
	CreatedAt  time.Time
	UpdatedAt  time.Time
}

// AutomationRunSource distinguishes manual and scheduled admission provenance.
type AutomationRunSource string // "manual" | "scheduled"
// AutomationRunStatus distinguishes active claims from terminal history.
type AutomationRunStatus string // "running" | "succeeded" | "failed" | "interrupted"
// AutomationStepStatus records sequential progress, never physical-effect certainty.
type AutomationStepStatus string // "pending" | "running" | "satisfied" | "dispatched" | "failed" | "not_attempted" | "interrupted"

// AutomationRunSnapshot retains the complete revision and household timezone at admission.
type AutomationRunSnapshot struct {
	AutomationID AutomationID
	Revision     int64
	Definition   AutomationDefinition
	Timezone     string
}

// AutomationRunStep retains intent and ownership-verified command evidence.
// PrecreationFailure marks a confirmed pre-creation failure and durably forbids
// command adoption even when both reserved identities later match a command.
// Owned terminal failures preserve the command failure code with PrecreationFailure
// false so their command evidence stays visible in history and recovery.
type AutomationRunStep struct {
	Index                 int // zero based; immutable within this Run
	Definition            AutomationStep
	Status                AutomationStepStatus
	ReservedCommandID     *devices.CommandID     // allocated before attempted execution
	ReservedCorrelationID *devices.CorrelationID // internal ownership marker; not exposed by the Run API
	CommandID             *devices.CommandID     // only present for a Command owned by this Step
	CommandStatus         *devices.CommandStatus
	Outcome               *devices.OutcomeKind
	FailureCode           *string
	PrecreationFailure    bool
	StartedAt             *time.Time
	CompletedAt           *time.Time
}

// AutomationRunRecord survives definition deletion until terminal history pruning.
type AutomationRunRecord struct {
	ID                AutomationRunID
	Snapshot          AutomationRunSnapshot
	Source            AutomationRunSource
	ScheduledAt       *time.Time            // only scheduled Runs; UTC instant
	MatchedTriggerIDs []AutomationTriggerID // empty for manual, all matches for scheduled
	Status            AutomationRunStatus
	StartedAt         time.Time // admission time
	CompletedAt       *time.Time
	FailureCode       *string
	Steps             []AutomationRunStep
}

type AutomationPage[T any] struct {
	Items   []T
	HasMore bool
}

// AutomationOccurrenceStatus distinguishes admitted schedule matches from overlap skips.
type AutomationOccurrenceStatus string // "started" | "skipped"

// AutomationOccurrence records one Automation's collected Trigger matches at one
// UTC minute. MatchedTriggers retains the complete matching Trigger snapshots in
// definition array order; it is never reconstructed from the current definition.
type AutomationOccurrence struct {
	AutomationID    AutomationID
	Revision        int64
	Name            string // retained diagnostic name, even after definition deletion
	MatchedTriggers []AutomationTrigger
	Timezone        string
	ScheduledAt     time.Time
	EvaluatedAt     time.Time
	Status          AutomationOccurrenceStatus
	RunID           *AutomationRunID // present only when started
	SkipReason      *string          // "automation_run_active" only in this version
}

// AutomationScheduleGap records one unevaluated UTC minute interval. Gaps explain
// intervals the scheduler did not evaluate; they never claim per-definition
// historical matches.
type AutomationScheduleGap struct {
	ID               string // "asg_" + canonical UUIDv7
	FromExclusive    time.Time
	ThroughInclusive time.Time
	RecordedAt       time.Time
	Reason           string // "core_restart" | "clock_or_processing_gap"
}

// AutomationSchedulerState is the persisted minute evaluation progress.
type AutomationSchedulerState struct {
	HighWaterMinute time.Time
	Timezone        string
}

// AutomationScheduleBatch is the committed result of one evaluated UTC minute.
// Evaluated distinguishes an actual minute evaluation from a duplicate or
// backward minute no-op: both carry empty Runs and Occurrences with no Gap,
// but only an actual evaluation advances progress and may clear scheduler
// health. No-ops alone must never clear an unresolved scheduler failure.
type AutomationScheduleBatch struct {
	Runs        []AutomationRunRecord
	Occurrences []AutomationOccurrence
	Gap         *AutomationScheduleGap
	Evaluated   bool // true only when the current UTC minute was actually evaluated
}

// AutomationOccurrenceListParams is a descending time/ID position, optionally filtered.
type AutomationOccurrenceListParams struct {
	AutomationID       *AutomationID
	BeforeScheduledAt  *time.Time
	BeforeAutomationID *AutomationID
	Limit              int
}

// AutomationScheduleGapListParams is a descending time/ID position.
type AutomationScheduleGapListParams struct {
	BeforeRecordedAt *time.Time
	BeforeID         *string
	Limit            int
}

// AutomationUpdate replaces a definition using optimistic revision concurrency.
type AutomationUpdate struct {
	ID               AutomationID
	ExpectedRevision int64
	Definition       AutomationDefinition
}

// AutomationListParams is an ascending ID keyset position.
type AutomationListParams struct {
	AfterID *AutomationID
	Limit   int
}

// AutomationRunListParams is a descending time/ID position, optionally filtered.
type AutomationRunListParams struct {
	AutomationID    *AutomationID
	BeforeStartedAt *time.Time
	BeforeID        *AutomationRunID
	Limit           int
}

// AutomationManualRequest scopes a retained idempotency key to its automation.
type AutomationManualRequest struct {
	AutomationID   AutomationID
	IdempotencyKey string
}

// AutomationAdmission identifies an atomic new or reused run.
type AutomationAdmission struct {
	Run    AutomationRunRecord
	Reused bool
}

// AutomationManualAdmission supplies process timezone to transactional admission.
// The repository loads the current definition and allocates the run identity/time.
type AutomationManualAdmission struct {
	Request  AutomationManualRequest
	Timezone string
}

// AutomationStepStart reserves independent identities before command creation.
type AutomationStepStart struct {
	RunID         AutomationRunID
	Index         int
	CommandID     devices.CommandID
	CorrelationID devices.CorrelationID
}

// AutomationStepCompletion establishes a durable result, never an ambiguous fault.
// Failed completion also fails the run and marks later steps not_attempted atomically.
// PrecreationFailure is true only for confirmed pre-creation failures that durably
// forbid command adoption; owned terminal failures copy the command failure code
// with PrecreationFailure false so their evidence stays visible.
type AutomationStepCompletion struct {
	RunID              AutomationRunID
	Index              int
	Status             AutomationStepStatus
	Outcome            *devices.OutcomeKind
	FailureCode        *string
	PrecreationFailure bool
}

// AutomationRunCompletion terminates a known sequence; interruption skips pending steps.
// Faulted, uncertain runs must not be passed here during graceful shutdown.
type AutomationRunCompletion struct {
	RunID       AutomationRunID
	Status      AutomationRunStatus
	FailureCode *string
}

// AutomationStepOwnsCommand is the sole command ownership predicate for execution,
// history evidence and recovery. A confirmed pre-creation failure durably forbids
// adoption through the persisted pre-creation failure marker, never the failure code:
// an owned terminal command that copies internal_error stays visible while a
// confirmed pre-creation internal_error excludes linkage even on matching identities.
func AutomationStepOwnsCommand(step AutomationRunStep, command devices.CommandRecord) bool {
	return !step.PrecreationFailure && step.ReservedCommandID != nil &&
		step.ReservedCorrelationID != nil &&
		command.ID == *step.ReservedCommandID &&
		command.CorrelationID == *step.ReservedCorrelationID
}
