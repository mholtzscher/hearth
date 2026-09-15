package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ListDevices(
	ctx context.Context,
	params devices.ListDevicesParams,
) (devices.Page[devices.Device], error) {
	if !devices.ValidPageLimit(params.Limit) {
		return devices.Page[devices.Device]{}, devices.ErrInvalidPage
	}
	afterID := ""
	if params.AfterID != nil {
		afterID = string(*params.AfterID)
	}
	rows, err := repository.queries.ListDevices(ctx, dbsqlc.ListDevicesParams{
		ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return devices.Page[devices.Device]{}, fmt.Errorf("list devices: %w", err)
	}
	items := make([]devices.Device, len(rows))
	for index, row := range rows {
		items[index] = devices.Device{ID: devices.DeviceID(row.ID), Kind: devices.DeviceKind(row.Kind), Name: row.Name}
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *DeviceRepository) GetDevice(
	ctx context.Context,
	params devices.GetDeviceParams,
) (devices.DeviceAggregate, error) {
	if !devices.ValidPageLimit(params.EntityLimit) {
		return devices.DeviceAggregate{}, devices.ErrInvalidPage
	}
	queries := repository.queries
	row, err := queries.GetDevice(ctx, dbsqlc.GetDeviceParams{ID: string(params.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.DeviceAggregate{}, devices.ErrDeviceNotFound
	}
	if err != nil {
		return devices.DeviceAggregate{}, fmt.Errorf("get device: %w", err)
	}
	deviceID := params.ID
	entities, err := repository.ListEntities(ctx, devices.ListEntitiesParams{
		DeviceID: &deviceID, AfterID: params.AfterEntityID, Limit: params.EntityLimit,
	})
	if err != nil {
		return devices.DeviceAggregate{}, err
	}
	return devices.DeviceAggregate{
		Device:   devices.Device{ID: devices.DeviceID(row.ID), Kind: devices.DeviceKind(row.Kind), Name: row.Name},
		Entities: entities,
	}, nil
}

func (repository *DeviceRepository) ListEntities(
	ctx context.Context,
	params devices.ListEntitiesParams,
) (devices.Page[devices.EntityWithState], error) {
	if !devices.ValidPageLimit(params.Limit) {
		return devices.Page[devices.EntityWithState]{}, devices.ErrInvalidPage
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
		return devices.Page[devices.EntityWithState]{}, fmt.Errorf("list entities: %w", err)
	}
	return entityPageFromRows(rows, params.Limit)
}

func listEntitiesByDevice(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.ListEntitiesParams,
	afterID string,
) (devices.Page[devices.EntityWithState], error) {
	rows, err := queries.ListEntitiesByDevice(ctx, dbsqlc.ListEntitiesByDeviceParams{
		DeviceID: string(*params.DeviceID), ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return devices.Page[devices.EntityWithState]{}, fmt.Errorf("list entities by device: %w", err)
	}
	return entityPageFromRows(rows, params.Limit)
}

func (repository *DeviceRepository) ListEntityCommands(
	ctx context.Context,
	params devices.ListEntityCommandsParams,
) (devices.Page[devices.CommandRecord], error) {
	if !devices.ValidPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return devices.Page[devices.CommandRecord]{}, devices.ErrInvalidPage
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
		return devices.Page[devices.CommandRecord]{}, fmt.Errorf("list entity commands: %w", err)
	}
	items := make([]devices.CommandRecord, 0, len(rows))
	for _, row := range rows {
		command, mappingErr := commandFromRow(row)
		if mappingErr != nil {
			return devices.Page[devices.CommandRecord]{}, fmt.Errorf("map entity command: %w", mappingErr)
		}
		items = append(items, command)
	}
	return pageFromExtra(items, params.Limit), nil
}

func entityPageFromRows(rows []dbsqlc.EntityReadProjection, limit int) (devices.Page[devices.EntityWithState], error) {
	items := make([]devices.EntityWithState, 0, len(rows))
	for _, row := range rows {
		view, err := entityWithStateFromRow(row)
		if err != nil {
			return devices.Page[devices.EntityWithState]{}, fmt.Errorf("map entity: %w", err)
		}
		items = append(items, view)
	}
	return pageFromExtra(items, limit), nil
}

func pageFromExtra[T any](items []T, limit int) devices.Page[T] {
	hasMore := len(items) > limit
	if hasMore {
		items = items[:limit]
	}
	if items == nil {
		items = make([]T, 0)
	}
	return devices.Page[T]{Items: items, HasMore: hasMore}
}

func (repository *DeviceRepository) GetEntity(
	ctx context.Context,
	id devices.EntityID,
) (devices.EntityWithState, error) {
	row, err := repository.queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.EntityWithState{}, devices.ErrEntityNotFound
	}
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("get entity: %w", err)
	}
	return entityWithStateFromRow(row)
}
