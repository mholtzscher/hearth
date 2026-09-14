package automations

import (
	"context"
	"log/slog"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// AutomationDevices is the read-only device seam the automations module consumes.
// It is defined here, by the consumer, and implemented by devices. Save-time
// validation proves current references; execution-time eligibility stays with
// the normal Command path.
type AutomationDevices interface {
	// ValidateObservationTrigger reports whether the Entity currently exists and
	// can be the source of an Observation Trigger.
	ValidateObservationTrigger(context.Context, devices.EntityID) error
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

// AutomationDefinitionRepository is the definition-management subset of
// [AutomationRepository]. It is the narrow seam a caller needs to manage
// definitions without admitting or executing Runs.
type AutomationDefinitionRepository interface {
	CreateAutomation(context.Context, AutomationDefinition) (AutomationRecord, error)
	GetAutomation(context.Context, AutomationID) (AutomationRecord, error)
	ListAutomations(context.Context, ListAutomationsParams) (AutomationPage[AutomationRecord], error)
	ReplaceAutomation(context.Context, AutomationID, int64, AutomationDefinition) (AutomationRecord, error)
	DeleteAutomation(context.Context, AutomationID, int64) error
}

// AutomationRepository is the complete domain-oriented persistence seam. The
// SQLite adapter implements every method; generated types never cross this seam.
type AutomationRepository interface {
	AutomationDefinitionRepository

	// AdmitDeviceFact evaluates one Device Fact against current enabled
	// definitions and commits every matching outcome in one transaction. It
	// registers no workers; the caller starts them only after the commit returns.
	AdmitDeviceFact(context.Context, DeviceFact, time.Time) (AdmissionResult, error)
	// AdmitManualRun starts one Run from the current definition snapshot even
	// when the Automation is disabled.
	AdmitManualRun(context.Context, AutomationID, time.Time) (AutomationRun, error)
	// MarkStepRunning persists one Step's reserved Command identities.
	MarkStepRunning(context.Context, StepStart) error
	// CompleteStep persists one Step's established terminal outcome.
	CompleteStep(context.Context, StepCompletion) error
	// CompleteRun persists one Run's established terminal state.
	CompleteRun(context.Context, RunCompletion) error
	// GetHistoryEntry reads one retained Run or Skip scoped to its Automation.
	GetHistoryEntry(context.Context, AutomationID, string) (AutomationHistoryEntry, error)
	// ListHistory pages retained Run and Skip summaries newest first.
	ListHistory(context.Context, ListHistoryParams) (AutomationPage[AutomationHistorySummary], error)
	// InterruptActiveRuns marks every running Step and Run as interrupted with
	// the supplied reason; it never replays or infers success.
	InterruptActiveRuns(context.Context, time.Time, string) error
	// DeleteHistoryBefore removes at most limit terminal history records older
	// than the cutoff without touching running Runs or matched-Fact receipts.
	DeleteHistoryBefore(context.Context, time.Time, int) (int64, error)
}

// AutomationDependencies carries the process-owned collaborators and identity
// constructors the module needs. Zero-valued fields fall back to production
// defaults, so focused tests override only what they observe.
type AutomationDependencies struct {
	Logger           *slog.Logger
	Now              func() time.Time
	NewAutomationID  func() (AutomationID, error)
	NewRunID         func() (AutomationRunID, error)
	NewSkipID        func() (AutomationSkipID, error)
	NewCommandID     func() (devices.CommandID, error)
	NewCorrelationID func() (devices.CorrelationID, error)
}

func (dependencies AutomationDependencies) withDefaults() AutomationDependencies {
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
		dependencies.NewRunID = NewAutomationRunID
	}
	if dependencies.NewSkipID == nil {
		dependencies.NewSkipID = NewAutomationSkipID
	}
	if dependencies.NewCommandID == nil {
		dependencies.NewCommandID = devices.NewCommandID
	}
	if dependencies.NewCorrelationID == nil {
		dependencies.NewCorrelationID = devices.NewCorrelationID
	}
	return dependencies
}
