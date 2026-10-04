package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// MarkStepRunning durably reserves one Step's Command identity before the external call.
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

// CompleteStep records a terminal outcome, setting started_at when the Step never started.
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
	verified, failure := automations.StepOutcomeEvidence(completion.Outcome)
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if err := verifyStepReservation(
			ctx, queries, completion.RunID, completion.Position, verified,
			automations.StepOutcomeStatus(completion.Outcome) != automations.StepInterrupted,
		); err != nil {
			return err
		}
		completedAt := encodeAutomationTimestamp(repo.now())
		updated, err := queries.CompleteStep(ctx, dbsqlc.CompleteStepParams{
			Status:            string(automations.StepOutcomeStatus(completion.Outcome)),
			VerifiedCommandID: encodeNullableString(commandIDString(verified)),
			FailureCode:       encodeNullableString(failure),
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

func verifyStepReservation(
	ctx context.Context,
	queries *dbsqlc.Queries,
	runID automations.RunID,
	position int,
	verified *devices.CommandID,
	requireReservation bool,
) error {
	rows, err := queries.ListRunSteps(ctx, dbsqlc.ListRunStepsParams{RunID: string(runID)})
	if err != nil {
		return err
	}
	for _, row := range rows {
		if row.Position != int64(position) {
			continue
		}
		reservation, parseErr := reservationFromColumns(row)
		if parseErr != nil {
			return parseErr
		}
		if requireReservation && reservation == nil {
			return fmt.Errorf("%w: Step completion requires a reservation", automations.ErrInvalidAutomation)
		}
		if verified != nil && (reservation == nil || reservation.CommandID != *verified) {
			return fmt.Errorf("%w: verified Command does not match Step reservation", automations.ErrInvalidAutomation)
		}
		return nil
	}
	return fmt.Errorf("%w: Step %s/%d not found", automations.ErrInvalidAutomation, runID, position)
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
			RunStatus: sql.NullString{
				String: string(automations.RunOutcomeStatus(completion.Outcome)),
				Valid:  true,
			},
			RunFailureCode: encodeNullableString(automations.RunOutcomeFailureCode(completion.Outcome)),
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
