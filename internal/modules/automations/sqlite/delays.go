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

// RecordDelayStart validates against the immutable snapshot and appends once.
func (repo *AutomationRepository) RecordDelayStart(ctx context.Context, start automations.DelayStart) error {
	if err := automations.ValidateDelayStart(start); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		run, err := runningDelayParent(ctx, queries, start.RunID)
		if err != nil {
			return err
		}
		if _, err = automations.DelayDurationWithPreparedSnapshot(run.Snapshot, start.StepID); err != nil {
			return err
		}
		if start.Position != len(run.Delays) {
			return fmt.Errorf("%w: delay position is not appendable", automations.ErrInvalidAutomation)
		}
		for _, delay := range run.Delays {
			if delay.StepID == start.StepID || delay.Status == automations.DelayRunning {
				return fmt.Errorf("%w: delay is already reached or a wait is running", automations.ErrInvalidAutomation)
			}
		}
		return queries.CreateRunDelay(ctx, dbsqlc.CreateRunDelayParams{
			RunID: string(start.RunID), StepID: string(start.StepID), Position: int64(start.Position),
			StartedAt: encodeAutomationTimestamp(start.StartedAt),
		})
	})
}

// CompleteDelay commits established terminal evidence without retries. An
// interruption ends the wait and its parent in the same transaction.
func (repo *AutomationRepository) CompleteDelay(ctx context.Context, completion automations.DelayCompletion) error {
	if err := automations.ValidateDelayCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if _, err := runningDelayParent(ctx, queries, completion.RunID); err != nil {
			return err
		}
		at := sql.NullString{String: encodeAutomationTimestamp(completion.CompletedAt), Valid: true}
		updated, err := queries.CompleteRunDelay(ctx, dbsqlc.CompleteRunDelayParams{
			RunID: string(completion.RunID), StepID: string(completion.StepID), Status: string(completion.Status),
			CompletedAt: at, FailureCode: encodeNullableString(completion.FailureCode),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("%w: delay is not completable", automations.ErrInvalidAutomation)
		}
		if completion.Status == automations.DelayCompleted {
			return nil
		}
		updated, err = queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
			ID:             string(completion.RunID),
			RunStatus:      sql.NullString{String: string(automations.RunInterrupted), Valid: true},
			RunCompletedAt: at,
			RunFailureCode: encodeNullableString(completion.FailureCode),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return fmt.Errorf("%w: delay parent is not completable", automations.ErrInvalidAutomation)
		}
		return nil
	})
}

func runningDelayParent(ctx context.Context, queries *dbsqlc.Queries, id automations.RunID) (automations.Run, error) {
	row, err := queries.GetDelayParent(ctx, dbsqlc.GetDelayParentParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Run{}, fmt.Errorf("%w: missing delay parent", automations.ErrInvalidAutomation)
	}
	if err != nil {
		return automations.Run{}, err
	}
	if row.Kind != string(automations.HistoryRun) || row.RunStatus.String != string(automations.RunRunning) {
		return automations.Run{}, fmt.Errorf("%w: delay parent is not running", automations.ErrInvalidAutomation)
	}
	return runFromRow(ctx, queries, row)
}

// runDelays projects retained rows using the snapshot already decoded for this
// Run. Corrupt evidence never falls back to a current definition.
func runDelays(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.Run,
) ([]automations.DelayExecution, error) {
	rows, err := queries.ListRunDelays(ctx, dbsqlc.ListRunDelaysParams{RunID: string(run.ID)})
	if err != nil {
		return nil, err
	}
	delays := make([]automations.DelayExecution, 0, len(rows))
	for position, row := range rows {
		delay, decodeErr := delayFromRow(row, run)
		if decodeErr != nil {
			return nil, fmt.Errorf("stored delay %s/%d: %w", run.ID, row.Position, decodeErr)
		}
		if delay.Position != position || (delay.Status == automations.DelayRunning &&
			(automations.RunStateStatus(run.State) != automations.RunRunning || position != len(rows)-1)) {
			return nil, fmt.Errorf(
				"%w: stored delay order or parent status disagrees",
				automations.ErrInvalidAutomation,
			)
		}
		delays = append(delays, delay)
	}
	return delays, nil
}

func delayFromRow(row dbsqlc.AutomationRunDelay, run automations.Run) (automations.DelayExecution, error) {
	startedAt, err := decodeAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.DelayExecution{}, err
	}
	start := automations.DelayStart{
		RunID:     run.ID,
		StepID:    automations.StepID(row.StepID),
		Position:  int(row.Position),
		StartedAt: startedAt,
	}
	if err = automations.ValidateDelayStart(start); err != nil {
		return automations.DelayExecution{}, err
	}
	duration, err := automations.DelayDurationWithPreparedSnapshot(run.Snapshot, start.StepID)
	if err != nil {
		return automations.DelayExecution{}, err
	}
	completedAt, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.DelayExecution{}, err
	}
	delay := automations.DelayExecution{
		StepID:      start.StepID,
		Position:    start.Position,
		DurationMS:  duration,
		Status:      automations.DelayStatus(row.Status),
		StartedAt:   startedAt,
		DueAt:       startedAt.Add(time.Duration(duration) * time.Millisecond),
		CompletedAt: completedAt,
		FailureCode: stringPointer(row.FailureCode),
	}
	if delay.Status == automations.DelayRunning {
		if completedAt != nil || delay.FailureCode != nil {
			return automations.DelayExecution{}, fmt.Errorf(
				"%w: running delay has terminal fields",
				automations.ErrInvalidAutomation,
			)
		}
	} else {
		if completedAt == nil {
			return automations.DelayExecution{}, fmt.Errorf(
				"%w: terminal delay lacks completion time",
				automations.ErrInvalidAutomation,
			)
		}
		if err = automations.ValidateDelayCompletion(automations.DelayCompletion{
			RunID:       run.ID,
			StepID:      delay.StepID,
			Status:      delay.Status,
			CompletedAt: *completedAt,
			FailureCode: delay.FailureCode,
		}); err != nil {
			return automations.DelayExecution{}, err
		}
	}
	return delay, nil
}
