package devices

import (
	"context"
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

func (repository *SQLiteRepository) GetDevice(ctx context.Context, id DeviceID) (DeviceAggregate, error) {
	rows, err := statesqlc.New(repository.database).GetDevice(ctx, statesqlc.GetDeviceParams{ID: string(id)})
	if err != nil {
		return DeviceAggregate{}, fmt.Errorf("get device: %w", err)
	}
	if len(rows) == 0 {
		return DeviceAggregate{}, ErrDeviceNotFound
	}
	aggregate := DeviceAggregate{
		Device:   Device{ID: DeviceID(rows[0].DeviceID), Kind: DeviceKind(rows[0].DeviceKind), Name: rows[0].DeviceName},
		Entities: make([]EntityWithState, 0, len(rows)),
	}
	for _, row := range rows {
		if !row.EntityID.Valid {
			continue
		}
		if !row.EntityDeviceID.Valid || !row.AdapterID.Valid || !row.EntityName.Valid ||
			!row.TypeID.Valid || !row.SupportJson.Valid {
			return DeviceAggregate{}, errors.New("device entity row is incomplete")
		}
		view, err := entityWithStateFromValues(
			row.EntityID.String, row.EntityDeviceID.String, row.AdapterID.String,
			row.EntityName.String, row.TypeID.String, row.SupportJson.String,
			row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
			row.ObservedAt, row.ReceiveOrder,
		)
		if err != nil {
			return DeviceAggregate{}, fmt.Errorf("map device entity: %w", err)
		}
		aggregate.Entities = append(aggregate.Entities, view)
	}
	return aggregate, nil
}

func (repository *SQLiteRepository) ListEntities(ctx context.Context, params ListEntitiesParams) (Page[EntityWithState], error) {
	if !validPageLimit(params.Limit) {
		return Page[EntityWithState]{}, ErrInvalidPage
	}
	afterID := ""
	if params.AfterID != nil {
		afterID = string(*params.AfterID)
	}
	queries := statesqlc.New(repository.database)
	items := make([]EntityWithState, 0, params.Limit+1)
	if params.DeviceID == nil {
		rows, err := queries.ListEntities(ctx, statesqlc.ListEntitiesParams{
			ID: afterID, Limit: int64(params.Limit + 1),
		})
		if err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("list entities: %w", err)
		}
		for _, row := range rows {
			view, err := entityWithStateFromValues(
				row.ID, row.DeviceID, row.AdapterID, row.Name, row.TypeID, row.SupportJson,
				row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
				row.ObservedAt, row.ReceiveOrder,
			)
			if err != nil {
				return Page[EntityWithState]{}, fmt.Errorf("map entity: %w", err)
			}
			items = append(items, view)
		}
	} else {
		rows, err := queries.ListEntitiesByDevice(ctx, statesqlc.ListEntitiesByDeviceParams{
			DeviceID: string(*params.DeviceID), ID: afterID, Limit: int64(params.Limit + 1),
		})
		if err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("list entities by device: %w", err)
		}
		for _, row := range rows {
			view, err := entityWithStateFromValues(
				row.ID, row.DeviceID, row.AdapterID, row.Name, row.TypeID, row.SupportJson,
				row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
				row.ObservedAt, row.ReceiveOrder,
			)
			if err != nil {
				return Page[EntityWithState]{}, fmt.Errorf("map entity: %w", err)
			}
			items = append(items, view)
		}
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *SQLiteRepository) ListEntityCommands(ctx context.Context, params ListEntityCommandsParams) (Page[CommandRecord], error) {
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
		requestedAt := formatTime(*params.BeforeRequestedAt)
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
		command, err := commandFromRow(row)
		if err != nil {
			return Page[CommandRecord]{}, fmt.Errorf("map entity command: %w", err)
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
