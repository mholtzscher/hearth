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
			return Page[Device]{}, fmt.Errorf("%w: parse device position: %v", ErrInvalidPage, err)
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

func (service *Service) GetDevice(ctx context.Context, id DeviceID) (DeviceAggregate, error) {
	if _, err := ParseDeviceID(string(id)); err != nil {
		return DeviceAggregate{}, fmt.Errorf("parse device ID: %w", err)
	}
	aggregate, err := service.repository.GetDevice(ctx, id)
	if err != nil {
		return DeviceAggregate{}, err
	}
	copy := DeviceAggregate{Device: aggregate.Device, Entities: make([]EntityWithState, len(aggregate.Entities))}
	for index, entity := range aggregate.Entities {
		copy.Entities[index] = copyEntityWithState(entity)
	}
	return copy, nil
}

func (service *Service) ListEntities(ctx context.Context, params ListEntitiesParams) (Page[EntityWithState], error) {
	if !validPageLimit(params.Limit) {
		return Page[EntityWithState]{}, ErrInvalidPage
	}
	if params.DeviceID != nil {
		if _, err := ParseDeviceID(string(*params.DeviceID)); err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("%w: parse device filter: %v", ErrInvalidPage, err)
		}
	}
	if params.AfterID != nil {
		if _, err := ParseEntityID(string(*params.AfterID)); err != nil {
			return Page[EntityWithState]{}, fmt.Errorf("%w: parse entity position: %v", ErrInvalidPage, err)
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

func (service *Service) ListEntityCommands(ctx context.Context, params ListEntityCommandsParams) (Page[CommandRecord], error) {
	if !validPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return Page[CommandRecord]{}, ErrInvalidPage
	}
	if _, err := ParseEntityID(string(params.EntityID)); err != nil {
		return Page[CommandRecord]{}, fmt.Errorf("%w: parse entity ID: %v", ErrInvalidPage, err)
	}
	if params.BeforeRequestedAt != nil {
		if params.BeforeRequestedAt.IsZero() {
			return Page[CommandRecord]{}, fmt.Errorf("%w: command position timestamp is required", ErrInvalidPage)
		}
		if _, err := ParseCommandID(string(*params.BeforeID)); err != nil {
			return Page[CommandRecord]{}, fmt.Errorf("%w: parse command position: %v", ErrInvalidPage, err)
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
