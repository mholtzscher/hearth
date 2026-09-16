package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// GetHistoryEntry reads one retained Run or Skip scoped to its former Automation;
// a parent mismatch or unknown identity is [automations.ErrHistoryNotFound].
func (repo *AutomationRepository) GetHistoryEntry(
	ctx context.Context,
	automationID automations.AutomationID,
	entryID string,
) (automations.HistoryEntry, error) {
	var entry automations.HistoryEntry
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetHistoryEntry(ctx, dbsqlc.GetHistoryEntryParams{
			AutomationID: string(automationID),
			ID:           entryID,
		})
		if errors.Is(err, sql.ErrNoRows) {
			return automations.ErrHistoryNotFound
		}
		if err != nil {
			return err
		}
		entry, err = historyEntry(ctx, queries, row)
		return err
	})
	if err != nil {
		return automations.HistoryEntry{}, err
	}
	return entry, nil
}

// ListHistory pages retained Run and Skip summaries newest first for one Automation.
func (repo *AutomationRepository) ListHistory(
	ctx context.Context,
	params automations.ListHistoryParams,
) (automations.Page[automations.HistorySummary], error) {
	page := automations.Page[automations.HistorySummary]{
		Items: []automations.HistorySummary{},
	}
	limit, err := automations.PageLimit(params.Limit)
	if err != nil {
		return page, err
	}
	if (params.BeforeRecordedAt == nil) != (params.BeforeID == nil) {
		return page, fmt.Errorf("%w: history cursor requires a time and an ID", automations.ErrInvalidAutomation)
	}
	var rows []dbsqlc.AutomationHistory
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		var queryErr error
		if params.BeforeRecordedAt == nil {
			rows, queryErr = queries.ListHistoryFirstPage(ctx, dbsqlc.ListHistoryFirstPageParams{
				AutomationID: string(params.AutomationID),
				Limit:        int64(limit + 1),
			})
		} else {
			recordedAt := encodeAutomationTimestamp(*params.BeforeRecordedAt)
			rows, queryErr = queries.ListHistoryAfter(ctx, dbsqlc.ListHistoryAfterParams{
				AutomationID: string(params.AutomationID),
				RecordedAt:   recordedAt,
				RecordedAt_2: recordedAt,
				ID:           *params.BeforeID,
				Limit:        int64(limit + 1),
			})
		}
		return queryErr
	})
	if err != nil {
		return page, err
	}
	if len(rows) > limit {
		page.HasMore = true
		rows = rows[:limit]
	}
	for _, row := range rows {
		summary, summaryErr := historySummary(row)
		if summaryErr != nil {
			return page, summaryErr
		}
		page.Items = append(page.Items, summary)
	}
	return page, nil
}

// InterruptActiveRuns marks every running Step and Run as interrupted with the supplied reason.
func (repo *AutomationRepository) InterruptActiveRuns(
	ctx context.Context,
	at time.Time,
	reason string,
) error {
	if at.IsZero() {
		return fmt.Errorf("%w: interruption time is required", automations.ErrInvalidAutomation)
	}
	if reason == "" {
		return fmt.Errorf("%w: interruption reason is required", automations.ErrInvalidAutomation)
	}
	completedAt := sql.NullString{String: encodeAutomationTimestamp(at), Valid: true}
	failureCode := sql.NullString{String: reason, Valid: true}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if _, err := queries.InterruptRunningRuns(ctx, dbsqlc.InterruptRunningRunsParams{
			RunFailureCode: failureCode,
			RunCompletedAt: completedAt,
		}); err != nil {
			return err
		}
		if _, err := queries.InterruptRunningSteps(ctx, dbsqlc.InterruptRunningStepsParams{
			FailureCode: failureCode,
			CompletedAt: completedAt,
		}); err != nil {
			return err
		}
		return nil
	})
}

// DeleteHistoryBefore removes at most limit terminal history records older than
// the cutoff in one transaction, never selecting running Runs.
func (repo *AutomationRepository) DeleteHistoryBefore(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	if cutoff.IsZero() {
		return 0, fmt.Errorf("%w: retention cutoff is required", automations.ErrInvalidAutomation)
	}
	if limit < 1 {
		return 0, fmt.Errorf("%w: retention batch must be positive", automations.ErrInvalidAutomation)
	}
	var deleted int64
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		count, err := queries.DeleteHistoryBefore(ctx, dbsqlc.DeleteHistoryBeforeParams{
			RecordedAt: encodeAutomationTimestamp(cutoff),
			Limit:      int64(limit),
		})
		if err != nil {
			return err
		}
		deleted = count
		return nil
	})
	return deleted, err
}
