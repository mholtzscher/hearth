package automations

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// MinimumAutomationHistoryRetention is the shortest Automation history retention
// the automations module will prune with. It matches the application's
// `automation_history_retention` configuration floor and keeps pruned history
// above the seven-day Device Fact retention so retained evidence stays
// explainable. A shorter or unconfigured retention makes PruneHistory fail
// instead of deleting too much.
const MinimumAutomationHistoryRetention = 8 * 24 * time.Hour

// automationHistoryPruneBatch bounds one retention transaction so hourly
// maintenance never holds a long write lock.
const automationHistoryPruneBatch = 500

// GetHistoryEntry reads one retained Run or Skip scoped to its former
// Automation. It stays queryable after the definition is hard-deleted.
func (service *Service) GetHistoryEntry(
	ctx context.Context,
	automationID AutomationID,
	entryID string,
) (HistoryEntry, error) {
	if _, err := ParseAutomationID(string(automationID)); err != nil {
		return HistoryEntry{}, err
	}
	return service.repository.GetHistoryEntry(ctx, automationID, entryID)
}

// ListHistory pages retained Run and Skip summaries newest first for one
// Automation, including one that has been hard-deleted.
func (service *Service) ListHistory(
	ctx context.Context,
	params ListHistoryParams,
) (Page[HistorySummary], error) {
	if _, err := ParseAutomationID(string(params.AutomationID)); err != nil {
		return Page[HistorySummary]{}, err
	}
	return service.repository.ListHistory(ctx, params)
}

// PruneHistory deletes terminal Runs and Skips older than the cutoff derived
// from the injected Dependencies.HistoryRetention window. Deletion
// runs in batches of automationHistoryPruneBatch so one pass never holds a long
// write lock, and the loop rechecks cancellation between batches. Running Runs
// are never selected, and matched-Fact receipts are retained for
// deduplication.
//
// The sweep time must be non-zero and the injected retention at least
// MinimumAutomationHistoryRetention; otherwise nothing is deleted and the
// misconfiguration is reported. The caller owns the schedule and the failure
// logging.
func (service *Service) PruneHistory(ctx context.Context, now time.Time) error {
	if now.IsZero() {
		return errors.New("automation history prune time is required")
	}
	retention := service.dependencies.HistoryRetention
	if retention < MinimumAutomationHistoryRetention {
		return fmt.Errorf(
			"automation history retention %s is below the minimum %s",
			retention, MinimumAutomationHistoryRetention,
		)
	}
	cutoff := now.UTC().Add(-retention)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		deleted, err := service.repository.DeleteHistoryBefore(
			ctx, cutoff, automationHistoryPruneBatch,
		)
		if err != nil {
			return err
		}
		if deleted < automationHistoryPruneBatch {
			return nil
		}
	}
}
