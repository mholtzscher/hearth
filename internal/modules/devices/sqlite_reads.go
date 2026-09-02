package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

func (repository *SQLiteRepository) ListDevices(ctx context.Context, params ListDevicesParams) (Page[Device], error) {
	if !validPageLimit(params.Limit) {
		return Page[Device]{}, ErrInvalidPage
	}
	afterID := ""
	if params.AfterID != nil {
		afterID = string(*params.AfterID)
	}
	rows, err := repository.queries.ListDevices(ctx, dbsqlc.ListDevicesParams{
		ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return Page[Device]{}, fmt.Errorf("list devices: %w", err)
	}
	items := make([]Device, len(rows))
	for index, row := range rows {
		items[index] = Device{ID: DeviceID(row.ID), Kind: DeviceKind(row.Kind), Name: row.Name}
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *SQLiteRepository) GetDevice(ctx context.Context, params GetDeviceParams) (DeviceAggregate, error) {
	if !validPageLimit(params.EntityLimit) {
		return DeviceAggregate{}, ErrInvalidPage
	}
	queries := repository.queries
	row, err := queries.GetDevice(ctx, dbsqlc.GetDeviceParams{ID: string(params.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceAggregate{}, ErrDeviceNotFound
	}
	if err != nil {
		return DeviceAggregate{}, fmt.Errorf("get device: %w", err)
	}
	deviceID := params.ID
	entities, err := repository.ListEntities(ctx, ListEntitiesParams{
		DeviceID: &deviceID, AfterID: params.AfterEntityID, Limit: params.EntityLimit,
	})
	if err != nil {
		return DeviceAggregate{}, err
	}
	return DeviceAggregate{
		Device:   Device{ID: DeviceID(row.ID), Kind: DeviceKind(row.Kind), Name: row.Name},
		Entities: entities,
	}, nil
}

func (repository *SQLiteRepository) ListEntities(
	ctx context.Context,
	params ListEntitiesParams,
) (Page[EntityWithState], error) {
	if !validPageLimit(params.Limit) {
		return Page[EntityWithState]{}, ErrInvalidPage
	}
	afterID := ""
	if params.AfterID != nil {
		afterID = string(*params.AfterID)
	}
	queries := repository.queries
	if params.DeviceID != nil {
		return listEntitiesByDevice(ctx, queries, params, afterID)
	}
	rows, err := queries.ListEntities(ctx, dbsqlc.ListEntitiesParams{
		ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return Page[EntityWithState]{}, fmt.Errorf("list entities: %w", err)
	}
	return entityPageFromRows(rows, params.Limit)
}

func listEntitiesByDevice(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params ListEntitiesParams,
	afterID string,
) (Page[EntityWithState], error) {
	rows, err := queries.ListEntitiesByDevice(ctx, dbsqlc.ListEntitiesByDeviceParams{
		DeviceID: string(*params.DeviceID), ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return Page[EntityWithState]{}, fmt.Errorf("list entities by device: %w", err)
	}
	return entityPageFromRows(rows, params.Limit)
}

func (repository *SQLiteRepository) ListEntityCommands(
	ctx context.Context,
	params ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	if !validPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return Page[CommandRecord]{}, ErrInvalidPage
	}
	queries := repository.queries
	var rows []dbsqlc.Command
	var err error
	if params.BeforeRequestedAt == nil {
		rows, err = queries.ListEntityCommandsFirstPage(ctx, dbsqlc.ListEntityCommandsFirstPageParams{
			EntityID: string(params.EntityID), Limit: int64(params.Limit + 1),
		})
	} else {
		requestedAt := formatSortableTime(*params.BeforeRequestedAt)
		rows, err = queries.ListEntityCommandsAfter(ctx, dbsqlc.ListEntityCommandsAfterParams{
			EntityID: string(params.EntityID), RequestedAt: requestedAt, RequestedAt_2: requestedAt,
			ID: string(*params.BeforeID), Limit: int64(params.Limit + 1),
		})
	}
	if err != nil {
		return Page[CommandRecord]{}, fmt.Errorf("list entity commands: %w", err)
	}
	items := make([]CommandRecord, 0, len(rows))
	for _, row := range rows {
		command, mappingErr := commandFromRow(row)
		if mappingErr != nil {
			return Page[CommandRecord]{}, fmt.Errorf("map entity command: %w", mappingErr)
		}
		items = append(items, command)
	}
	return pageFromExtra(items, params.Limit), nil
}

func entityPageFromRows(rows []dbsqlc.EntityReadProjection, limit int) (Page[EntityWithState], error) {
	items := make([]EntityWithState, 0, len(rows))
	for _, row := range rows {
		view, err := entityWithStateFromRow(row)
		if err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("map entity: %w", err)
		}
		items = append(items, view)
	}
	return pageFromExtra(items, limit), nil
}

func validPageLimit(limit int) bool {
	return limit >= 1 && limit <= 200
}

func pageFromExtra[T any](items []T, limit int) Page[T] {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = make([]T, 0)
	}
	return Page[T]{Items: items, HasMore: hasMore}
}
