package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// RecordBranchDecision appends evidence before execution. Failure evidence and
// the Run outcome share this transaction; commit errors are never retried.
func (repo *AutomationRepository) RecordBranchDecision(
	ctx context.Context,
	runID automations.RunID,
	decision automations.BranchDecision,
) error {
	if _, err := automations.ParseRunID(string(runID)); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		row, err := queries.GetBranchDecisionParent(ctx, dbsqlc.GetBranchDecisionParentParams{ID: string(runID)})
		if err != nil {
			return err
		}
		if row.Kind != string(automations.HistoryRun) || row.RunStatus.String != string(automations.RunRunning) {
			return fmt.Errorf("%w: branch parent %s is not a running Run", automations.ErrInvalidAutomation, runID)
		}
		run, err := runFromRow(ctx, queries, row)
		if err != nil {
			return err
		}
		if err = automations.ValidateBranchDecision(decision, run.Snapshot, run.MatchedTriggerIDs); err != nil {
			return err
		}
		previousAt := run.StartedAt
		if len(run.BranchDecisions) > 0 {
			previousAt = run.BranchDecisions[len(run.BranchDecisions)-1].EvaluatedAt
		}
		if decision.Position != len(run.BranchDecisions) || decision.EvaluatedAt.Before(previousAt) {
			return fmt.Errorf(
				"%w: branch decision position or time is not appendable",
				automations.ErrInvalidAutomation,
			)
		}
		return repo.persistBranchDecision(ctx, queries, runID, decision)
	})
}

// persistBranchDecision consumes evidence validated against the transaction's
// running parent and immutable snapshot.
func (repo *AutomationRepository) persistBranchDecision(
	ctx context.Context,
	queries *dbsqlc.Queries,
	runID automations.RunID,
	decision automations.BranchDecision,
) error {
	raw, err := automations.EncodeBranchDecision(decision)
	if err != nil {
		return err
	}
	if err = queries.CreateRunBranchDecision(ctx, dbsqlc.CreateRunBranchDecisionParams{
		RunID:        string(runID),
		StepID:       string(decision.StepID),
		Position:     int64(decision.Position),
		DecisionJson: string(raw),
	}); err != nil {
		return err
	}
	if decision.Outcome != automations.BranchUnknown && decision.Outcome != automations.BranchError {
		return nil
	}
	updated, err := queries.CompleteRun(ctx, dbsqlc.CompleteRunParams{
		ID: string(runID), RunStatus: sql.NullString{String: string(automations.RunFailed), Valid: true},
		RunFailureCode: encodeNullableString(decision.FailureCode),
		RunCompletedAt: sql.NullString{String: encodeAutomationTimestamp(repo.now()), Valid: true},
	})
	if err != nil {
		return err
	}
	if updated != 1 {
		return fmt.Errorf("%w: branch parent %s is not completable", automations.ErrInvalidAutomation, runID)
	}
	return nil
}

// runBranchDecisions verifies retained row identity and evidence using only the
// original snapshot. Definitions and current Devices references are irrelevant.
func runBranchDecisions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	run automations.Run,
) ([]automations.BranchDecision, error) {
	rows, err := queries.ListRunBranchDecisions(ctx, dbsqlc.ListRunBranchDecisionsParams{RunID: string(run.ID)})
	if err != nil {
		return nil, err
	}
	decisions := make([]automations.BranchDecision, 0, len(rows))
	previousAt := run.StartedAt
	for position, row := range rows {
		decision, decodeErr := automations.DecodeBranchDecision(json.RawMessage(row.DecisionJson))
		if decodeErr != nil {
			return nil, fmt.Errorf("stored branch %s/%d: %w", run.ID, row.Position, decodeErr)
		}
		if row.RunID != string(run.ID) || row.StepID != string(decision.StepID) ||
			row.Position != int64(decision.Position) || decision.Position != position ||
			decision.EvaluatedAt.Before(previousAt) {
			return nil, fmt.Errorf(
				"%w: stored branch identity, position, or chronology disagrees",
				automations.ErrInvalidAutomation,
			)
		}
		if err = automations.ValidateBranchDecision(decision, run.Snapshot, run.MatchedTriggerIDs); err != nil {
			return nil, fmt.Errorf("stored branch %s/%d: %w", run.ID, row.Position, err)
		}
		previousAt = decision.EvaluatedAt
		decisions = append(decisions, decision)
	}
	return decisions, nil
}
