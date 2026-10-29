package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// CompleteRun terminalizes a Run without permitting success from an incomplete selected path.
func (repo *AutomationRepository) CompleteRun(ctx context.Context, completion automations.RunCompletion) error {
	if _, err := automations.ParseRunID(string(completion.RunID)); err != nil {
		return err
	}
	if err := automations.ValidateRunCompletion(completion); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, err := runningExecutionParent(ctx, queries, completion.RunID)
		if err != nil {
			return err
		}
		if !parent.recorded {
			return lifecycleCorruption("legacy Run cannot resume execution")
		}
		trace, err := runExecutions(ctx, queries, string(completion.RunID))
		if err != nil {
			return err
		}
		completedAt := repo.now().UTC()
		if err = automations.ValidateRunValueTraceWithPreparedSnapshot(
			parent.snapshot,
			parent.matches,
			automations.CompletedRun{CompletedAt: completedAt, Outcome: completion.Outcome},
			parent.initialization,
			trace,
			parent.recorded,
		); err != nil {
			return err
		}
		return completeExecutionRun(ctx, queries, completion.RunID, completion.Outcome, completedAt)
	})
}

// InterruptRun makes one Run-scoped cleanup attempt and preserves terminal evidence.
func (repo *AutomationRepository) InterruptRun(ctx context.Context, id automations.RunID, at time.Time) error {
	if _, err := automations.ParseRunID(string(id)); err != nil {
		return err
	}
	if at.IsZero() {
		return errors.New("automation interruption time is required")
	}
	return repo.interruptEvidence(ctx, string(id), at, automations.FailureExecutorFault)
}

// InterruptActiveRuns atomically interrupts unresolved execution after startup, never resuming it.
func (repo *AutomationRepository) InterruptActiveRuns(ctx context.Context, at time.Time, reason string) error {
	if at.IsZero() {
		return errors.New("automation interruption time is required")
	}
	switch reason {
	case automations.FailureCoreStopping, automations.FailureCoreRestarted, automations.FailureExecutorFault:
	default:
		return lifecycleCorruption("unsupported interruption reason")
	}
	return repo.interruptEvidence(ctx, "", at, reason)
}

func (repo *AutomationRepository) interruptEvidence(
	ctx context.Context,
	scope string,
	at time.Time,
	reason string,
) error {
	completed := sql.NullString{String: encodeAutomationTimestamp(at), Valid: true}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		if err := queries.InterruptStepExecutions(ctx, dbsqlc.InterruptStepExecutionsParams{
			Scope: scope, CompletedAt: completed, Reason: reason,
		}); err != nil {
			return err
		}
		return queries.InterruptRuns(ctx, dbsqlc.InterruptRunsParams{
			Scope: scope, CompletedAt: completed, Reason: sql.NullString{String: reason, Valid: true},
		})
	})
}
