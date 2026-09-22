package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationDevices provides reference validation, Command execution, and ownership verification.
type AutomationDevices interface {
	// ValidateObservationTrigger reports whether the Entity currently exists and
	// can be the source of an Observation Trigger and whether each comparison
	// pointer can select from its current State value when one is present.
	ValidateObservationTrigger(context.Context, devices.EntityID, []string) error
	// ValidateConditionEntity reports whether the Entity currently exists and is
	// stateful, so a Condition may select its retained State.
	ValidateConditionEntity(context.Context, devices.EntityID) error
	// GetEntityStateSnapshot reads one coherent State snapshot covering exactly
	// the requested Entity IDs.
	GetEntityStateSnapshot(context.Context, []devices.EntityID) (devices.EntityStateSnapshot, error)
	// ValidateEntityEventTrigger reports whether the Entity currently exists and
	// supports the exact Entity Event name.
	ValidateEntityEventTrigger(context.Context, devices.EntityID, devices.EntityEventName) error
	// ValidateCommand validates current Operation support and returns the
	// normalized static parameters.
	ValidateCommand(context.Context, devices.CommandInput) (devices.CommandParameters, error)
	// CommandAdmissionOpen reports whether device Command admission is still open.
	CommandAdmissionOpen() bool
	// ExecuteCommand creates and executes one Command.
	ExecuteCommand(context.Context, devices.CommandInput) (devices.CommandResult, error)
	// GetCommand reads one Command for ownership verification.
	GetCommand(context.Context, devices.CommandID) (devices.CommandRecord, error)
}

// DefinitionRepository manages definitions without admitting or executing Runs.
type DefinitionRepository interface {
	CreateAutomation(context.Context, Definition) (Record, error)
	GetAutomation(context.Context, AutomationID) (Record, error)
	ListAutomations(context.Context, ListAutomationsParams) (Page[Record], error)
	ReplaceAutomation(context.Context, AutomationID, int64, Definition) (Record, error)
	DeleteAutomation(context.Context, AutomationID, int64) error
}

// Repository is the complete domain-oriented persistence seam.
type Repository interface {
	DefinitionRepository

	// ListEnabledAutomations reads every currently enabled definition in
	// ascending Automation ID order.
	ListEnabledAutomations(context.Context) ([]Record, error)

	// AdmitDeviceFact commits every matching outcome in one transaction; the
	// supplied snapshot must cover every Entity the transaction's current eligible
	// Conditions require.
	AdmitDeviceFact(
		context.Context, DeviceFact, devices.EntityStateSnapshot, time.Time,
	) (AdmissionResult, error)
	// AdmitManualRun starts one Run, or commits one Condition Skip, from the
	// current definition snapshot even when the Automation is disabled.
	AdmitManualRun(
		context.Context, ManualRunInput, devices.EntityStateSnapshot, time.Time,
	) (ManualAdmissionResult, error)
	// MarkStepRunning persists one Step's reserved Command identities.
	MarkStepRunning(context.Context, StepStart) error
	// CompleteStep persists one Step's established terminal outcome.
	CompleteStep(context.Context, StepCompletion) error
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

// Dependencies supplies logging, time, and identity constructors; zero-valued
// fields use production defaults except HistoryRetention.
type Dependencies struct {
	Logger           *slog.Logger
	Now              func() time.Time
	NewAutomationID  func() (AutomationID, error)
	NewRunID         func() (RunID, error)
	NewSkipID        func() (SkipID, error)
	NewCommandID     func() (devices.CommandID, error)
	NewCorrelationID func() (devices.CorrelationID, error)
	// HistoryRetention is the terminal Automation history retention window
	// PruneHistory applies. It must be at least
	// MinimumAutomationHistoryRetention; zero is unconfigured and fails safely
	// at prune time, never at construction.
	HistoryRetention time.Duration
}

// WithDefaults returns a copy with every zero-valued collaborator replaced by
// its production default. HistoryRetention deliberately keeps zero so an
// unconfigured retention fails safely at prune time.
func (dependencies Dependencies) WithDefaults() Dependencies {
	logger := dependencies.Logger
	if logger == nil {
		logger = slog.Default().With(slog.String("component", "automations"))
	}
	dependencies.Logger = logger
	if dependencies.Now == nil {
		dependencies.Now = time.Now
	}
	if dependencies.NewAutomationID == nil {
		dependencies.NewAutomationID = NewAutomationID
	}
	if dependencies.NewRunID == nil {
		dependencies.NewRunID = NewRunID
	}
	if dependencies.NewSkipID == nil {
		dependencies.NewSkipID = NewSkipID
	}
	if dependencies.NewCommandID == nil {
		dependencies.NewCommandID = devices.NewCommandID
	}
	if dependencies.NewCorrelationID == nil {
		dependencies.NewCorrelationID = devices.NewCorrelationID
	}
	return dependencies
}
