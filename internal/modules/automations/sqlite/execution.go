package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// MarkStepRunning durably reserves one Step's Command identity before the
// external call. The update only matches a still not-attempted Step.
func (repo *AutomationRepository) MarkStepRunning(
	ctx context.Context,
	start automations.StepStart,
) error {
	if _, err := automations.ParseRunID(string(start.RunID)); err != nil {
		return err
	}
	if _, err := devices.ParseCommandID(string(start.CommandID)); err != nil {
		return fmt.Errorf("%w: reserved command ID: %w", automations.ErrInvalidAutomation, err)
	}
	if _, err := devices.ParseCorrelationID(string(start.CorrelationID)); err != nil {
		return fmt.Errorf("%w: reserved correlation ID: %w", automations.ErrInvalidAutomation, err)
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		updated, err := queries.MarkStepRunning(ctx, dbsqlc.MarkStepRunningParams{
			ReservedCommandID:     sql.NullString{String: string(start.CommandID), Valid: true},
			ReservedCorrelationID: sql.NullString{String: string(start.CorrelationID), Valid: true},
			StartedAt:             sql.NullString{String: encodeAutomationTimestamp(repo.now()), Valid: true},
			RunID:                 string(start.RunID),
			Position:              int64(start.Position),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf(
				"%w: step %s/%d is not startable",
				automations.ErrInvalidAutomation, start.RunID, start.Position,
			)
		}
		return nil
	})
}

// CompleteStep records a terminal outcome. If the Step never started, its
// started_at is set to the completion time.
func (repo *AutomationRepository) CompleteStep(
	ctx context.Context,
	completion automations.StepCompletion,
) error {
	if _, err := automations.ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if err := automations.ValidateStepCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		completedAt := encodeAutomationTimestamp(repo.now())
		updated, err := queries.CompleteStep(ctx, dbsqlc.CompleteStepParams{
			Status:            string(completion.Status),
			VerifiedCommandID: encodeNullableString(commandIDString(completion.VerifiedCommandID)),
			FailureCode:       encodeNullableString(completion.FailureCode),
			StartedAt:         sql.NullString{String: completedAt, Valid: true},
			CompletedAt:       sql.NullString{String: completedAt, Valid: true},
			RunID:             string(completion.RunID),
			Position:          int64(completion.Position),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf(
				"%w: step %s/%d is not completable",
				automations.ErrInvalidAutomation, completion.RunID, completion.Position,
			)
		}
		return nil
	})
}

// CompleteRun records one Run's established terminal state exactly once.
func (repo *AutomationRepository) CompleteRun(
	ctx context.Context,
	completion automations.RunCompletion,
) error {
	if _, err := automations.ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if err := automations.ValidateRunCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		updated, err := queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
			RunStatus:      sql.NullString{String: string(completion.Status), Valid: true},
			RunFailureCode: encodeNullableString(completion.FailureCode),
			RunCompletedAt: sql.NullString{String: encodeAutomationTimestamp(repo.now()), Valid: true},
			ID:             string(completion.RunID),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf(
				"%w: run %s is not completable", automations.ErrInvalidAutomation, completion.RunID,
			)
		}
		return nil
	})
}
