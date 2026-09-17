package devices

import (
	"context"
	"fmt"
)

func (service *Service) ListDevices(ctx context.Context, params ListDevicesParams) (Page[Device], error) {
	if !ValidPageLimit(params.Limit) {
		return Page[Device]{}, ErrInvalidPage
	}
	if params.AfterID != nil {
		if _, err := ParseDeviceID(string(*params.AfterID)); err != nil {
			return Page[Device]{}, fmt.Errorf("%w: parse device position: %w", ErrInvalidPage, err)
		}
	}
	page, err := service.stores.Reads.ListDevices(ctx, params)
	if err != nil {
		return Page[Device]{}, err
	}
	items := make([]Device, len(page.Items))
	copy(items, page.Items)
	return Page[Device]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetDevice(ctx context.Context, params GetDeviceParams) (DeviceAggregate, error) {
	if !ValidPageLimit(params.EntityLimit) {
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
	aggregate, err := service.stores.Reads.GetDevice(ctx, params)
	if err != nil {
		return DeviceAggregate{}, err
	}
	items := make([]EntityWithState, len(aggregate.Entities.Items))
	for index, entity := range aggregate.Entities.Items {
		items[index] = CopyEntityWithState(entity)
	}
	return DeviceAggregate{
		Device: aggregate.Device,
		Entities: Page[EntityWithState]{
			Items: items, HasMore: aggregate.Entities.HasMore,
		},
	}, nil
}

func (service *Service) ListEntities(ctx context.Context, params ListEntitiesParams) (Page[EntityWithState], error) {
	if !ValidPageLimit(params.Limit) {
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
	page, err := service.stores.Reads.ListEntities(ctx, params)
	if err != nil {
		return Page[EntityWithState]{}, err
	}
	items := make([]EntityWithState, len(page.Items))
	for index, entity := range page.Items {
		items[index] = CopyEntityWithState(entity)
	}
	return Page[EntityWithState]{Items: items, HasMore: page.HasMore}, nil
}

func (service *Service) GetEntity(ctx context.Context, id EntityID) (EntityWithState, error) {
	if _, err := ParseEntityID(string(id)); err != nil {
		return EntityWithState{}, fmt.Errorf("parse entity ID: %w", err)
	}
	view, err := service.stores.Reads.GetEntity(ctx, id)
	if err != nil {
		return EntityWithState{}, err
	}
	return CopyEntityWithState(view), nil
}

func (service *Service) GetCommand(ctx context.Context, id CommandID) (CommandRecord, error) {
	if _, err := ParseCommandID(string(id)); err != nil {
		return CommandRecord{}, fmt.Errorf("parse command ID: %w", err)
	}
	command, err := service.stores.Reads.GetCommand(ctx, id)
	if err != nil {
		return CommandRecord{}, err
	}
	return CopyCommandRecord(command), nil
}

func (service *Service) ListEntityCommands(
	ctx context.Context,
	params ListEntityCommandsParams,
) (Page[CommandRecord], error) {
	if !ValidPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
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
	if _, err := service.stores.Reads.GetEntity(ctx, params.EntityID); err != nil {
		return Page[CommandRecord]{}, err
	}
	page, err := service.stores.Reads.ListEntityCommands(ctx, params)
	if err != nil {
		return Page[CommandRecord]{}, err
	}
	items := make([]CommandRecord, len(page.Items))
	for index, command := range page.Items {
		items[index] = CopyCommandRecord(command)
	}
	return Page[CommandRecord]{Items: items, HasMore: page.HasMore}, nil
}

// ListCommands pages household-wide Command history newest-first. Unlike
// ListEntityCommands it never 404s: an unknown but canonical Entity filter
// matches nothing, mirroring the Entity list Device filter.
func (service *Service) ListCommands(
	ctx context.Context,
	params ListCommandsParams,
) (Page[CommandRecord], error) {
	if !ValidPageLimit(params.Limit) || (params.BeforeRequestedAt == nil) != (params.BeforeID == nil) {
		return Page[CommandRecord]{}, ErrInvalidPage
	}
	if params.EntityID != nil {
		if _, err := ParseEntityID(string(*params.EntityID)); err != nil {
			return Page[CommandRecord]{}, fmt.Errorf("%w: parse entity filter: %w", ErrInvalidPage, err)
		}
	}
	if params.Status != nil && !ValidCommandStatus(*params.Status) {
		return Page[CommandRecord]{}, fmt.Errorf("%w: unknown command status", ErrInvalidPage)
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
	page, err := service.stores.Reads.ListCommands(ctx, params)
	if err != nil {
		return Page[CommandRecord]{}, err
	}
	items := make([]CommandRecord, len(page.Items))
	for index, command := range page.Items {
		items[index] = CopyCommandRecord(command)
	}
	return Page[CommandRecord]{Items: items, HasMore: page.HasMore}, nil
}

// ValidPageLimit reports whether limit is a page size Core serves: at least one
// and at most 200 items. Services and persistence read guards share this policy.
func ValidPageLimit(limit int) bool {
	return limit >= 1 && limit <= 200
}

// CopyCommandRecord returns a CommandRecord that shares no mutable memory with
// command, so a caller cannot change a stored or returned record through arrays
// or pointers it still holds. Persistence adapters return owned records
// through it.
func CopyCommandRecord(command CommandRecord) CommandRecord {
	cloned := command
	cloned.Parameters = append(CommandParameters(nil), command.Parameters...)
	if command.RuntimeID != nil {
		value := *command.RuntimeID
		cloned.RuntimeID = &value
	}
	if command.AcceptedAt != nil {
		value := *command.AcceptedAt
		cloned.AcceptedAt = &value
	}
	if command.CompletedAt != nil {
		value := *command.CompletedAt
		cloned.CompletedAt = &value
	}
	if command.OutcomeObservationID != nil {
		value := *command.OutcomeObservationID
		cloned.OutcomeObservationID = &value
	}
	if command.FailureCode != nil {
		value := *command.FailureCode
		cloned.FailureCode = &value
	}
	return cloned
}
