package automations

import (
	"context"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// DefinitionRepository manages definitions without admitting or executing Runs.
// Writes accept arbitrary typed definitions and validate structural integrity
// and encoded size; current Devices reference validation belongs to the Service.
type DefinitionRepository interface {
	CreateAutomation(context.Context, Definition) (Record, error)
	GetAutomation(context.Context, AutomationID) (Record, error)
	ListAutomations(context.Context, ListAutomationsParams) (Page[Record], error)
	ReplaceAutomation(context.Context, AutomationID, int64, Definition) (Record, error)
	DeleteAutomation(context.Context, AutomationID, int64) error
}

// Repository is the complete domain-oriented persistence seam. Admission
// validates facts independently and checks eligibility against transaction-local
// state, even when callers bypass the Service.
type Repository interface {
	DefinitionRepository

	// ListEnabledAutomations reads every currently enabled definition in
	// ascending Automation ID order. Returned definitions are owned and normalized,
	// including schedule preparation reused by unchanged-definition matching.
	ListEnabledAutomations(context.Context) ([]Record, error)
	// InitializeScheduleWatermark advances progress without admission, retaining a future mark.
	InitializeScheduleWatermark(context.Context, time.Time) error
	// AdmitDueSchedules re-reads definitions and commits the entire tick plus progress atomically.
	AdmitDueSchedules(context.Context, devices.EntityStateSnapshot, ScheduleTick) (AdmissionResult, error)

	// AdmitDeviceFact commits every matching outcome in one transaction; the
	// supplied snapshot must cover every Entity the transaction's current eligible
	// Conditions require.
	AdmitDeviceFact(
		context.Context, DeviceFact, devices.EntityStateSnapshot, time.Time, time.Time,
	) (AdmissionResult, error)
	// ListDueHeldStates returns at most limit pending holds ordered by deadline and identity.
	ListDueHeldStates(context.Context, time.Time, int) ([]HeldStateCandidate, error)
	// AdmitDueHeldStates uses due cutoff for selection and evaluatedAt for Conditions and outcomes.
	AdmitDueHeldStates(
		context.Context,
		devices.EntityStateSnapshot,
		time.Time,
		time.Time,
		int,
	) (AdmissionResult, int, error)
	// ResetPendingHeldStates clears pending deadlines while retaining receive-order watermarks.
	ResetPendingHeldStates(context.Context) error
	// AdmitManualRun starts one Run, or commits one Condition Skip, from the
	// current definition snapshot even when the Automation is disabled.
	AdmitManualRun(
		context.Context, ManualRunInput, devices.EntityStateSnapshot, time.Time,
	) (ManualAdmissionResult, error)
	// MarkStepRunning persists one Step's reserved Command identities.
	MarkStepRunning(context.Context, StepStart) error
	// CompleteStep persists one Step's established terminal outcome.
	CompleteStep(context.Context, StepCompletion) error
	// RecordBranchDecision validates and appends immutable evidence once. Unknown
	// or error evidence and the failed Run outcome commit atomically.
	RecordBranchDecision(context.Context, RunID, BranchDecision) error
	// RecordDelayStart appends reached wait evidence under a running snapshot.
	RecordDelayStart(context.Context, DelayStart) error
	// CompleteDelay commits terminal wait evidence; interruption also ends the Run.
	CompleteDelay(context.Context, DelayCompletion) error
	// CompleteRun persists one Run's established terminal state.
	CompleteRun(context.Context, RunCompletion) error
	// GetHistoryEntry reads one retained Run or Skip scoped to its Automation.
	GetHistoryEntry(context.Context, AutomationID, string) (HistoryEntry, error)
	// ListHistory pages retained Run and Skip summaries newest first.
	ListHistory(context.Context, ListHistoryParams) (Page[HistorySummary], error)
	// InterruptActiveRuns marks every running Step and Run as interrupted with the
	// supplied reason.
	InterruptActiveRuns(context.Context, time.Time, string) error
	// DeleteHistoryBefore removes at most limit terminal history records older
	// than the cutoff.
	DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}
