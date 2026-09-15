package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

type entityReconciliation struct {
	params   devices.RegisterEntityParams
	mapping  dbsqlc.GetEntityMappingRow
	entityID devices.EntityID
	exists   bool
}

func (repository *DeviceRepository) RegisterBinding(
	ctx context.Context,
	params devices.RegisterBindingParams,
) (devices.Binding, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.Binding{}, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	if runtimeErr := checkRegistrationRuntime(ctx, queries, params.AdapterID, params.RuntimeID); runtimeErr != nil {
		return devices.Binding{}, runtimeErr
	}
	updatedAt := formatTime(params.UpdatedAt)

	deviceID, err := reconcileRegistrationDevice(ctx, queries, params, updatedAt)
	if err != nil {
		return devices.Binding{}, err
	}
	reconciliations, err := prepareEntityReconciliations(ctx, queries, params, deviceID)
	if err != nil {
		return devices.Binding{}, err
	}
	entityBindings, err := applyEntityReconciliations(ctx, queries, params, deviceID, updatedAt, reconciliations)
	if err != nil {
		return devices.Binding{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.Binding{}, fmt.Errorf("commit registration: %w", commitErr)
	}
	return devices.Binding{BindingKey: params.BindingKey, DeviceID: deviceID, Entities: entityBindings}, nil
}

func checkRegistrationRuntime(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID devices.RuntimeID,
) error {
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("validate registration runtime: %w", err)
	}
	return nil
}

func reconcileRegistrationDevice(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	updatedAt string,
) (devices.DeviceID, error) {
	binding, err := queries.GetBinding(ctx, dbsqlc.GetBindingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return createRegistrationDevice(ctx, queries, params, updatedAt)
	}
	if err != nil {
		return "", fmt.Errorf("get binding: %w", err)
	}
	deviceID := devices.DeviceID(binding.DeviceID)
	if availabilityErr := ensureExternalDeviceAvailable(
		ctx, queries, params.AdapterID, params.Device.ExternalID, deviceID,
	); availabilityErr != nil {
		return "", availabilityErr
	}
	if updateErr := queries.UpdateDeviceDescriptor(ctx, dbsqlc.UpdateDeviceDescriptorParams{
		Kind: string(params.Device.Kind), Name: params.Device.Name, UpdatedAt: updatedAt, ID: binding.DeviceID,
	}); updateErr != nil {
		return "", fmt.Errorf("update device descriptor: %w", updateErr)
	}
	if updateErr := queries.UpdateBindingExternalID(ctx, dbsqlc.UpdateBindingExternalIDParams{
		ExternalDeviceID: nullableString(params.Device.ExternalID), UpdatedAt: updatedAt,
		AdapterID: params.AdapterID, BindingKey: params.BindingKey,
	}); updateErr != nil {
		return "", mapRegistrationWriteError("update binding external ID", updateErr)
	}
	return deviceID, nil
}

func createRegistrationDevice(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	updatedAt string,
) (devices.DeviceID, error) {
	deviceID := params.DeviceID
	if err := ensureExternalDeviceAvailable(
		ctx, queries, params.AdapterID, params.Device.ExternalID, deviceID,
	); err != nil {
		return "", err
	}
	if err := queries.CreateDevice(ctx, dbsqlc.CreateDeviceParams{
		ID: string(deviceID), Kind: string(params.Device.Kind), Name: params.Device.Name,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		return "", fmt.Errorf("create device: %w", err)
	}
	if err := queries.CreateBinding(ctx, dbsqlc.CreateBindingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, DeviceID: string(deviceID),
		ExternalDeviceID: nullableString(params.Device.ExternalID), CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		return "", mapRegistrationWriteError("create binding", err)
	}
	return deviceID, nil
}

func prepareEntityReconciliations(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	deviceID devices.DeviceID,
) ([]entityReconciliation, error) {
	reconciliations := make([]entityReconciliation, len(params.Entities))
	for index, entity := range params.Entities {
		reconciliation, err := prepareEntityReconciliation(ctx, queries, params, entity, deviceID)
		if err != nil {
			return nil, err
		}
		reconciliations[index] = reconciliation
	}
	return reconciliations, nil
}

func prepareEntityReconciliation(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	entity devices.RegisterEntityParams,
	deviceID devices.DeviceID,
) (entityReconciliation, error) {
	reconciliation := entityReconciliation{params: entity, entityID: entity.EntityID}
	mapping, err := queries.GetEntityMapping(ctx, dbsqlc.GetEntityMappingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Entity.Key,
	})
	switch {
	case err == nil:
		reconciliation.mapping = mapping
		reconciliation.entityID = devices.EntityID(mapping.EntityID)
		reconciliation.exists = true
		if mapping.DeviceID != string(deviceID) {
			return entityReconciliation{}, errors.New("entity mapping references a different device")
		}
		if devices.EntityTypeID(mapping.TypeID) != entity.Entity.TypeID {
			return entityReconciliation{}, devices.ErrEntityTypeImmutable
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return entityReconciliation{}, fmt.Errorf("get entity mapping: %w", err)
	}

	owner, ownerErr := queries.GetEntityMappingByExternalID(ctx, dbsqlc.GetEntityMappingByExternalIDParams{
		AdapterID: params.AdapterID, ExternalEntityID: entity.Entity.ExternalID,
	})
	if ownerErr == nil && owner.EntityID != string(reconciliation.entityID) {
		return entityReconciliation{}, devices.ErrRegistrationIdentityConflict
	}
	if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
		return entityReconciliation{}, fmt.Errorf("check external entity ID: %w", ownerErr)
	}
	return reconciliation, nil
}

func applyEntityReconciliations(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	deviceID devices.DeviceID,
	updatedAt string,
	reconciliations []entityReconciliation,
) ([]devices.EntityBinding, error) {
	bindings := make([]devices.EntityBinding, len(reconciliations))
	for index, reconciliation := range reconciliations {
		binding, err := applyEntityReconciliation(ctx, queries, params, deviceID, updatedAt, reconciliation)
		if err != nil {
			return nil, err
		}
		bindings[index] = binding
	}
	return bindings, nil
}

func applyEntityReconciliation(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	deviceID devices.DeviceID,
	updatedAt string,
	reconciliation entityReconciliation,
) (devices.EntityBinding, error) {
	entity := reconciliation.params.Entity
	if reconciliation.exists {
		if err := queries.UpdateEntityDescriptor(ctx, dbsqlc.UpdateEntityDescriptorParams{
			Name: entity.Name, SupportJson: string(entity.Support), UpdatedAt: updatedAt,
			ID: reconciliation.mapping.EntityID,
		}); err != nil {
			return devices.EntityBinding{}, fmt.Errorf("update entity descriptor: %w", err)
		}
		if err := queries.UpdateEntityMappingExternalID(ctx, dbsqlc.UpdateEntityMappingExternalIDParams{
			ExternalEntityID: entity.ExternalID, UpdatedAt: updatedAt,
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Key,
		}); err != nil {
			return devices.EntityBinding{}, mapRegistrationWriteError("update entity external ID", err)
		}
	} else {
		if err := createRegistrationEntity(ctx, queries, params, deviceID, updatedAt, reconciliation); err != nil {
			return devices.EntityBinding{}, err
		}
	}
	return devices.EntityBinding{
		Key: entity.Key, EntityID: reconciliation.entityID, Enabled: reconciliationEnabled(reconciliation),
	}, nil
}

func createRegistrationEntity(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.RegisterBindingParams,
	deviceID devices.DeviceID,
	updatedAt string,
	reconciliation entityReconciliation,
) error {
	entity := reconciliation.params.Entity
	if err := queries.CreateEntity(ctx, dbsqlc.CreateEntityParams{
		ID: string(reconciliation.entityID), DeviceID: string(deviceID), Name: entity.Name,
		TypeID: string(entity.TypeID), SupportJson: string(entity.Support),
		Enabled: boolToInt64(reconciliationEnabled(reconciliation)), CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		return fmt.Errorf("create entity: %w", err)
	}
	if err := queries.CreateEntityMapping(ctx, dbsqlc.CreateEntityMappingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Key,
		EntityID: string(reconciliation.entityID), ExternalEntityID: entity.ExternalID,
		CreatedAt: updatedAt, UpdatedAt: updatedAt,
	}); err != nil {
		return mapRegistrationWriteError("create entity mapping", err)
	}
	return createEntityAvailabilityBaseline(
		ctx, queries, params.AdapterID, reconciliation.entityID, updatedAt,
	)
}

func createEntityAvailabilityBaseline(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	entityID devices.EntityID,
	observedAt string,
) error {
	adapter, err := queries.GetAdapterAvailabilityBaseline(
		ctx,
		dbsqlc.GetAdapterAvailabilityBaselineParams{AdapterID: adapterID},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return devices.ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("get Adapter health for Entity availability baseline: %w", err)
	}
	status := string(devices.EntityAvailabilityUnknown)
	source := availabilitySourceAdapterHealth
	reasonCode := adapter.HealthReasonCode
	sourceObservedAt := adapter.HealthSourceObservedAt
	switch devices.AdapterHealthStatus(adapter.HealthStatus) {
	case devices.AdapterHealthHealthy:
		source = healthSourceCore
		reasonCode = nullableText("hearth.awaiting_entity_report")
		sourceObservedAt = sql.NullString{}
	case devices.AdapterHealthUnhealthy:
		status = string(devices.EntityAvailabilityUnavailable)
	case devices.AdapterHealthUnknown:
	default:
		return errors.New("adapter health is incomplete for entity availability baseline")
	}
	err = queries.InsertEntityAvailabilityBaseline(
		ctx,
		dbsqlc.InsertEntityAvailabilityBaselineParams{
			AdapterID: adapterID, EntityID: nullableText(string(entityID)),
			RuntimeID: adapter.ActiveRuntimeID, Status: status, Source: source,
			ReasonCode: reasonCode, SourceObservedAt: sourceObservedAt, ObservedAt: observedAt,
		},
	)
	if err != nil {
		return fmt.Errorf("insert Entity availability baseline: %w", err)
	}
	return nil
}

func reconciliationEnabled(reconciliation entityReconciliation) bool {
	if reconciliation.exists {
		return reconciliation.mapping.Enabled != 0
	}
	if reconciliation.params.Entity.InitiallyEnabled != nil {
		return *reconciliation.params.Entity.InitiallyEnabled
	}
	return true
}

func ensureExternalDeviceAvailable(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	externalID *string,
	deviceID devices.DeviceID,
) error {
	if externalID == nil {
		return nil
	}
	binding, err := queries.GetBindingByExternalDeviceID(ctx, dbsqlc.GetBindingByExternalDeviceIDParams{
		AdapterID: adapterID, ExternalDeviceID: sql.NullString{String: *externalID, Valid: true},
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check external device ID: %w", err)
	}
	if binding.DeviceID != string(deviceID) {
		return devices.ErrRegistrationIdentityConflict
	}
	return nil
}

func mapRegistrationWriteError(action string, err error) error {
	if isUniqueConstraint(err) {
		return fmt.Errorf("%s: %w", action, devices.ErrRegistrationIdentityConflict)
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
