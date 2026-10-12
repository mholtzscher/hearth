package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/automations"
	"github.com/mholtzscher/hearth/internal/modules/automations/sqlite/dbsqlc"
)

// runningEvidenceParent loads and validates the transaction-local immutable parent
// shared by reached delay and notification evidence operations.
func runningEvidenceParent(
	ctx context.Context,
	queries *dbsqlc.Queries,
	id automations.RunID,
) (automations.Run, error) {
	row, err := queries.GetRunEvidenceParent(ctx, dbsqlc.GetRunEvidenceParentParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return automations.Run{}, fmt.Errorf("%w: missing evidence parent", automations.ErrInvalidAutomation)
	}
	if err != nil {
		return automations.Run{}, err
	}
	if row.Kind != string(automations.HistoryRun) || row.RunStatus.String != string(automations.RunRunning) {
		return automations.Run{}, fmt.Errorf("%w: evidence parent is not running", automations.ErrInvalidAutomation)
	}
	return runFromRow(ctx, queries, row)
}
