package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
	statesqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/state"
)

func (repository *SQLiteRepository) ListDevices(ctx context.Context, params ListDevicesParams) (Page[Device], error) {
	if !validPageLimit(params.Limit) {
		return Page[Device]{}, ErrInvalidPage
	}
	afterID := ""
	if params.AfterID != nil {
		afterID = string(*params.AfterID)
	}
	rows, err := statesqlc.New(repository.database).ListDevices(ctx, statesqlc.ListDevicesParams{
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
	queries := statesqlc.New(repository.database)
	row, err := queries.GetDevice(ctx, statesqlc.GetDeviceParams{ID: string(params.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return DeviceAggregate{}, ErrDeviceNotFound
	}
	if err != nil {
		return DeviceAggregate{}, fmt.Errorf("get device: %w", err)
	}
	if !row.AdapterID.Valid || row.AdapterID.String == "" || !row.BindingKey.Valid || row.BindingKey.String == "" ||
		(row.ExternalDeviceID.Valid && row.ExternalDeviceID.String == "") {
		return DeviceAggregate{}, errors.New("map device binding: device binding row is incomplete")
	}
	binding := DeviceBinding{AdapterID: row.AdapterID.String, BindingKey: row.BindingKey.String}
	if row.ExternalDeviceID.Valid {
		externalDeviceID := row.ExternalDeviceID.String
		binding.ExternalDeviceID = &externalDeviceID
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
		Binding:  binding,
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
	queries := statesqlc.New(repository.database)
	if params.DeviceID != nil {
		return listEntitiesByDevice(ctx, queries, params, afterID)
	}
	rows, err := queries.ListEntities(ctx, statesqlc.ListEntitiesParams{
		ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return Page[EntityWithState]{}, fmt.Errorf("list entities: %w", err)
	}
	items := make([]EntityWithState, 0, params.Limit+1)
	for _, row := range rows {
		view, mappingErr := entityWithStateFromValues(
			row.ID, row.DeviceID, row.AdapterID, row.BindingKey, row.EntityKey, row.ExternalEntityID,
			row.Name, row.TypeID, row.SupportJson, row.Enabled, row.ObservationID, row.ValueJson,
			row.AdapterReceivedAt, row.SourceUpdatedAt, row.ObservedAt, row.ReceiveOrder,
		)
		if mappingErr != nil {
			return Page[EntityWithState]{}, fmt.Errorf("map entity: %w", mappingErr)
		}
		items = append(items, view)
	}
	return pageFromExtra(items, params.Limit), nil
}

func listEntitiesByDevice(
	ctx context.Context,
	queries *statesqlc.Queries,
	params ListEntitiesParams,
	afterID string,
) (Page[EntityWithState], error) {
	rows, err := queries.ListEntitiesByDevice(ctx, statesqlc.ListEntitiesByDeviceParams{
		DeviceID: string(*params.DeviceID), ID: afterID, Limit: int64(params.Limit + 1),
	})
	if err != nil {
		return Page[EntityWithState]{}, fmt.Errorf("list entities by device: %w", err)
	}
	items := make([]EntityWithState, 0, params.Limit+1)
	for _, row := range rows {
		view, mappingErr := entityWithStateFromValues(
			row.ID, row.DeviceID, row.AdapterID, row.BindingKey, row.EntityKey, row.ExternalEntityID,
			row.Name, row.TypeID, row.SupportJson, row.Enabled, row.ObservationID, row.ValueJson,
			row.AdapterReceivedAt, row.SourceUpdatedAt, row.ObservedAt, row.ReceiveOrder,
		)
		if mappingErr != nil {
			return Page[EntityWithState]{}, fmt.Errorf("map entity: %w", mappingErr)
		}
		items = append(items, view)
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *SQLiteRepository) ListEntityCommands(
	ctx context.Context,
	params ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	if !validPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return Page[CommandRecord]{}, ErrInvalidPage
	}
	queries := commandsqlc.New(repository.database)
	var rows []commandsqlc.Command
	var err error
	if params.BeforeRequestedAt == nil {
		rows, err = queries.ListEntityCommandsFirstPage(ctx, commandsqlc.ListEntityCommandsFirstPageParams{
			EntityID: string(params.EntityID), Limit: int64(params.Limit + 1),
		})
	} else {
		requestedAt := formatSortableTime(*params.BeforeRequestedAt)
		rows, err = queries.ListEntityCommandsAfter(ctx, commandsqlc.ListEntityCommandsAfterParams{
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
