package devices

import (
	"context"
	"fmt"
)

func (service *Service) ListDevices(ctx context.Context, params ListDevicesParams) (Page[Device], error) {
	if !validPageLimit(params.Limit) {
		return Page[Device]{}, ErrInvalidPage
	}
	if params.AfterID != nil {
		if _, err := ParseDeviceID(string(*params.AfterID)); err != nil {
			return Page[Device]{}, fmt.Errorf("%w: parse device position: %w", ErrInvalidPage, err)
		}
	}
	page, err := service.repository.ListDevices(ctx, params)
	if err != nil {
		return Page[Device]{}, err
	}
	items := make([]Device, len(page.Items))
	copy(items, page.Items)
	return Page[Device]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetDevice(ctx context.Context, params GetDeviceParams) (DeviceAggregate, error) {
	if !validPageLimit(params.EntityLimit) {
		return DeviceAggregate{}, ErrInvalidPage
	}
	if _, err := ParseDeviceID(string(params.ID)); err != nil {
		return DeviceAggregate{}, fmt.Errorf("parse device ID: %w", err)
	}
	if params.AfterEntityID != nil {
		if _, err := ParseEntityID(string(*params.AfterEntityID)); err != nil {
			return DeviceAggregate{}, fmt.Errorf("%w: parse entity position: %w", ErrInvalidPage, err)
		}
	}
	aggregate, err := service.repository.GetDevice(ctx, params)
	if err != nil {
		return DeviceAggregate{}, err
	}
	items := make([]EntityWithState, len(aggregate.Entities.Items))
	for index, entity := range aggregate.Entities.Items {
		items[index] = copyEntityWithState(entity)
	}
	return DeviceAggregate{
		Device: aggregate.Device,
		Entities: Page[EntityWithState]{
			Items: items, HasMore: aggregate.Entities.HasMore,
		},
	}, nil
}

func (service *Service) ListEntities(ctx context.Context, params ListEntitiesParams) (Page[EntityWithState], error) {
	if !validPageLimit(params.Limit) {
		return Page[EntityWithState]{}, ErrInvalidPage
	}
	if params.DeviceID != nil {
		if _, err := ParseDeviceID(string(*params.DeviceID)); err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("%w: parse device filter: %w", ErrInvalidPage, err)
		}
	}
	if params.AfterID != nil {
		if _, err := ParseEntityID(string(*params.AfterID)); err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("%w: parse entity position: %w", ErrInvalidPage, err)
		}
	}
	page, err := service.repository.ListEntities(ctx, params)
	if err != nil {
		return Page[EntityWithState]{}, err
	}
	items := make([]EntityWithState, len(page.Items))
	for index, entity := range page.Items {
		items[index] = copyEntityWithState(entity)
	}
	return Page[EntityWithState]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityWithState, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityWithState{}, fmt.Errorf("parse entity ID: %w", err)
	}
	view, err := service.repository.GetEntity(ctx, id)
	if err != nil {
		return EntityWithState{}, err
	}
	return copyEntityWithState(view), nil
}

func (service *Service) GetCommand(ctx context.Context, id CommandID) (CommandRecord, error) {
	if _, err := ParseCommandID(string(id)); err != nil {
		return CommandRecord{}, fmt.Errorf("parse command ID: %w", err)
	}
	command, err := service.repository.GetCommand(ctx, id)
	if err != nil {
		return CommandRecord{}, err
	}
	return copyCommandRecord(command), nil
}

func (service *Service) ListEntityCommands(
	ctx context.Context,
	params ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	if !validPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return Page[CommandRecord]{}, ErrInvalidPage
	}
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return Page[CommandRecord]{}, fmt.Errorf("%w: parse entity ID: %w", ErrInvalidPage, err)
	}
	if params.BeforeRequestedAt != nil {
		if params.BeforeRequestedAt.IsZero() {
			return Page[CommandRecord]{}, fmt.Errorf("%w: command position timestamp is required", ErrInvalidPage)
		}
		if _, err := ParseCommandID(string(*params.BeforeID)); err != nil {
			return Page[CommandRecord]{}, fmt.Errorf("%w: parse command position: %w", ErrInvalidPage, err)
		}
		utc := params.BeforeRequestedAt.UTC()
		params.BeforeRequestedAt = &utc
	}
	if _, err := service.repository.GetEntity(ctx, params.EntityID); err != nil {
		return Page[CommandRecord]{}, err
	}
	page, err := service.repository.ListEntityCommands(ctx, params)
	if err != nil {
		return Page[CommandRecord]{}, err
	}
	items := make([]CommandRecord, len(page.Items))
	for index, command := range page.Items {
		items[index] = copyCommandRecord(command)
	}
	return Page[CommandRecord]{Items: items, HasMore: page.HasMore}, nil
}

func copyCommandRecord(command CommandRecord) CommandRecord {
	copy := command
	copy.Parameters = append(CommandParameters(nil), command.Parameters...)
	if command.AcceptedAt != nil {
		value := *command.AcceptedAt
		copy.AcceptedAt = &value
	}
	if command.CompletedAt != nil {
		value := *command.CompletedAt
		copy.CompletedAt = &value
	}
	if command.OutcomeObservationID != nil {
		value := *command.OutcomeObservationID
		copy.OutcomeObservationID = &value
	}
	if command.FailureCode != nil {
		value := *command.FailureCode
		copy.FailureCode = &value
	}
	return copy
}
