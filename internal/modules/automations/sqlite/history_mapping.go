package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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
		return nil, fmt.Errorf(
			"%w: stored history %q has unknown kind %q",
			automations.ErrInvalidAutomation, row.ID, row.Kind,
		)
	}
}

func historySummary(row historySummaryRow) (automations.HistorySummary, error) {
	summary, err := newHistorySummaryBase(row)
	if err != nil {
		return automations.HistorySummary{}, err
	}
	switch automations.HistoryKind(row.Kind) {
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
	row historySummaryRow,
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
		AutomationID:   automationID,
		AutomationName: row.AutomationName,
		Revision:       row.Revision,
		RecordedAt:     recordedAt,
	}
	conditionSummary, err := conditionSummaryFromColumns(row)
	if err != nil {
		return automations.HistorySummary{}, err
	}
	summary.ConditionSummary = conditionSummary
	return summary, nil
}

// applyRunSummary decodes the Run-only source and Fact columns.
func applyRunSummary(
	row historySummaryRow,
	summary *automations.HistorySummary,
) error {
	if !row.RunStatus.Valid || !row.RunSource.Valid {
		return fmt.Errorf(
			"%w: stored Run %q has no status or source", automations.ErrInvalidAutomation, row.ID,
		)
	}
	status := automations.RunStatus(row.RunStatus.String)
	switch status {
	case automations.RunRunning, automations.RunSucceeded, automations.RunFailed, automations.RunInterrupted:
	default:
		return lifecycleCorruption("unknown summary Run status")
	}
	summary.Body = automations.RunHistorySummary{Status: status}
	cause, err := causeFromRow(row, automations.RunSource(row.RunSource.String))
	if err != nil {
		return err
	}
	summary.Cause = cause
	return validateScheduleSummary(row, *summary)
}

// applySkipSummary decodes the Skip-only reason, provenance, and Fact columns.
func applySkipSummary(
	row historySummaryRow,
	summary *automations.HistorySummary,
) error {
	if !row.SkipReason.Valid || !row.SkipSource.Valid {
		return fmt.Errorf(
			"%w: stored Skip %q is incomplete", automations.ErrInvalidAutomation, row.ID,
		)
	}
	reason := automations.SkipReason(row.SkipReason.String)
	switch reason {
	case automations.SkipBusy,
		automations.SkipStaleFact,
		automations.SkipConditionsFalse,
		automations.SkipConditionsUnknown:
	default:
		return lifecycleCorruption("unknown summary Skip reason")
	}
	summary.Body = automations.SkipHistorySummary{Reason: reason}
	cause, err := causeFromRow(row, automations.RunSource(row.SkipSource.String))
	if err != nil {
		return err
	}
	summary.Cause = cause
	return validateScheduleSummary(row, *summary)
}

//nolint:funlen // Reconstruction validates the complete retained record.
func runFromRow(
	ctx context.Context,
	queries *dbsqlc.Queries,
	row dbsqlc.AutomationHistory,
) (automations.Run, error) {
	if !row.DefinitionRevision.Valid || row.DefinitionRevision.Int64 != row.Revision ||
		!row.RunSource.Valid || !row.RunStatus.Valid || !row.RunStartedAt.Valid ||
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
	rawSnapshot, err := queries.GetDefinitionRevision(ctx, dbsqlc.GetDefinitionRevisionParams{
		AutomationID: row.AutomationID, Revision: row.Revision,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Run{}, fmt.Errorf("%w: stored Run %q is missing definition revision %d",
			automations.ErrInvalidAutomation, row.ID, row.Revision)
	}
	if err != nil {
		return automations.Run{}, fmt.Errorf("stored Run %q definition revision: %w", row.ID, err)
	}
	snapshot, err := automations.DecodeDefinition(json.RawMessage(rawSnapshot))
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
	cause, err := causeFromRow(historySummaryColumns(row), automations.RunSource(row.RunSource.String))
	if err != nil {
		return automations.Run{}, err
	}
	state, err := runStateFromColumns(
		automations.RunStatus(row.RunStatus.String),
		completedAt,
		stringPointer(row.RunFailureCode),
	)
	if err != nil {
		return automations.Run{}, err
	}
	decision, err := automations.DecodeConditionDecisionEvidence(
		json.RawMessage(row.ConditionDecisionJson),
		snapshot.Conditions,
	)
	if err != nil {
		return automations.Run{}, err
	}
	executions, err := runExecutions(ctx, queries, row.ID)
	if err != nil {
		return automations.Run{}, err
	}
	initialization, err := runInitializationFromRow(row)
	if err != nil {
		return automations.Run{}, err
	}
	run := automations.Run{
		ID:                  runID,
		AutomationID:        automationID,
		AutomationName:      row.AutomationName,
		Revision:            row.Revision,
		Snapshot:            snapshot,
		Cause:               cause,
		MatchedTriggerIDs:   matched,
		ConditionDecision:   decision,
		State:               state,
		StartedAt:           startedAt,
		Executions:          executions,
		DataEvidence:        row.DataEvidence,
		ValueInitialization: initialization,
	}
	if err = validateScheduleRun(run); err != nil {
		return automations.Run{}, err
	}
	matchedTriggers, err := automations.MatchedTriggerSnapshots(run.Snapshot, run.MatchedTriggerIDs)
	if err != nil {
		return automations.Run{}, err
	}
	if err = automations.ValidateAdmissionCause(run.Cause, matchedTriggers); err != nil {
		return automations.Run{}, err
	}
	if err = automations.ValidateRunValueTraceWithPreparedSnapshot(
		run.Snapshot,
		run.MatchedTriggerIDs,
		automations.RunStateStatus(run.State),
		run.ValueInitialization, run.Executions, run.DataEvidence == automations.RunDataRecorded,
	); err != nil {
		return automations.Run{}, err
	}
	return run, nil
}

func runInitializationFromRow(row dbsqlc.AutomationHistory) (*automations.RunValueInitialization, error) {
	if row.DataEvidence != automations.RunDataRecorded && row.DataEvidence != automations.RunDataLegacyUnavailable {
		return nil, lifecycleCorruption("unknown value provenance")
	}
	if row.DataEvidence == automations.RunDataLegacyUnavailable &&
		(row.ValueEvidenceBytes != 0 || row.ValueEvidenceRecords != 0) {
		return nil, lifecycleCorruption("legacy Run has value counters")
	}
	if !row.ValueInitializationJson.Valid {
		return nil, nil //nolint:nilnil // No initialization yet.
	}
	initialization, err := automations.DecodeRunValueInitialization(json.RawMessage(row.ValueInitializationJson.String))
	if err != nil {
		return nil, err
	}
	if failed, ok := initialization.Body.(automations.InitializationFailed); ok &&
		(!row.RunFailureCode.Valid || row.RunFailureCode.String != failed.Failure.FailureCode) {
		return nil, lifecycleCorruption("initialization failure disagrees with parent")
	}
	return &initialization, nil
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
	cause, err := causeFromRow(historySummaryColumns(row), automations.RunSource(row.SkipSource.String))
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
	decision, err := decodeConditionDecisionColumn(row)
	if err != nil {
		return automations.Skip{}, err
	}
	skip := automations.Skip{
		ID:                skipID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Cause:             cause,
		MatchedTriggers:   triggers,
		Reason:            automations.SkipReason(row.SkipReason.String),
		ConditionDecision: decision,
		SkippedAt:         skippedAt,
	}
	if automations.CauseSource(skip.Cause) == automations.RunSourceSchedule && row.FactPreviousValueJson.Valid {
		return automations.Skip{}, fmt.Errorf(
			"%w: schedule Skip carries previous Fact evidence",
			automations.ErrInvalidAutomation,
		)
	}
	if err = validateScheduleSkip(skip); err != nil {
		return automations.Skip{}, err
	}
	if err = automations.ValidateAdmissionCause(skip.Cause, skip.MatchedTriggers); err != nil {
		return automations.Skip{}, err
	}
	return skip, nil
}

// heldStateEvidenceFromRow decodes the optional held-state provenance columns;
// partially stored evidence is corruption rather than an absent hold.
func heldStateEvidenceFromRow(row historySummaryRow) (*automations.HeldStateEvidence, error) {
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
