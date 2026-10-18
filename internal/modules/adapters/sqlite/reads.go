package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
	"github.com/mholtzscher/hearth/internal/modules/adapters/sqlite/dbsqlc"
)

func (repository *AdapterRepository) GetAdapter(
	ctx context.Context,
	adapterID string,
) (adapters.AdapterInstance, error) {
	if err := adapters.ValidateAdapterID(adapterID); err != nil {
		return adapters.AdapterInstance{}, err
	}
	row, err := repository.queries.GetAdapterView(ctx, dbsqlc.GetAdapterViewParams{
		AdapterID: adapterID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return adapters.AdapterInstance{}, adapters.ErrAdapterNotFound
	}
	if err != nil {
		return adapters.AdapterInstance{}, fmt.Errorf("get Adapter: %w", err)
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

func (repository *AdapterRepository) ListAdapters(
	ctx context.Context,
	params adapters.ListAdaptersParams,
) (adapters.Page[adapters.AdapterInstance], error) {
	if !adapters.ValidPageLimit(params.Limit) ||
		(params.AfterID != nil && adapters.ValidateAdapterID(*params.AfterID) != nil) {
		return adapters.Page[adapters.AdapterInstance]{}, adapters.ErrInvalidPage
	}
	queries := repository.queries
	limit := int64(params.Limit + 1)
	var views []sqliteAdapterView
	if params.AfterID == nil {
		rows, err := queries.ListAdapterViewsFirstPage(ctx, dbsqlc.ListAdapterViewsFirstPageParams{
			PageLimit: limit,
		})
		if err != nil {
			return adapters.Page[adapters.AdapterInstance]{}, fmt.Errorf("list Adapters: %w", err)
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
			return adapters.Page[adapters.AdapterInstance]{}, fmt.Errorf("list Adapters after cursor: %w", err)
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
	page := adapters.Page[adapters.AdapterInstance]{HasMore: len(views) > params.Limit}
	if page.HasMore {
		views = views[:params.Limit]
	}
	page.Items = make([]adapters.AdapterInstance, len(views))
	for index, view := range views {
		instance, err := adapterInstanceFromView(view)
		if err != nil {
			return adapters.Page[adapters.AdapterInstance]{}, fmt.Errorf("map listed Adapter: %w", err)
		}
		page.Items[index] = instance
	}
	return page, nil
}

func (repository *AdapterRepository) ListAdapterHealthHistory(
	ctx context.Context,
	params adapters.ListAdapterHealthParams,
) (adapters.Page[adapters.HealthTransition], error) {
	if !adapters.ValidPageLimit(params.Limit) || (params.BeforeReceiveOrder != nil && *params.BeforeReceiveOrder < 1) {
		return adapters.Page[adapters.HealthTransition]{}, adapters.ErrInvalidPage
	}
	if _, err := repository.GetAdapter(ctx, params.AdapterID); err != nil {
		return adapters.Page[adapters.HealthTransition]{}, err
	}
	queries := repository.queries
	transitions, err := listAdapterTransitions(ctx, queries, params, int64(params.Limit+1))
	if err != nil {
		return adapters.Page[adapters.HealthTransition]{}, err
	}
	return trimTransitionPage(transitions, params.Limit), nil
}

func listAdapterTransitions(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params adapters.ListAdapterHealthParams,
	limit int64,
) ([]adapters.HealthTransition, error) {
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
) ([]adapters.HealthTransition, error) {
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
) ([]adapters.HealthTransition, error) {
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
