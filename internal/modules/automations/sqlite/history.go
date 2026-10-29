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

// historyEntry reads one full Run or Skip, including the ordered Steps of a Run.
func historyEntry(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.HistoryEntry, error) {
	switch automations.HistoryKind(row.Kind) {
	case automations.HistoryRun:
		run, err := readRunHistory(ctx, queries, row)
		if err != nil {
			return nil, err
		}
		return run, nil
	case automations.HistorySkip:
		skip, err := skipFromRow(row)
		if err != nil {
			return nil, err
		}
		return skip, nil
	default:
		return nil, storedDataErrorf("stored history %q has unknown kind %q", row.ID, row.Kind)
	}
}

func readRunHistory(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.Run, error) {
	runID, automationID, err := runIdentityFromRow(row)
	if err != nil {
		return automations.Run{}, err
	}
	rawSnapshot, err := queries.GetDefinitionRevision(ctx, dbsqlc.GetDefinitionRevisionParams{
		AutomationID: row.AutomationID, Revision: row.Revision,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Run{}, storedDataErrorf("stored Run %q is missing definition revision %d",
			row.ID, row.Revision)
	}
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q definition revision: %w", row.ID, err)
	}
	run, err := runFromStoredDefinition(row, runID, automationID, rawSnapshot)
	if err != nil {
		return automations.Run{}, err
	}
	executions, err := runExecutions(ctx, queries, row.ID)
	if err != nil {
		return automations.Run{}, err
	}
	if err = applyRunValueHistory(row, &run, executions); err != nil {
		return automations.Run{}, err
	}
	return run, nil
}

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
		return nil, err
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
	var rows []historySummaryRow
	err = repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		var queryErr error
		if params.BeforeRecordedAt == nil {
			pageRows, listErr := queries.ListHistoryFirstPage(ctx, dbsqlc.ListHistoryFirstPageParams{
				AutomationID: string(params.AutomationID),
				Limit:        int64(limit + 1),
			})
			queryErr = listErr
			rows = make([]historySummaryRow, len(pageRows))
			for i, row := range pageRows {
				rows[i] = historySummaryRow(row)
			}
		} else {
			recordedAt := encodeAutomationTimestamp(*params.BeforeRecordedAt)
			pageRows, listErr := queries.ListHistoryAfter(ctx, dbsqlc.ListHistoryAfterParams{
				AutomationID: string(params.AutomationID),
				RecordedAt:   recordedAt,
				ID:           *params.BeforeID,
				PageLimit:    int64(limit + 1),
			})
			queryErr = listErr
			rows = make([]historySummaryRow, len(pageRows))
			for i, row := range pageRows {
				rows[i] = historySummaryRow(row)
			}
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
		summary, summaryErr := historySummaryFromRow(row)
		if summaryErr != nil {
			return page, summaryErr
		}
		page.Items = append(page.Items, summary)
	}
	return page, nil
}

// DeleteHistoryBefore removes at most limit terminal history records older than
// the cutoff in one transaction, never selecting running Runs.
func (repo *AutomationRepository) DeleteHistoryBefore(
	ctx context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	if limit < 1 {
		return 0, fmt.Errorf("%w: retention batch must be positive", automations.ErrInvalidAutomation)
	}
	if cutoff.IsZero() {
		return 0, errors.New("automation retention cutoff is required")
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
		return queries.DeleteUnreferencedDefinitionRevisions(ctx, dbsqlc.DeleteUnreferencedDefinitionRevisionsParams{
			Limit: int64(limit),
		})
	})
	return deleted, err
}
