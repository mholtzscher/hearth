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

// Dependencies supplies logging, time, and identity constructors; zero-valued
// fields use production defaults except HistoryRetention.
type Dependencies struct {
	Logger *slog.Logger
	Now    func() time.Time
	// HeldStateStartupAt is the Core startup cutoff for buffered held-state Facts.
	// When unset, NewService uses Now at construction time.
	HeldStateStartupAt time.Time
	NewAutomationID    func() (AutomationID, error)
	NewRunID           func() (RunID, error)
	NewSkipID          func() (SkipID, error)
	NewCommandID       func() (devices.CommandID, error)
	NewCorrelationID   func() (devices.CorrelationID, error)
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
