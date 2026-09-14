package automations

import (
	"context"
	"log/slog"
	"time"
)

// automationHistoryPruneBatch bounds one retention transaction so hourly
// maintenance never holds a long write lock.
const automationHistoryPruneBatch = 500

// GetHistoryEntry reads one retained Run or Skip scoped to its former
// Automation. It stays queryable after the definition is hard-deleted.
func (service *Service) GetHistoryEntry(
	ctx context.Context,
	automationID AutomationID,
	entryID string,
) (AutomationHistoryEntry, error) {
	if _, err := ParseAutomationID(string(automationID)); err != nil {
		return AutomationHistoryEntry{}, err
	}
	return service.repository.GetHistoryEntry(ctx, automationID, entryID)
}

// ListHistory pages retained Run and Skip summaries newest first for one
// Automation, including one that has been hard-deleted.
func (service *Service) ListHistory(
	ctx context.Context,
	params ListHistoryParams,
) (AutomationPage[AutomationHistorySummary], error) {
	if _, err := ParseAutomationID(string(params.AutomationID)); err != nil {
		return AutomationPage[AutomationHistorySummary]{}, err
	}
	return service.repository.ListHistory(ctx, params)
}

// PruneHistory deletes terminal Runs and Skips older than the cutoff in bounded
// batches and returns how many records were removed. Running Runs are never
// selected, and matched-Fact receipts are retained for deduplication.
func (service *Service) PruneHistory(ctx context.Context, cutoff time.Time, batch int) (int64, error) {
	if batch < 1 {
		batch = automationHistoryPruneBatch
	}
	var total int64
	for {
		deleted, err := service.repository.DeleteHistoryBefore(ctx, cutoff, batch)
		if err != nil {
			service.logPruneFailure(ctx)
			return total, err
		}
		total += deleted
		if deleted < int64(batch) {
			return total, nil
		}
	}
}

// logPruneFailure records one failed hourly retention pass with a fixed event.
// It never logs the upstream error text.
func (service *Service) logPruneFailure(ctx context.Context) {
	service.dependencies.Logger.ErrorContext(
		ctx,
		"automation history prune failed",
		slog.String("event", "core.automation_history_prune_failed"),
		slog.String("error_code", "automation_history_prune_failed"),
	)
}
