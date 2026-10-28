package sqlite

import (
	"context"
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// RecordRunValueInitialization seals initialization and any failure with the parent.
func (repo *AutomationRepository) RecordRunValueInitialization(
	ctx context.Context,
	id automations.RunID,
	init automations.RunValueInitialization,
) error {
	if _, err := automations.ParseRunID(string(id)); err != nil {
		return err
	}
	return repo.transaction(ctx, func(queries *dbsqlc.Queries) error {
		parent, err := runningExecutionParent(ctx, queries, id)
		if err != nil {
			return err
		}
		raw, err := automations.EncodeRunValueInitializationWithPreparedSnapshot(init, parent.snapshot)
		if err != nil {
			return err
		}
		changed, err := queries.InitializeRunValues(ctx, dbsqlc.InitializeRunValuesParams{
			RunID: string(id), Initialization: sql.NullString{String: string(raw), Valid: true},
		})
		if err != nil {
			return err
		}
		if changed != 1 {
			return lifecycleCorruption("Run initialization is not insertable")
		}
		if failure, ok := init.Body.(automations.InitializationFailed); ok {
			return completeExecutionRun(ctx, queries, id,
				automations.FailedRun{FailureCode: failure.Failure.FailureCode}, init.RecordedAt)
		}
		return nil
	})
}
