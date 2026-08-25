package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	registrationsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/registration"
)

func (service *Service) registerBinding(ctx context.Context, params registerBindingParams) (Binding, error) {
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Binding{}, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer tx.Rollback()
	queries := registrationsqlc.New(tx)
	updatedAt := formatTime(params.UpdatedAt)

	binding, err := queries.GetBinding(ctx, registrationsqlc.GetBindingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey,
	})
	var deviceID DeviceID
	switch {
	case err == nil:
		deviceID = DeviceID(binding.DeviceID)
		if err := queries.UpdateDeviceDescriptor(ctx, registrationsqlc.UpdateDeviceDescriptorParams{
			Kind: string(params.Device.Kind), Name: params.Device.Name, UpdatedAt: updatedAt, ID: binding.DeviceID,
		}); err != nil {
			return Binding{}, fmt.Errorf("update device descriptor: %w", err)
		}
		if err := queries.UpdateBindingExternalID(ctx, registrationsqlc.UpdateBindingExternalIDParams{
			ExternalDeviceID: nullableString(params.Device.ExternalID), UpdatedAt: updatedAt,
			AdapterID: params.AdapterID, BindingKey: params.BindingKey,
		}); err != nil {
			return Binding{}, mapRegistrationWriteError("update binding external ID", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		deviceID = params.DeviceID
		if err := queries.CreateDevice(ctx, registrationsqlc.CreateDeviceParams{
			ID: string(deviceID), Kind: string(params.Device.Kind), Name: params.Device.Name,
			CreatedAt: updatedAt, UpdatedAt: updatedAt,
		}); err != nil {
			return Binding{}, fmt.Errorf("create device: %w", err)
		}
		if err := queries.CreateBinding(ctx, registrationsqlc.CreateBindingParams{
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, DeviceID: string(deviceID),
			ExternalDeviceID: nullableString(params.Device.ExternalID), CreatedAt: updatedAt, UpdatedAt: updatedAt,
		}); err != nil {
			return Binding{}, mapRegistrationWriteError("create binding", err)
		}
	default:
		return Binding{}, fmt.Errorf("get binding: %w", err)
	}

	mapping, err := queries.GetEntityMapping(ctx, registrationsqlc.GetEntityMappingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: params.Entity.Key,
	})
	var entityID EntityID
	switch {
	case err == nil:
		entityID = EntityID(mapping.EntityID)
		if mapping.DeviceID != string(deviceID) {
			return Binding{}, errors.New("entity mapping references a different device")
		}
		if EntityTypeID(mapping.TypeID) != params.Entity.TypeID {
			return Binding{}, errImmutableTypeChange
		}
		if err := queries.UpdateEntityDescriptor(ctx, registrationsqlc.UpdateEntityDescriptorParams{
			Name: params.Entity.Name, SupportJson: string(params.Entity.Support), UpdatedAt: updatedAt, ID: mapping.EntityID,
		}); err != nil {
			return Binding{}, fmt.Errorf("update entity descriptor: %w", err)
		}
		if err := queries.UpdateEntityMappingExternalID(ctx, registrationsqlc.UpdateEntityMappingExternalIDParams{
			ExternalEntityID: params.Entity.ExternalID, UpdatedAt: updatedAt,
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: params.Entity.Key,
		}); err != nil {
			return Binding{}, mapRegistrationWriteError("update entity external ID", err)
		}
	case errors.Is(err, sql.ErrNoRows):
		entityID = params.EntityID
		if err := queries.CreateEntity(ctx, registrationsqlc.CreateEntityParams{
			ID: string(entityID), DeviceID: string(deviceID), Name: params.Entity.Name,
			TypeID: string(params.Entity.TypeID), SupportJson: string(params.Entity.Support),
			CreatedAt: updatedAt, UpdatedAt: updatedAt,
		}); err != nil {
			return Binding{}, fmt.Errorf("create entity: %w", err)
		}
		if err := queries.CreateEntityMapping(ctx, registrationsqlc.CreateEntityMappingParams{
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: params.Entity.Key,
			EntityID: string(entityID), ExternalEntityID: params.Entity.ExternalID,
			CreatedAt: updatedAt, UpdatedAt: updatedAt,
		}); err != nil {
			return Binding{}, mapRegistrationWriteError("create entity mapping", err)
		}
	default:
		return Binding{}, fmt.Errorf("get entity mapping: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return Binding{}, fmt.Errorf("commit registration: %w", err)
	}
	return Binding{
		BindingKey: params.BindingKey,
		DeviceID:   deviceID,
		Entities:   []EntityBinding{{Key: params.Entity.Key, EntityID: entityID}},
	}, nil
}

func mapRegistrationWriteError(action string, err error) error {
	if isUniqueConstraint(err) {
		return fmt.Errorf("%s: %w", action, errIdentityConflict)
	}
	return fmt.Errorf("%s: %w", action, err)
}

func isUniqueConstraint(err error) bool {
	type sqliteError interface{ Code() int }
	var databaseError sqliteError
	if !errors.As(err, &databaseError) {
		return false
	}
	code := databaseError.Code()
	return code == 1555 || code == 2067
}
