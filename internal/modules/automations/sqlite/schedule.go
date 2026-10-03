package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// InitializeScheduleWatermark consumes the activation minute without outcomes.
func (repo *AutomationRepository) InitializeScheduleWatermark(ctx context.Context, at time.Time) error {
	if at.IsZero() {
		return fmt.Errorf("%w: schedule activation time is required", automations.ErrInvalidAutomation)
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if _, err := scheduleWatermark(
			ctx,
			queries,
		); err != nil &&
			!errors.Is(err, automations.ErrAdmissionUnavailable) {
			return err
		}
		return queries.AdvanceScheduleWatermark(ctx, dbsqlc.AdvanceScheduleWatermarkParams{
			HighwaterAt: encodeAutomationTimestamp(at.UTC().Truncate(time.Minute)),
		})
	})
}

// AdmitDueSchedules commits current-minute decisions and progress together. No
// historical minute is inspected, even after a stall or a backward clock jump.
func (repo *AutomationRepository) AdmitDueSchedules(
	ctx context.Context, snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick,
) (automations.AdmissionResult, error) {
	if tick.At.IsZero() || tick.Location == nil {
		return automations.AdmissionResult{}, fmt.Errorf(
			"%w: schedule time and household location are required", automations.ErrInvalidAutomation,
		)
	}
	minute := tick.At.UTC().Truncate(time.Minute)
	var result automations.AdmissionResult
	err := repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		watermark, err := scheduleWatermark(ctx, queries)
		if err != nil || !minute.After(watermark) {
			return err
		}
		rows, err := queries.ListAllAutomations(ctx)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if err = repo.admitScheduleDefinition(ctx, queries, row, snapshot, tick, &result); err != nil {
				return err
			}
		}
		return queries.AdvanceScheduleWatermark(ctx, dbsqlc.AdvanceScheduleWatermarkParams{
			HighwaterAt: encodeAutomationTimestamp(minute),
		})
	})
	if err != nil {
		return automations.AdmissionResult{}, err
	}
	return result, nil
}

// admitScheduleDefinition checks the transaction's current revision before matching.
func (repo *AutomationRepository) admitScheduleDefinition(
	ctx context.Context, queries *dbsqlc.Queries, row dbsqlc.Automation,
	snapshot devices.EntityStateSnapshot, tick automations.ScheduleTick, result *automations.AdmissionResult,
) error {
	record, err := automationRecord(row)
	if err != nil {
		return err
	}
	minute := tick.At.UTC().Truncate(time.Minute)
	if !record.Definition.Enabled || !minute.After(record.UpdatedAt) {
		return nil
	}
	matched, err := automations.MatchPreparedScheduledTriggers(record.Definition, minute, tick.Location)
	if err != nil || len(matched) == 0 {
		return err
	}
	return repo.admitSchedule(ctx, queries, record, matched, snapshot, tick.At, result)
}

func scheduleWatermark(ctx context.Context, queries *dbsqlc.Queries) (time.Time, error) {
	row, err := queries.GetScheduleWatermark(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, automations.ErrAdmissionUnavailable
	}
	if err != nil {
		return time.Time{}, err
	}
	at, err := decodeAutomationTimestamp(row)
	if err != nil || at.IsZero() || !at.Equal(at.Truncate(time.Minute)) {
		return time.Time{}, fmt.Errorf(
			"%w: invalid stored schedule watermark %q",
			automations.ErrInvalidAutomation,
			row,
		)
	}
	return at, nil
}

func (repo *AutomationRepository) admitSchedule(
	ctx context.Context, queries *dbsqlc.Queries, record automations.Record,
	matched []automations.TriggerID, snapshot devices.EntityStateSnapshot, at time.Time,
	result *automations.AdmissionResult,
) error {
	running, err := queries.CountRunningRuns(ctx, dbsqlc.CountRunningRunsParams{AutomationID: string(record.ID)})
	if err != nil {
		return err
	}
	decision, reason, err := automaticConditionDecision(record.Definition.Conditions, running, snapshot, at)
	if err != nil {
		return err
	}
	result.Outcome.MatchedAutomations++
	if reason == "" {
		runID, idErr := repo.newRunID()
		if idErr != nil {
			return fmt.Errorf("allocate automation run ID: %w", idErr)
		}
		run := automations.NewRunSnapshot(record, runID, automations.RunSourceSchedule, nil, matched, decision, at)
		if err = repo.persistRun(ctx, queries, run); err != nil {
			return err
		}
		result.StartedRuns = append(result.StartedRuns, run)
		result.Outcome.StartedRuns++
		return nil
	}
	skipID, err := repo.newSkipID()
	if err != nil {
		return fmt.Errorf("allocate automation skip ID: %w", err)
	}
	triggers, err := automations.MatchedTriggerSnapshots(record.Definition, matched)
	if err != nil {
		return err
	}
	skip := automations.Skip{
		ID: skipID, AutomationID: record.ID, AutomationName: record.Definition.Name,
		Revision: record.Revision, Source: automations.RunSourceSchedule, MatchedTriggers: triggers,
		Reason: reason, ConditionDecision: decision, SkippedAt: at.UTC(),
	}
	if err = repo.persistHistorySkip(ctx, queries, skip); err != nil {
		return err
	}
	result.Skips = append(result.Skips, automations.AdmissionSkip{
		SkipID: skipID, AutomationID: record.ID, Revision: record.Revision,
		Source: automations.RunSourceSchedule, Reason: reason,
	})
	result.Outcome.RecordedSkips++
	return nil
}
