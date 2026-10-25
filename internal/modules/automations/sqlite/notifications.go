package sqlite

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/modules/notifications"
)

// RecordNotificationStart appends a reached reservation under its immutable running parent.
func (repo *AutomationRepository) RecordNotificationStart(
	ctx context.Context,
	start automations.NotificationStart,
) error {
	if err := automations.ValidateNotificationStart(start); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		run, err := startableEvidenceParent(ctx, queries, start.RunID)
		if err != nil {
			return err
		}
		if _, err = automations.NotificationWithPreparedSnapshot(run.Snapshot, start.StepID); err != nil {
			return err
		}
		if start.Position != len(run.Notifications) {
			return notificationCorruption("position is not appendable")
		}
		for _, evidence := range run.Notifications {
			if evidence.StepID == start.StepID || evidence.Status != automations.NotificationAccepted {
				return notificationCorruption("prior notification is not accepted or Step already reached")
			}
		}
		if start.Inputs.StepID == nil || *start.Inputs.StepID != start.StepID {
			return dataCorruption("notification input ownership disagrees")
		}
		if _, ok := start.Inputs.Body.(automations.InputsResolved); !ok {
			return dataCorruption("notification requires resolved inputs")
		}
		if err = appendRunData(ctx, queries, run, start.Inputs, false); err != nil {
			return err
		}
		return queries.CreateRunNotification(ctx, dbsqlc.CreateRunNotificationParams{
			RunID: string(start.RunID), StepID: string(start.StepID), Position: int64(start.Position),
			AttemptID: string(start.AttemptID), StartedAt: encodeAutomationTimestamp(start.StartedAt),
		})
	})
}

// CompleteNotification verifies the reservation and commits evidence plus any failed parent together.
func (repo *AutomationRepository) CompleteNotification(
	ctx context.Context,
	completion automations.NotificationCompletion,
) error {
	if err := automations.ValidateNotificationCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if _, err := runningEvidenceParent(ctx, queries, completion.RunID); err != nil {
			return err
		}
		at := sql.NullString{String: encodeAutomationTimestamp(completion.CompletedAt), Valid: true}
		outcome := sql.NullString{}
		if completion.Outcome != nil {
			outcome = sql.NullString{String: string(*completion.Outcome), Valid: true}
		}
		updated, err := queries.CompleteRunNotification(ctx, dbsqlc.CompleteRunNotificationParams{
			RunID:     string(completion.RunID),
			StepID:    string(completion.StepID),
			AttemptID: string(completion.AttemptID),
			Status: string(
				completion.Status,
			),
			CompletedAt: at,
			Outcome:     outcome,
			ReasonCode:  encodeNullableString(completion.ReasonCode),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return notificationCorruption("reservation is not completable")
		}
		if completion.Status == automations.NotificationAccepted {
			return nil
		}
		status := automations.RunFailed
		if completion.Status == automations.NotificationInterrupted {
			status = automations.RunInterrupted
		}
		updated, err = queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
			ID: string(completion.RunID), RunStatus: sql.NullString{String: string(status), Valid: true},
			RunCompletedAt: at, RunFailureCode: encodeNullableString(completion.ReasonCode),
		})
		if err != nil {
			return err
		}
		if updated != 1 {
			return notificationCorruption("parent is not completable")
		}
		return nil
	})
}

func runNotifications(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.Run,
) ([]automations.NotificationExecution, error) {
	rows, err := queries.ListRunNotifications(ctx, dbsqlc.ListRunNotificationsParams{RunID: string(run.ID)})
	if err != nil {
		return nil, err
	}
	result := make([]automations.NotificationExecution, 0, len(rows))
	for position, row := range rows {
		evidence, decodeErr := notificationFromRow(row, run)
		if decodeErr != nil {
			return nil, decodeErr
		}
		if evidence.Position != position ||
			(position < len(rows)-1 && evidence.Status != automations.NotificationAccepted) {
			return nil, notificationCorruption("order disagrees")
		}
		result = append(result, evidence)
	}
	return result, nil
}

func notificationFromRow(
	row dbsqlc.AutomationRunNotification,
	run automations.Run,
) (automations.NotificationExecution, error) {
	startedAt, err := decodeAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.NotificationExecution{}, err
	}
	start := automations.NotificationStart{
		RunID:     run.ID,
		StepID:    automations.StepID(row.StepID),
		Position:  int(row.Position),
		AttemptID: notifications.AttemptID(row.AttemptID),
		StartedAt: startedAt,
	}
	if err = automations.ValidateNotificationStart(start); err != nil {
		return automations.NotificationExecution{}, err
	}
	if _, err = automations.NotificationWithPreparedSnapshot(run.Snapshot, start.StepID); err != nil {
		return automations.NotificationExecution{}, err
	}
	completedAt, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.NotificationExecution{}, err
	}
	evidence := automations.NotificationExecution{
		StepID:    start.StepID,
		Position:  start.Position,
		AttemptID: start.AttemptID,
		Status: automations.NotificationStatus(
			row.Status,
		),
		StartedAt:   startedAt,
		CompletedAt: completedAt,
		ReasonCode:  stringPointer(row.ReasonCode),
	}
	if row.Outcome.Valid {
		outcome := notifications.Outcome(row.Outcome.String)
		evidence.Outcome = &outcome
	}
	if evidence.Status == automations.NotificationRunning {
		if completedAt != nil || evidence.Outcome != nil || evidence.ReasonCode != nil {
			return automations.NotificationExecution{}, notificationCorruption("running evidence has terminal fields")
		}
	} else {
		if completedAt == nil {
			return automations.NotificationExecution{}, notificationCorruption(
				"terminal evidence lacks completion time",
			)
		}
		if err = automations.ValidateNotificationCompletion(automations.NotificationCompletion{
			RunID: run.ID, StepID: evidence.StepID, AttemptID: evidence.AttemptID, Status: evidence.Status,
			CompletedAt: *completedAt, Outcome: evidence.Outcome, ReasonCode: evidence.ReasonCode,
		}); err != nil {
			return automations.NotificationExecution{}, err
		}
	}
	return evidence, nil
}

func notificationCorruption(message string) error {
	return fmt.Errorf("%w: notification %s", automations.ErrInvalidAutomation, message)
}
