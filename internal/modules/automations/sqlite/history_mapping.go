package sqlite

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// historyEntry reads one full Run or Skip, including the ordered Steps of a Run.
func historyEntry(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.AutomationHistoryEntry, error) {
	switch automations.AutomationHistoryKind(row.Kind) {
	case automations.AutomationHistoryRun:
		run, err := runFromRow(ctx, queries, row)
		if err != nil {
			return automations.AutomationHistoryEntry{}, err
		}
		return automations.AutomationHistoryEntry{Kind: automations.AutomationHistoryRun, Run: &run}, nil
	case automations.AutomationHistorySkip:
		skip, err := skipFromRow(row)
		if err != nil {
			return automations.AutomationHistoryEntry{}, err
		}
		return automations.AutomationHistoryEntry{Kind: automations.AutomationHistorySkip, Skip: &skip}, nil
	default:
		return automations.AutomationHistoryEntry{}, fmt.Errorf(
			"%w: stored history %q has unknown kind %q",
			automations.ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
}

func historySummary(row dbsqlc.AutomationHistory) (automations.AutomationHistorySummary, error) {
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.AutomationHistorySummary{}, fmt.Errorf("stored history %q: %w", row.ID, err)
	}
	if row.Revision < 1 {
		return automations.AutomationHistorySummary{}, fmt.Errorf(
			"%w: stored history %q revision %d", automations.ErrInvalidAutomation, row.ID, row.Revision,
		)
	}
	recordedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.AutomationHistorySummary{}, fmt.Errorf("stored history %q recorded_at: %w", row.ID, err)
	}
	summary := automations.AutomationHistorySummary{
		ID:             row.ID,
		Kind:           automations.AutomationHistoryKind(row.Kind),
		AutomationID:   automationID,
		AutomationName: row.AutomationName,
		Revision:       row.Revision,
		RecordedAt:     recordedAt,
	}
	switch summary.Kind {
	case automations.AutomationHistoryRun:
		if !row.RunStatus.Valid {
			return summary, fmt.Errorf(
				"%w: stored Run %q has no status", automations.ErrInvalidAutomation, row.ID,
			)
		}
		summary.Status = automations.RunStatus(row.RunStatus.String)
		fact, factErr := factSummaryFromRow(row)
		if factErr != nil {
			return summary, factErr
		}
		summary.Fact = fact
	case automations.AutomationHistorySkip:
		if !row.SkipReason.Valid {
			return summary, fmt.Errorf(
				"%w: stored Skip %q has no reason", automations.ErrInvalidAutomation, row.ID,
			)
		}
		summary.Reason = automations.AutomationSkipReason(row.SkipReason.String)
		fact, factErr := factSummaryFromRow(row)
		if factErr != nil {
			return summary, factErr
		}
		if fact == nil {
			return summary, fmt.Errorf(
				"%w: stored Skip %q has no Fact summary", automations.ErrInvalidAutomation, row.ID,
			)
		}
		summary.Fact = fact
	default:
		return summary, fmt.Errorf(
			"%w: stored history %q has unknown kind %q",
			automations.ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
	return summary, nil
}

func runFromRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.AutomationRun, error) {
	if !row.RunSnapshotJson.Valid || !row.RunSource.Valid || !row.RunStatus.Valid || !row.RunStartedAt.Valid ||
		!row.RunMatchedTriggerIdsJson.Valid {
		return automations.AutomationRun{}, fmt.Errorf(
			"%w: stored Run %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	runID, err := automations.ParseAutomationRunID(row.ID)
	if err != nil {
		return automations.AutomationRun{}, err
	}
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.AutomationRun{}, fmt.Errorf("stored Run %q: %w", row.ID, err)
	}
	snapshot, err := automations.DecodeAutomationDefinition(json.RawMessage(row.RunSnapshotJson.String))
	if err != nil {
		return automations.AutomationRun{}, fmt.Errorf("stored Run %q snapshot: %w", row.ID, err)
	}
	matched, err := decodeTriggerIDs(json.RawMessage(row.RunMatchedTriggerIdsJson.String))
	if err != nil {
		return automations.AutomationRun{}, fmt.Errorf("stored Run %q matched triggers: %w", row.ID, err)
	}
	startedAt, err := decodeAutomationTimestamp(row.RunStartedAt.String)
	if err != nil {
		return automations.AutomationRun{}, fmt.Errorf("stored Run %q started_at: %w", row.ID, err)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.RunCompletedAt)
	if err != nil {
		return automations.AutomationRun{}, fmt.Errorf("stored Run %q completed_at: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return automations.AutomationRun{}, err
	}
	steps, err := runSteps(ctx, queries, row.ID)
	if err != nil {
		return automations.AutomationRun{}, err
	}
	run := automations.AutomationRun{
		ID:                runID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Snapshot:          snapshot,
		Source:            automations.RunSource(row.RunSource.String),
		Fact:              fact,
		MatchedTriggerIDs: matched,
		Status:            automations.RunStatus(row.RunStatus.String),
		FailureCode:       stringPointer(row.RunFailureCode),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		Steps:             steps,
	}
	if err = automations.ValidateAutomationRun(run); err != nil {
		return automations.AutomationRun{}, err
	}
	return run, nil
}

func skipFromRow(row dbsqlc.AutomationHistory) (automations.AutomationSkip, error) {
	if !row.SkipMatchedTriggersJson.Valid || !row.SkipReason.Valid {
		return automations.AutomationSkip{}, fmt.Errorf(
			"%w: stored Skip %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	skipID, err := automations.ParseAutomationSkipID(row.ID)
	if err != nil {
		return automations.AutomationSkip{}, err
	}
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.AutomationSkip{}, fmt.Errorf("stored Skip %q: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return automations.AutomationSkip{}, err
	}
	if fact == nil {
		return automations.AutomationSkip{}, fmt.Errorf(
			"%w: stored Skip %q has no Fact summary", automations.ErrInvalidAutomation, row.ID,
		)
	}
	triggers, err := automations.DecodeMatchedTriggers(json.RawMessage(row.SkipMatchedTriggersJson.String))
	if err != nil {
		return automations.AutomationSkip{}, fmt.Errorf("stored Skip %q matched triggers: %w", row.ID, err)
	}
	skippedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.AutomationSkip{}, fmt.Errorf("stored Skip %q recorded_at: %w", row.ID, err)
	}
	skip := automations.AutomationSkip{
		ID:              skipID,
		AutomationID:    automationID,
		AutomationName:  row.AutomationName,
		Revision:        row.Revision,
		Fact:            *fact,
		MatchedTriggers: triggers,
		Reason:          automations.AutomationSkipReason(row.SkipReason.String),
		SkippedAt:       skippedAt,
	}
	if err = automations.ValidateAutomationSkip(skip); err != nil {
		return automations.AutomationSkip{}, err
	}
	return skip, nil
}

func runSteps(
	ctx context.Context,
	queries *dbsqlc.Queries,
	runID string,
) ([]automations.AutomationStepAttempt, error) {
	rows, err := queries.ListRunSteps(ctx, dbsqlc.ListRunStepsParams{RunID: runID})
	if err != nil {
		return nil, err
	}
	steps := make([]automations.AutomationStepAttempt, 0, len(rows))
	for _, row := range rows {
		step, stepErr := stepAttemptFromRow(row)
		if stepErr != nil {
			return nil, stepErr
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func stepAttemptFromRow(row dbsqlc.AutomationRunStep) (automations.AutomationStepAttempt, error) {
	stepID, err := automations.ParseStepID(row.StepID)
	if err != nil {
		return automations.AutomationStepAttempt{}, fmt.Errorf("stored step %s/%d: %w", row.RunID, row.Position, err)
	}
	startedAt, err := parseNullableAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.AutomationStepAttempt{}, fmt.Errorf(
			"stored step %s/%d started_at: %w", row.RunID, row.Position, err,
		)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.AutomationStepAttempt{}, fmt.Errorf(
			"stored step %s/%d completed_at: %w", row.RunID, row.Position, err,
		)
	}
	step := automations.AutomationStepAttempt{
		Position:              int(row.Position),
		StepID:                stepID,
		Status:                automations.StepStatus(row.Status),
		ReservedCommandID:     commandIDPointer(row.ReservedCommandID),
		ReservedCorrelationID: correlationIDPointer(row.ReservedCorrelationID),
		VerifiedCommandID:     commandIDPointer(row.VerifiedCommandID),
		FailureCode:           stringPointer(row.FailureCode),
		StartedAt:             startedAt,
		CompletedAt:           completedAt,
	}
	return step, nil
}
