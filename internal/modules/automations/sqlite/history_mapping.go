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
) (automations.HistoryEntry, error) {
	switch automations.HistoryKind(row.Kind) {
	case automations.HistoryRun:
		run, err := runFromRow(ctx, queries, row)
		if err != nil {
			return automations.HistoryEntry{}, err
		}
		return automations.HistoryEntry{Kind: automations.HistoryRun, Run: &run}, nil
	case automations.HistorySkip:
		skip, err := skipFromRow(row)
		if err != nil {
			return automations.HistoryEntry{}, err
		}
		return automations.HistoryEntry{Kind: automations.HistorySkip, Skip: &skip}, nil
	default:
		return automations.HistoryEntry{}, fmt.Errorf(
			"%w: stored history %q has unknown kind %q",
			automations.ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
}

func historySummary(row dbsqlc.AutomationHistory) (automations.HistorySummary, error) {
	summary, err := newHistorySummaryBase(row)
	if err != nil {
		return automations.HistorySummary{}, err
	}
	switch summary.Kind {
	case automations.HistoryRun:
		err = applyRunSummary(row, &summary)
	case automations.HistorySkip:
		err = applySkipSummary(row, &summary)
	default:
		return summary, fmt.Errorf(
			"%w: stored history %q has unknown kind %q",
			automations.ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
	if err != nil {
		return summary, err
	}
	return summary, nil
}

// newHistorySummaryBase decodes the identity, revision, timestamp, and Condition
// decision summary every listing shares.
func newHistorySummaryBase(
	row dbsqlc.AutomationHistory,
) (automations.HistorySummary, error) {
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.HistorySummary{}, fmt.Errorf("stored history %q: %w", row.ID, err)
	}
	if row.Revision < 1 {
		return automations.HistorySummary{}, fmt.Errorf(
			"%w: stored history %q revision %d", automations.ErrInvalidAutomation, row.ID, row.Revision,
		)
	}
	recordedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.HistorySummary{}, fmt.Errorf("stored history %q recorded_at: %w", row.ID, err)
	}
	summary := automations.HistorySummary{
		ID:             row.ID,
		Kind:           automations.HistoryKind(row.Kind),
		AutomationID:   automationID,
		AutomationName: row.AutomationName,
		Revision:       row.Revision,
		RecordedAt:     recordedAt,
	}
	// The decision summary columns are derived from the decision document at
	// write time, so listing never parses the full snapshot-and-evidence JSON.
	summary.ConditionMode = automations.ConditionDecisionMode(row.ConditionMode)
	summary.BypassRequested = row.ConditionBypassed == 1
	if row.ConditionResult.Valid {
		result := automations.ConditionResult(row.ConditionResult.String)
		summary.ConditionResult = &result
	}
	return summary, nil
}

// applyRunSummary decodes the Run-only source and Fact columns.
func applyRunSummary(
	row dbsqlc.AutomationHistory,
	summary *automations.HistorySummary,
) error {
	if !row.RunStatus.Valid || !row.RunSource.Valid {
		return fmt.Errorf(
			"%w: stored Run %q has no status or source", automations.ErrInvalidAutomation, row.ID,
		)
	}
	summary.Status = automations.RunStatus(row.RunStatus.String)
	summary.Source = automations.RunSource(row.RunSource.String)
	heldState, err := heldStateEvidenceFromRow(row)
	if err != nil {
		return err
	}
	summary.HeldState = heldState
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return err
	}
	summary.Fact = fact
	return nil
}

// applySkipSummary decodes the Skip-only reason, provenance, and Fact columns.
func applySkipSummary(
	row dbsqlc.AutomationHistory,
	summary *automations.HistorySummary,
) error {
	if !row.SkipReason.Valid || !row.SkipSource.Valid {
		return fmt.Errorf(
			"%w: stored Skip %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	summary.Reason = automations.SkipReason(row.SkipReason.String)
	summary.Source = automations.RunSource(row.SkipSource.String)
	heldState, err := heldStateEvidenceFromRow(row)
	if err != nil {
		return err
	}
	summary.HeldState = heldState
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return err
	}
	summary.Fact = fact
	return nil
}

func runFromRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.Run, error) {
	if !row.RunSnapshotJson.Valid || !row.RunSource.Valid || !row.RunStatus.Valid || !row.RunStartedAt.Valid ||
		!row.RunMatchedTriggerIdsJson.Valid {
		return automations.Run{}, fmt.Errorf(
			"%w: stored Run %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	runID, err := automations.ParseRunID(row.ID)
	if err != nil {
		return automations.Run{}, err
	}
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q: %w", row.ID, err)
	}
	snapshot, err := automations.DecodeDefinition(json.RawMessage(row.RunSnapshotJson.String))
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q snapshot: %w", row.ID, err)
	}
	matched, err := decodeTriggerIDs(json.RawMessage(row.RunMatchedTriggerIdsJson.String))
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q matched triggers: %w", row.ID, err)
	}
	startedAt, err := decodeAutomationTimestamp(row.RunStartedAt.String)
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q started_at: %w", row.ID, err)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.RunCompletedAt)
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q completed_at: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return automations.Run{}, err
	}
	heldState, err := heldStateEvidenceFromRow(row)
	if err != nil {
		return automations.Run{}, err
	}
	decision, err := decodeConditionDecisionColumn(row)
	if err != nil {
		return automations.Run{}, err
	}
	steps, err := runSteps(ctx, queries, row.ID)
	if err != nil {
		return automations.Run{}, err
	}
	run := automations.Run{
		ID:                runID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Snapshot:          snapshot,
		Source:            automations.RunSource(row.RunSource.String),
		Fact:              fact,
		HeldState:         heldState,
		MatchedTriggerIDs: matched,
		ConditionDecision: decision,
		Status:            automations.RunStatus(row.RunStatus.String),
		FailureCode:       stringPointer(row.RunFailureCode),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		Steps:             steps,
	}
	return run, nil
}

// skipFromRow decodes one retained Skip; a row without an explicit skip_source is
// corruption rather than a zero value.
func skipFromRow(row dbsqlc.AutomationHistory) (automations.Skip, error) {
	if !row.SkipMatchedTriggersJson.Valid || !row.SkipReason.Valid || !row.SkipSource.Valid {
		return automations.Skip{}, fmt.Errorf(
			"%w: stored Skip %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	skipID, err := automations.ParseSkipID(row.ID)
	if err != nil {
		return automations.Skip{}, err
	}
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.Skip{}, fmt.Errorf("stored Skip %q: %w", row.ID, err)
	}
	fact, err := factSummaryFromRow(row)
	if err != nil {
		return automations.Skip{}, err
	}
	heldState, err := heldStateEvidenceFromRow(row)
	if err != nil {
		return automations.Skip{}, err
	}
	triggers, err := automations.DecodeMatchedTriggers(json.RawMessage(row.SkipMatchedTriggersJson.String))
	if err != nil {
		return automations.Skip{}, fmt.Errorf("stored Skip %q matched triggers: %w", row.ID, err)
	}
	skippedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.Skip{}, fmt.Errorf("stored Skip %q recorded_at: %w", row.ID, err)
	}
	source := automations.RunSource(row.SkipSource.String)
	decision, err := decodeConditionDecisionColumn(row)
	if err != nil {
		return automations.Skip{}, err
	}
	skip := automations.Skip{
		ID:                skipID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Source:            source,
		Fact:              fact,
		HeldState:         heldState,
		MatchedTriggers:   triggers,
		Reason:            automations.SkipReason(row.SkipReason.String),
		ConditionDecision: decision,
		SkippedAt:         skippedAt,
	}
	return skip, nil
}

// heldStateEvidenceFromRow decodes the optional held-state provenance columns;
// partially stored evidence is corruption rather than an absent hold.
func heldStateEvidenceFromRow(row dbsqlc.AutomationHistory) (*automations.HeldStateEvidence, error) {
	if !row.HoldTriggerID.Valid && !row.HoldStartedAt.Valid && !row.HoldDueAt.Valid {
		return nil, nil //nolint:nilnil // Absence of optional hold evidence is not an error.
	}
	if !row.HoldTriggerID.Valid || !row.HoldStartedAt.Valid || !row.HoldDueAt.Valid {
		return nil, fmt.Errorf("%w: stored history %q has incomplete held-state evidence",
			automations.ErrInvalidAutomation, row.ID)
	}
	triggerID, err := automations.ParseTriggerID(row.HoldTriggerID.String)
	if err != nil {
		return nil, fmt.Errorf("stored history %q hold trigger: %w", row.ID, err)
	}
	startedAt, err := decodeAutomationTimestamp(row.HoldStartedAt.String)
	if err != nil {
		return nil, fmt.Errorf("stored history %q hold started_at: %w", row.ID, err)
	}
	dueAt, err := decodeAutomationTimestamp(row.HoldDueAt.String)
	if err != nil {
		return nil, fmt.Errorf("stored history %q hold due_at: %w", row.ID, err)
	}
	evidence := &automations.HeldStateEvidence{TriggerID: triggerID, StartedAt: startedAt, DueAt: dueAt}
	if !dueAt.After(startedAt) {
		return nil, fmt.Errorf("%w: stored history %q held-state due time is not after its start",
			automations.ErrInvalidAutomation, row.ID)
	}
	return evidence, nil
}

// decodeConditionDecisionColumn decodes one persisted Condition decision; a
// missing payload is corruption, never a zero-valued mode.
func decodeConditionDecisionColumn(
	row dbsqlc.AutomationHistory,
) (automations.ConditionDecision, error) {
	decision, err := automations.DecodeConditionDecision(
		json.RawMessage(row.ConditionDecisionJson),
	)
	if err != nil {
		return nil, fmt.Errorf(
			"stored history %q condition decision: %w", row.ID, err,
		)
	}
	return decision, nil
}

func runSteps(
	ctx context.Context,
	queries *dbsqlc.Queries,
	runID string,
) ([]automations.StepAttempt, error) {
	rows, err := queries.ListRunSteps(ctx, dbsqlc.ListRunStepsParams{RunID: runID})
	if err != nil {
		return nil, err
	}
	steps := make([]automations.StepAttempt, 0, len(rows))
	for _, row := range rows {
		step, stepErr := stepAttemptFromRow(row)
		if stepErr != nil {
			return nil, stepErr
		}
		steps = append(steps, step)
	}
	return steps, nil
}

func stepAttemptFromRow(row dbsqlc.AutomationRunStep) (automations.StepAttempt, error) {
	stepID, err := automations.ParseStepID(row.StepID)
	if err != nil {
		return automations.StepAttempt{}, fmt.Errorf("stored step %s/%d: %w", row.RunID, row.Position, err)
	}
	startedAt, err := parseNullableAutomationTimestamp(row.StartedAt)
	if err != nil {
		return automations.StepAttempt{}, fmt.Errorf(
			"stored step %s/%d started_at: %w", row.RunID, row.Position, err,
		)
	}
	completedAt, err := parseNullableAutomationTimestamp(row.CompletedAt)
	if err != nil {
		return automations.StepAttempt{}, fmt.Errorf(
			"stored step %s/%d completed_at: %w", row.RunID, row.Position, err,
		)
	}
	step := automations.StepAttempt{
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
