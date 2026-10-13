package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// runningEvidenceParent loads and validates the transaction-local immutable parent
// shared by reached delay and notification evidence operations.
func runningEvidenceParent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
) (automations.Run, error) {
	row, err := queries.GetRunEvidenceParent(ctx, dbsqlc.GetRunEvidenceParentParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Run{}, fmt.Errorf("%w: missing evidence parent", automations.ErrInvalidAutomation)
	}
	if err != nil {
		return automations.Run{}, err
	}
	if row.Kind != string(automations.HistoryRun) || row.RunStatus.String != string(automations.RunRunning) {
		return automations.Run{}, fmt.Errorf("%w: evidence parent is not running", automations.ErrInvalidAutomation)
	}
	return runFromRow(ctx, queries, row)
}

// startableEvidenceParent enforces sequential execution across all evidence kinds
// using the same transaction that reserves the next reached Step.
func startableEvidenceParent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
) (automations.Run, error) {
	run, err := runningEvidenceParent(ctx, queries, id)
	if err != nil {
		return automations.Run{}, err
	}
	for _, step := range run.Steps {
		if automations.StepAttemptStatus(step.State) == automations.StepRunning {
			return automations.Run{}, runningEvidenceConflict()
		}
	}
	for _, delay := range run.Delays {
		if delay.Status == automations.DelayRunning {
			return automations.Run{}, runningEvidenceConflict()
		}
	}
	for _, notification := range run.Notifications {
		if notification.Status == automations.NotificationRunning {
			return automations.Run{}, runningEvidenceConflict()
		}
	}
	return run, nil
}

func runningEvidenceConflict() error {
	return fmt.Errorf("%w: a Step is already running", automations.ErrInvalidAutomation)
}
