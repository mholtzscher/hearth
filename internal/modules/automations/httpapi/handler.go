package httpapi

import (
	"context"
	"log/slog"

	"github.com/mholtzscher/hearth/internal/modules/automations"
)

// Automations exposes definition management, manual admission, and history to HTTP handlers.
type Automations interface {
	CreateAutomation(context.Context, automations.Definition) (automations.Record, error)
	GetAutomation(context.Context, automations.AutomationID) (automations.Record, error)
	ListAutomations(
		context.Context,
		automations.ListAutomationsParams,
	) (automations.Page[automations.Record], error)
	ReplaceAutomation(
		context.Context,
		automations.AutomationID,
		int64,
		automations.Definition,
	) (automations.Record, error)
	DeleteAutomation(context.Context, automations.AutomationID, int64) error
	StartManualRun(context.Context, automations.ManualRunInput) (automations.Run, error)
	GetHistoryEntry(context.Context, automations.AutomationID, string) (automations.HistoryEntry, error)
	ListHistory(
		context.Context,
		automations.ListHistoryParams,
	) (automations.Page[automations.HistorySummary], error)
}

// handler owns the automation HTTP operations.
type handler struct {
	automations Automations
	logger      *slog.Logger
}
