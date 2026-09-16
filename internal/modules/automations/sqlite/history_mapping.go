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
	summary, decision, err := newHistorySummaryBase(row)
	if err != nil {
		return automations.AutomationHistorySummary{}, err
	}
	switch summary.Kind {
	case automations.AutomationHistoryRun:
		err = applyRunSummary(row, &summary)
	case automations.AutomationHistorySkip:
		err = applySkipSummary(row, &summary, decision)
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
// decision every summary shares, independent of the Run or Skip kind.
func newHistorySummaryBase(
	row dbsqlc.AutomationHistory,
) (automations.AutomationHistorySummary, automations.AutomationConditionDecision, error) {
	automationID, err := automations.ParseAutomationID(row.AutomationID)
	if err != nil {
		return automations.AutomationHistorySummary{}, automations.AutomationConditionDecision{},
			fmt.Errorf("stored history %q: %w", row.ID, err)
	}
	if row.Revision < 1 {
		return automations.AutomationHistorySummary{}, automations.AutomationConditionDecision{}, fmt.Errorf(
			"%w: stored history %q revision %d", automations.ErrInvalidAutomation, row.ID, row.Revision,
		)
	}
	recordedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.AutomationHistorySummary{}, automations.AutomationConditionDecision{},
			fmt.Errorf("stored history %q recorded_at: %w", row.ID, err)
	}
	summary := automations.AutomationHistorySummary{
		ID:             row.ID,
		Kind:           automations.AutomationHistoryKind(row.Kind),
		AutomationID:   automationID,
		AutomationName: row.AutomationName,
		Revision:       row.Revision,
		RecordedAt:     recordedAt,
	}
	decision, err := decodeConditionDecisionColumn(row)
	if err != nil {
		return summary, automations.AutomationConditionDecision{}, err
	}
	summary.ConditionMode = decision.Mode
	summary.BypassRequested = decision.BypassRequested
	if decision.Evaluation != nil {
		result := decision.Evaluation.Result
		summary.ConditionResult = &result
	}
	return summary, decision, nil
}

// applyRunSummary decodes the Run-only source and Fact columns.
func applyRunSummary(
	row dbsqlc.AutomationHistory,
	summary *automations.AutomationHistorySummary,
) error {
	if !row.RunStatus.Valid || !row.RunSource.Valid {
		return fmt.Errorf(
			"%w: stored Run %q has no status or source", automations.ErrInvalidAutomation, row.ID,
		)
	}
	summary.Status = automations.RunStatus(row.RunStatus.String)
	summary.Source = automations.RunSource(row.RunSource.String)
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
	summary *automations.AutomationHistorySummary,
	decision automations.AutomationConditionDecision,
) error {
	if !row.SkipReason.Valid {
		return fmt.Errorf(
			"%w: stored Skip %q has no reason", automations.ErrInvalidAutomation, row.ID,
		)
	}
	summary.Reason = automations.AutomationSkipReason(row.SkipReason.String)
	summary.Source = skipSource(row, decision)
	if summary.Source == "" {
		return fmt.Errorf(
			"%w: stored Skip %q has no admission provenance", automations.ErrInvalidAutomation, row.ID,
		)
	}
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
	decision, err := decodeConditionDecisionColumn(row)
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
		ConditionDecision: decision,
		Status:            automations.RunStatus(row.RunStatus.String),
		FailureCode:       stringPointer(row.RunFailureCode),
		StartedAt:         startedAt,
		CompletedAt:       completedAt,
		Steps:             steps,
	}
	return run, nil
}

// skipFromRow decodes one retained Skip. A legacy row may carry SQL NULL
// skip_source and condition_decision_json; that is normalized to a device-fact
// Skip with the explicit not_configured decision only when complete Fact
// evidence and an unconditional automatic reason make it consistent. Anything
// else is corruption rather than a silently accepted zero value.
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
	triggers, err := automations.DecodeMatchedTriggers(json.RawMessage(row.SkipMatchedTriggersJson.String))
	if err != nil {
		return automations.AutomationSkip{}, fmt.Errorf("stored Skip %q matched triggers: %w", row.ID, err)
	}
	skippedAt, err := decodeAutomationTimestamp(row.RecordedAt)
	if err != nil {
		return automations.AutomationSkip{}, fmt.Errorf("stored Skip %q recorded_at: %w", row.ID, err)
	}
	source := automations.RunSource(row.SkipSource.String)
	decision, err := decodeConditionDecisionColumn(row)
	if err != nil {
		return automations.AutomationSkip{}, err
	}
	if !row.SkipSource.Valid {
		source, decision, err = normalizeLegacySkip(row, fact, decision)
		if err != nil {
			return automations.AutomationSkip{}, err
		}
	}
	skip := automations.AutomationSkip{
		ID:                skipID,
		AutomationID:      automationID,
		AutomationName:    row.AutomationName,
		Revision:          row.Revision,
		Source:            source,
		Fact:              fact,
		MatchedTriggers:   triggers,
		Reason:            automations.AutomationSkipReason(row.SkipReason.String),
		ConditionDecision: decision,
		SkippedAt:         skippedAt,
	}
	return skip, nil
}

// normalizeLegacySkip maps one unconditioned legacy Skip row to the explicit
// not_configured device-fact form. It accepts only complete Fact evidence with
// matched Triggers and a stale_fact or automation_busy reason, so a Condition
// Skip that lost its provenance stays corruption.
func normalizeLegacySkip(
	row dbsqlc.AutomationHistory,
	fact *automations.DeviceFactSummary,
	decision automations.AutomationConditionDecision,
) (automations.RunSource, automations.AutomationConditionDecision, error) {
	if fact == nil || row.ConditionDecisionJson.Valid {
		return "", automations.AutomationConditionDecision{}, fmt.Errorf(
			"%w: stored Skip %q has no admission provenance", automations.ErrInvalidAutomation, row.ID,
		)
	}
	switch automations.AutomationSkipReason(row.SkipReason.String) {
	case automations.AutomationSkipStaleFact, automations.AutomationSkipBusy:
	case automations.AutomationSkipConditionsFalse, automations.AutomationSkipConditionsUnknown:
		return "", automations.AutomationConditionDecision{}, fmt.Errorf(
			"%w: stored Skip %q has no admission provenance", automations.ErrInvalidAutomation, row.ID,
		)
	default:
		return "", automations.AutomationConditionDecision{}, fmt.Errorf(
			"%w: stored Skip %q has no admission provenance", automations.ErrInvalidAutomation, row.ID,
		)
	}
	if decision.Mode != automations.AutomationConditionDecisionNotConfigured {
		return "", automations.AutomationConditionDecision{}, fmt.Errorf(
			"%w: stored Skip %q has no admission provenance", automations.ErrInvalidAutomation, row.ID,
		)
	}
	return automations.RunSourceDeviceFact, decision, nil
}

// decodeConditionDecisionColumn decodes one persisted Condition decision. SQL
// NULL is the legacy unconditioned row and normalizes to not_configured, never a
// zero-valued mode.
func decodeConditionDecisionColumn(
	row dbsqlc.AutomationHistory,
) (automations.AutomationConditionDecision, error) {
	if !row.ConditionDecisionJson.Valid {
		return automations.AutomationConditionDecision{
			Mode: automations.AutomationConditionDecisionNotConfigured,
		}, nil
	}
	decision, err := automations.DecodeAutomationConditionDecision(
		json.RawMessage(row.ConditionDecisionJson.String),
	)
	if err != nil {
		return automations.AutomationConditionDecision{}, fmt.Errorf(
			"stored history %q condition decision: %w", row.ID, err,
		)
	}
	return decision, nil
}

// skipSource reports the admission provenance of one row for a history summary.
// A legacy row normalizes to device_fact exactly when skipFromRow would accept it.
func skipSource(
	row dbsqlc.AutomationHistory,
	decision automations.AutomationConditionDecision,
) automations.RunSource {
	if row.SkipSource.Valid {
		return automations.RunSource(row.SkipSource.String)
	}
	switch automations.AutomationSkipReason(row.SkipReason.String) {
	case automations.AutomationSkipStaleFact, automations.AutomationSkipBusy:
		if decision.Mode == automations.AutomationConditionDecisionNotConfigured {
			return automations.RunSourceDeviceFact
		}
		return ""
	case automations.AutomationSkipConditionsFalse, automations.AutomationSkipConditionsUnknown:
		return ""
	default:
		return ""
	}
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
