package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) GetAdapter(ctx context.Context, adapterID string) (devices.AdapterInstance, error) {
	row, err := repository.queries.GetAdapterView(ctx, dbsqlc.GetAdapterViewParams{
		AdapterID: adapterID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.AdapterInstance{}, devices.ErrAdapterNotFound
	}
	if err != nil {
		return devices.AdapterInstance{}, fmt.Errorf("get Adapter: %w", err)
	}
	return adapterInstanceFromView(sqliteAdapterView{
		adapterID: row.AdapterID, healthStatus: row.HealthStatus,
		healthReasonCode: row.HealthReasonCode, healthSource: row.HealthSource,
		healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
		healthSourceObservedAt: row.HealthSourceObservedAt, runtimeID: row.RuntimeID,
		softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
		lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
	})
}

func (repository *DeviceRepository) ListAdapters(
	ctx context.Context,
	params devices.ListAdaptersParams,
) (devices.Page[devices.AdapterInstance], error) {
	if params.Limit < 1 {
		return devices.Page[devices.AdapterInstance]{}, devices.ErrInvalidPage
	}
	queries := repository.queries
	limit := int64(params.Limit + 1)
	var views []sqliteAdapterView
	if params.AfterID == nil {
		rows, err := queries.ListAdapterViewsFirstPage(ctx, dbsqlc.ListAdapterViewsFirstPageParams{
			PageLimit: limit,
		})
		if err != nil {
			return devices.Page[devices.AdapterInstance]{}, fmt.Errorf("list Adapters: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthSource: row.HealthSource,
				healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
				healthSourceObservedAt: row.HealthSourceObservedAt, runtimeID: row.RuntimeID,
				softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
				lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
			}
		}
	} else {
		rows, err := queries.ListAdapterViewsAfter(ctx, dbsqlc.ListAdapterViewsAfterParams{
			AfterAdapterID: *params.AfterID, PageLimit: limit,
		})
		if err != nil {
			return devices.Page[devices.AdapterInstance]{}, fmt.Errorf("list Adapters after cursor: %w", err)
		}
		views = make([]sqliteAdapterView, len(rows))
		for index, row := range rows {
			views[index] = sqliteAdapterView{
				adapterID: row.AdapterID, healthStatus: row.HealthStatus,
				healthReasonCode: row.HealthReasonCode, healthSource: row.HealthSource,
				healthSince: row.HealthSince, healthEvidenceAt: row.HealthEvidenceAt,
				healthSourceObservedAt: row.HealthSourceObservedAt, runtimeID: row.RuntimeID,
				softwareName: row.SoftwareName, softwareVersion: row.SoftwareVersion, claimedAt: row.ClaimedAt,
				lastHeartbeatAt: row.LastHeartbeatAt, leaseExpiresAt: row.LeaseExpiresAt, endedAt: row.EndedAt,
			}
		}
	}
	page := devices.Page[devices.AdapterInstance]{HasMore: len(views) > params.Limit}
	if page.HasMore {
		views = views[:params.Limit]
	}
	page.Items = make([]devices.AdapterInstance, len(views))
	for index, view := range views {
		instance, err := adapterInstanceFromView(view)
		if err != nil {
			return devices.Page[devices.AdapterInstance]{}, fmt.Errorf("map listed Adapter: %w", err)
		}
		page.Items[index] = instance
	}
	return page, nil
}

func (repository *DeviceRepository) ListAdapterHealthHistory(
	ctx context.Context,
	params devices.ListAdapterHealthParams,
) (devices.Page[devices.HealthTransition], error) {
	if _, err := repository.GetAdapter(ctx, params.AdapterID); err != nil {
		return devices.Page[devices.HealthTransition]{}, err
	}
	queries := repository.queries
	transitions, err := listAdapterTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return devices.Page[devices.HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listAdapterTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.ListAdapterHealthParams,
	limit int64,
) ([]devices.HealthTransition, error) {
	if params.BeforeReceiveOrder == nil {
		return listFirstAdapterTransitions(ctx, queries, params.AdapterID, limit)
	}
	return listAdapterTransitionsBefore(ctx, queries, params.AdapterID, *params.BeforeReceiveOrder, limit)
}

func listFirstAdapterTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	limit int64,
) ([]devices.HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryFirstPage(ctx, dbsqlc.ListAdapterHealthHistoryFirstPageParams{
		AdapterID: adapterID, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listAdapterTransitionsBefore(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	before int64,
	limit int64,
) ([]devices.HealthTransition, error) {
	rows, err := queries.ListAdapterHealthHistoryBefore(ctx, dbsqlc.ListAdapterHealthHistoryBeforeParams{
		AdapterID: adapterID, ReceiveOrder: before, Limit: limit,
	})
	if err != nil {
		return nil, fmt.Errorf("list Adapter health history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func (repository *DeviceRepository) ListEntityAvailabilityHistory(
	ctx context.Context,
	params devices.ListEntityAvailabilityParams,
) (devices.Page[devices.HealthTransition], error) {
	var exists int
	if err := repository.database.QueryRowContext(ctx, "SELECT 1 FROM entities WHERE id = ?", params.EntityID).
		Scan(&exists); errors.Is(err, sql.ErrNoRows) {
		return devices.Page[devices.HealthTransition]{}, devices.ErrEntityNotFound
	} else if err != nil {
		return devices.Page[devices.HealthTransition]{}, fmt.Errorf("get Entity for availability history: %w", err)
	}
	queries := repository.queries
	transitions, err := listEntityTransitions(ctx, queries, params, int64(params.Limit+1))
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
) ([]devices.HealthTransition, error) {
	if params.BeforeReceiveOrder == nil {
		return listFirstEntityTransitions(ctx, queries, params.EntityID, limit)
	}
	return listEntityTransitionsBefore(ctx, queries, params.EntityID, *params.BeforeReceiveOrder, limit)
}

func listFirstEntityTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entityID devices.EntityID,
	limit int64,
) ([]devices.HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryFirstPage(
		ctx,
		dbsqlc.ListEntityAvailabilityHistoryFirstPageParams{
			EntityID: nullableText(string(entityID)), PageLimit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}

func listEntityTransitionsBefore(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entityID devices.EntityID,
	before int64,
	limit int64,
) ([]devices.HealthTransition, error) {
	rows, err := queries.ListEntityAvailabilityHistoryBefore(
		ctx,
		dbsqlc.ListEntityAvailabilityHistoryBeforeParams{
			EntityID: nullableText(string(entityID)), BeforeReceiveOrder: before, PageLimit: limit,
		},
	)
	if err != nil {
		return nil, fmt.Errorf("list Entity availability history before cursor: %w", err)
	}
	values := make([]sqliteTransition, len(rows))
	for index, row := range rows {
		values[index] = newSQLiteTransition(
			row.ReceiveOrder, row.Status, row.Source, row.ReasonCode,
			row.SourceObservedAt, row.ObservedAt,
		)
	}
	return healthTransitionsFromValues(values)
}
