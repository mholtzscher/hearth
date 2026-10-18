package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ListEntityAvailabilityHistory(
	ctx context.Context,
	params devices.ListEntityAvailabilityParams,
) (devices.Page[devices.HealthTransition], error) {
	if _, err := devices.ParseEntityID(string(params.EntityID)); err != nil || !devices.ValidPageLimit(params.Limit) ||
		(params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return devices.Page[devices.HealthTransition]{}, devices.ErrInvalidPage
	}
	var exists int
	if err := repository.database.QueryRowContext(ctx, "SELECT 1 FROM entities WHERE id = ?", params.EntityID).
		Scan(&exists); errors.Is(
		err,
		sql.ErrNoRows,
	) {
		return devices.Page[devices.HealthTransition]{}, devices.ErrEntityNotFound
	} else if err != nil {
		return devices.Page[devices.HealthTransition]{}, fmt.Errorf("get Entity for availability history: %w", err)
	}
	values, err := listEntityTransitions(ctx, repository.queries, params, int64(params.Limit+1))
	if err != nil {
		return devices.Page[devices.HealthTransition]{}, err
	}
	transitions, err := healthTransitionsFromValues(values)
	if err != nil {
		return devices.Page[devices.HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listEntityTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.ListEntityAvailabilityParams,
	limit int64,
) ([]sqliteTransition, error) {
	var values []sqliteTransition
	if params.BeforeReceiveOrder == nil {
		rows, err := queries.ListEntityAvailabilityHistoryFirstPage(
			ctx,
			dbsqlc.ListEntityAvailabilityHistoryFirstPageParams{
				EntityID: nullableText(string(params.EntityID)), PageLimit: limit,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("list Entity availability history: %w", err)
		}
		for _, row := range rows {
			values = append(
				values,
				newSQLiteTransition(
					row.ReceiveOrder,
					row.Status,
					row.Source,
					row.ReasonCode,
					row.SourceObservedAt,
					row.ObservedAt,
				),
			)
		}
	} else {
		rows, err := queries.ListEntityAvailabilityHistoryBefore(ctx, dbsqlc.ListEntityAvailabilityHistoryBeforeParams{
			EntityID: nullableText(
				string(params.EntityID),
			),
			BeforeReceiveOrder: *params.BeforeReceiveOrder,
			PageLimit:          limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list Entity availability history before cursor: %w", err)
		}
		for _, row := range rows {
			values = append(
				values,
				newSQLiteTransition(
					row.ReceiveOrder,
					row.Status,
					row.Source,
					row.ReasonCode,
					row.SourceObservedAt,
					row.ObservedAt,
				),
			)
		}
	}
	return values, nil
}
