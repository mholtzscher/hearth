package devices

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

var (
	_ RegistrationRepository = (*SQLiteRepository)(nil)
	_ OwnedMappingRepository = (*SQLiteRepository)(nil)
	_ RuntimeRepository      = (*SQLiteRepository)(nil)
	_ AdapterRepository      = (*SQLiteRepository)(nil)
	_ AvailabilityRepository = (*SQLiteRepository)(nil)
	_ ReadRepository         = (*SQLiteRepository)(nil)
	_ EnablementRepository   = (*SQLiteRepository)(nil)
	_ CommandLedger          = (*SQLiteRepository)(nil)
	_ ObservationRepository  = (*SQLiteRepository)(nil)
	_ DeviceEventRepository  = (*SQLiteRepository)(nil)
)

type SQLiteRepository struct {
	database *sql.DB
	queries  *dbsqlc.Queries
	catalog  *TypeCatalog
}

type entityReconciliation struct {
	params   RegisterEntityParams
	mapping  dbsqlc.GetEntityMappingRow
	entityID EntityID
	exists   bool
}

func NewSQLiteRepository(database *sql.DB, catalog *TypeCatalog) *SQLiteRepository {
	return &SQLiteRepository{database: database, queries: dbsqlc.New(database), catalog: catalog}
}

// SQLiteStores exposes one SQLite repository through each Service capability.
func SQLiteStores(repository *SQLiteRepository) Stores {
	return Stores{
		Registration:  repository,
		OwnedMappings: repository,
		Runtimes:      repository,
		Adapters:      repository,
		Availability:  repository,
		Reads:         repository,
		Enablement:    repository,
		Commands:      repository,
		Observations:  repository,
		DeviceEvents:  repository,
	}
}

func (repository *SQLiteRepository) ListOwnedMappings(
	ctx context.Context,
	params ListOwnedMappingsParams,
) (Page[OwnedMapping], error) {
	afterBindingKey := ""
	afterEntityKey := ""
	hasAfter := int64(0)
	if params.After != nil {
		afterBindingKey = params.After.BindingKey
		afterEntityKey = params.After.EntityKey
		hasAfter = 1
	}
	rows, err := repository.queries.ListOwnedMappings(ctx, dbsqlc.ListOwnedMappingsParams{
		AdapterID:       params.AdapterID,
		HasAfter:        hasAfter,
		AfterBindingKey: afterBindingKey,
		AfterEntityKey:  afterEntityKey,
		ResultLimit:     int64(params.Limit + 1),
	})
	if err != nil {
		return Page[OwnedMapping]{}, fmt.Errorf("list owned mappings: %w", err)
	}
	items := make([]OwnedMapping, len(rows))
	for index, row := range rows {
		items[index] = OwnedMapping{
			BindingKey: row.BindingKey,
			DeviceID:   DeviceID(row.DeviceID),
			EntityKey:  row.EntityKey,
			EntityID:   EntityID(row.EntityID),
		}
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *SQLiteRepository) RegisterBinding(
	ctx context.Context,
	params RegisterBindingParams,
) (Binding, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return Binding{}, fmt.Errorf("begin registration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	if runtimeErr := checkRegistrationRuntime(ctx, queries, params.AdapterID, params.RuntimeID); runtimeErr != nil {
		return Binding{}, runtimeErr
	}
	updatedAt := formatTime(params.UpdatedAt)

	deviceID, err := reconcileRegistrationDevice(ctx, queries, params, updatedAt)
	if err != nil {
		return Binding{}, err
	}
	reconciliations, err := prepareEntityReconciliations(ctx, queries, params, deviceID)
	if err != nil {
		return Binding{}, err
	}
	entityBindings, err := applyEntityReconciliations(ctx, queries, params, deviceID, updatedAt, reconciliations)
	if err != nil {
		return Binding{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return Binding{}, fmt.Errorf("commit registration: %w", commitErr)
	}
	return Binding{BindingKey: params.BindingKey, DeviceID: deviceID, Entities: entityBindings}, nil
}

func checkRegistrationRuntime(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID RuntimeID,
) error {
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("validate registration runtime: %w", err)
	}
	return nil
}

func reconcileRegistrationDevice(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params RegisterBindingParams,
	updatedAt string,
) (DeviceID, error) {
	binding, err := queries.GetBinding(ctx, dbsqlc.GetBindingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return createRegistrationDevice(ctx, queries, params, updatedAt)
	}
	if err != nil {
		return "", fmt.Errorf("get binding: %w", err)
	}
	deviceID := DeviceID(binding.DeviceID)
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
	params RegisterBindingParams,
	updatedAt string,
) (DeviceID, error) {
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
	params RegisterBindingParams,
	deviceID DeviceID,
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
	params RegisterBindingParams,
	entity RegisterEntityParams,
	deviceID DeviceID,
) (entityReconciliation, error) {
	reconciliation := entityReconciliation{params: entity, entityID: entity.EntityID}
	mapping, err := queries.GetEntityMapping(ctx, dbsqlc.GetEntityMappingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Entity.Key,
	})
	switch {
	case err == nil:
		reconciliation.mapping = mapping
		reconciliation.entityID = EntityID(mapping.EntityID)
		reconciliation.exists = true
		if mapping.DeviceID != string(deviceID) {
			return entityReconciliation{}, errors.New("entity mapping references a different device")
		}
		if EntityTypeID(mapping.TypeID) != entity.Entity.TypeID {
			return entityReconciliation{}, errImmutableTypeChange
		}
	case errors.Is(err, sql.ErrNoRows):
	default:
		return entityReconciliation{}, fmt.Errorf("get entity mapping: %w", err)
	}

	owner, ownerErr := queries.GetEntityMappingByExternalID(ctx, dbsqlc.GetEntityMappingByExternalIDParams{
		AdapterID: params.AdapterID, ExternalEntityID: entity.Entity.ExternalID,
	})
	if ownerErr == nil && owner.EntityID != string(reconciliation.entityID) {
		return entityReconciliation{}, errIdentityConflict
	}
	if ownerErr != nil && !errors.Is(ownerErr, sql.ErrNoRows) {
		return entityReconciliation{}, fmt.Errorf("check external entity ID: %w", ownerErr)
	}
	return reconciliation, nil
}

func applyEntityReconciliations(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params RegisterBindingParams,
	deviceID DeviceID,
	updatedAt string,
	reconciliations []entityReconciliation,
) ([]EntityBinding, error) {
	bindings := make([]EntityBinding, len(reconciliations))
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
	params RegisterBindingParams,
	deviceID DeviceID,
	updatedAt string,
	reconciliation entityReconciliation,
) (EntityBinding, error) {
	entity := reconciliation.params.Entity
	if reconciliation.exists {
		if err := queries.UpdateEntityDescriptor(ctx, dbsqlc.UpdateEntityDescriptorParams{
			Name: entity.Name, SupportJson: string(entity.Support), UpdatedAt: updatedAt,
			ID: reconciliation.mapping.EntityID,
		}); err != nil {
			return EntityBinding{}, fmt.Errorf("update entity descriptor: %w", err)
		}
		if err := queries.UpdateEntityMappingExternalID(ctx, dbsqlc.UpdateEntityMappingExternalIDParams{
			ExternalEntityID: entity.ExternalID, UpdatedAt: updatedAt,
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Key,
		}); err != nil {
			return EntityBinding{}, mapRegistrationWriteError("update entity external ID", err)
		}
	} else {
		if err := createRegistrationEntity(ctx, queries, params, deviceID, updatedAt, reconciliation); err != nil {
			return EntityBinding{}, err
		}
	}
	return EntityBinding{
		Key: entity.Key, EntityID: reconciliation.entityID, Enabled: reconciliationEnabled(reconciliation),
	}, nil
}

func createRegistrationEntity(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params RegisterBindingParams,
	deviceID DeviceID,
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
	entityID EntityID,
	observedAt string,
) error {
	adapter, err := queries.GetAdapterAvailabilityBaseline(
		ctx,
		dbsqlc.GetAdapterAvailabilityBaselineParams{AdapterID: adapterID},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("get Adapter health for Entity availability baseline: %w", err)
	}
	status := string(EntityAvailabilityUnknown)
	source := availabilitySourceAdapterHealth
	reasonCode := adapter.HealthReasonCode
	sourceObservedAt := adapter.HealthSourceObservedAt
	switch AdapterHealthStatus(adapter.HealthStatus) {
	case AdapterHealthHealthy:
		source = healthSourceCore
		reasonCode = nullableText("hearth.awaiting_entity_report")
		sourceObservedAt = sql.NullString{}
	case AdapterHealthUnhealthy:
		status = string(EntityAvailabilityUnavailable)
	case AdapterHealthUnknown:
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
	deviceID DeviceID,
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
		return errIdentityConflict
	}
	return nil
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

func (repository *SQLiteRepository) SetEntityEnabled(
	ctx context.Context,
	params SetEntityEnabledParams,
) (EntityWithState, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return EntityWithState{}, fmt.Errorf("begin entity enablement update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	if params.RequiredRuntime != nil {
		if params.RequiredOwner == nil {
			return EntityWithState{}, errors.New("runtime-scoped enablement requires an Adapter owner")
		}
		_, runtimeErr := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
			AdapterID: *params.RequiredOwner, RuntimeID: string(*params.RequiredRuntime),
		})
		if errors.Is(runtimeErr, sql.ErrNoRows) {
			return EntityWithState{}, ErrRuntimeFenced
		}
		if runtimeErr != nil {
			return EntityWithState{}, fmt.Errorf("validate entity enablement runtime: %w", runtimeErr)
		}
	}
	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return EntityWithState{}, ErrEntityNotFound
	}
	if err != nil {
		return EntityWithState{}, fmt.Errorf("get entity for enablement update: %w", err)
	}
	if params.RequiredOwner != nil && row.AdapterID != *params.RequiredOwner {
		return EntityWithState{}, ErrEntityWrongAdapter
	}
	if row.Enabled != boolToInt64(params.Enabled) {
		if _, updateErr := queries.UpdateEntityEnablement(ctx, dbsqlc.UpdateEntityEnablementParams{
			Enabled: boolToInt64(params.Enabled), UpdatedAt: formatTime(params.UpdatedAt),
			ID: string(params.EntityID),
		}); updateErr != nil {
			return EntityWithState{}, fmt.Errorf("update entity enablement: %w", updateErr)
		}
		row, err = queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.EntityID)})
		if err != nil {
			return EntityWithState{}, fmt.Errorf("get updated entity: %w", err)
		}
	}
	view, err := entityWithStateFromRow(row)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("map updated entity: %w", err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return EntityWithState{}, fmt.Errorf("commit entity enablement update: %w", commitErr)
	}
	return copyEntityWithState(view), nil
}

func (repository *SQLiteRepository) CreateCommand(ctx context.Context, command CommandRecord) (CommandRecord, error) {
	if command.Status != CommandStatusRequested || command.AcceptedAt != nil || command.CompletedAt != nil ||
		command.OutcomeObservationID != nil ||
		command.FailureCode != nil {
		return CommandRecord{}, errors.New("new command must be in requested status without terminal fields")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return CommandRecord{}, fmt.Errorf("begin command creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	entity, err := repository.queries.WithTx(tx).GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(command.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRecord{}, ErrEntityNotFound
	}
	if err != nil {
		return CommandRecord{}, fmt.Errorf("get entity for command: %w", err)
	}
	command.AdapterID = entity.AdapterID
	if entity.Enabled == 0 {
		completedAt := command.RequestedAt
		failureCode := CommandFailureEntityDisabled
		command.Status = CommandStatusEntityDisabled
		command.CompletedAt = &completedAt
		command.FailureCode = &failureCode
	} else {
		instance, healthErr := repository.queries.WithTx(tx).GetAdapterInstance(
			ctx,
			dbsqlc.GetAdapterInstanceParams{AdapterID: entity.AdapterID},
		)
		if healthErr != nil && !errors.Is(healthErr, sql.ErrNoRows) {
			return CommandRecord{}, fmt.Errorf("get Adapter health for command: %w", healthErr)
		}
		if errors.Is(healthErr, sql.ErrNoRows) || !instance.ActiveRuntimeID.Valid ||
			instance.HealthStatus == string(AdapterHealthUnhealthy) {
			completedAt := command.RequestedAt
			failureCode := CommandFailureAdapterUnhealthy
			command.Status = CommandStatusAdapterUnhealthy
			command.CompletedAt = &completedAt
			command.FailureCode = &failureCode
		} else {
			runtimeID := RuntimeID(instance.ActiveRuntimeID.String)
			command.RuntimeID = &runtimeID
		}
	}
	queries := repository.queries.WithTx(tx)
	if createErr := queries.CreateCommand(ctx, dbsqlc.CreateCommandParams{
		ID: string(command.ID), EntityID: string(command.EntityID), AdapterID: command.AdapterID,
		RuntimeID: nullableRuntimeID(command.RuntimeID), Operation: string(command.OperationName),
		ParametersJson: string(command.Parameters),
		CorrelationID:  string(command.CorrelationID), Status: string(command.Status),
		RequestedAt: formatSortableTime(command.RequestedAt), DeadlineAt: formatTime(command.DeadlineAt),
		CompletedAt: nullableTime(command.CompletedAt), FailureCode: nullableCommandFailure(command.FailureCode),
	}); createErr != nil {
		// The commands table has only one primary key: id. Do not map UNIQUE
		// violations (such as outcome observation identity) or other constraints.
		type sqliteError interface{ Code() int }
		var databaseError sqliteError
		if errors.As(createErr, &databaseError) && databaseError.Code() == 1555 {
			return CommandRecord{}, fmt.Errorf("create command: %w", ErrCommandIDConflict)
		}
		return CommandRecord{}, fmt.Errorf("create command: %w", createErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return CommandRecord{}, fmt.Errorf("commit command creation: %w", commitErr)
	}
	return copyCommandRecord(command), nil
}

func (repository *SQLiteRepository) GetCommand(ctx context.Context, id CommandID) (CommandRecord, error) {
	row, err := repository.queries.GetCommand(ctx, dbsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRecord{}, ErrCommandNotFound
	}
	if err != nil {
		return CommandRecord{}, fmt.Errorf("get command: %w", err)
	}
	return commandFromRow(row)
}

func (repository *SQLiteRepository) MarkCommandAccepted(ctx context.Context, id CommandID, acceptedAt time.Time) error {
	queries := repository.queries
	rows, err := queries.MarkCommandAccepted(ctx, dbsqlc.MarkCommandAcceptedParams{
		AcceptedAt: sql.NullString{String: formatTime(acceptedAt), Valid: true}, ID: string(id),
	})
	if err != nil {
		return fmt.Errorf("mark command accepted: %w", err)
	}
	if rows > 0 {
		return nil
	}
	if _, lookupErr := repository.GetCommand(ctx, id); lookupErr != nil {
		return lookupErr
	}
	return ErrCommandTerminal
}

func (repository *SQLiteRepository) CompleteCommand(ctx context.Context, completion CommandCompletion) error {
	if !validCommandCompletion(completion) {
		return errors.New("invalid command completion")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin command completion: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	row, err := queries.GetCommand(ctx, dbsqlc.GetCommandParams{ID: string(completion.ID)})
	if errors.Is(err, sql.ErrNoRows) {
		return ErrCommandNotFound
	}
	if err != nil {
		return fmt.Errorf("get command for completion: %w", err)
	}
	command, err := commandFromRow(row)
	if err != nil {
		return err
	}
	if command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted {
		if sameCompletion(command, completion) {
			return nil
		}
		return ErrCommandTerminal
	}
	rows, err := queries.CompleteCommand(ctx, dbsqlc.CompleteCommandParams{
		Status:      string(completion.Status),
		CompletedAt: sql.NullString{String: formatTime(completion.CompletedAt), Valid: true},
		FailureCode: nullableCompletionFailure(completion.FailureCode),
		ID:          string(completion.ID),
	})
	if err != nil {
		return fmt.Errorf("complete command: %w", err)
	}
	if rows != 1 {
		return ErrCommandTerminal
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit command completion: %w", commitErr)
	}
	return nil
}

func (repository *SQLiteRepository) InterruptActiveCommands(ctx context.Context, completedAt time.Time) error {
	_, err := repository.queries.
		InterruptActiveCommands(ctx, dbsqlc.InterruptActiveCommandsParams{
			CompletedAt: sql.NullString{String: formatTime(completedAt), Valid: true},
		})
	if err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	return nil
}

func commandFromRow(row dbsqlc.Command) (CommandRecord, error) {
	requestedAt, err := parseTime(row.RequestedAt)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("parse command requested_at: %w", err)
	}
	deadlineAt, err := parseTime(row.DeadlineAt)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("parse command deadline_at: %w", err)
	}
	acceptedAt, err := parseOptionalTime(row.AcceptedAt)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("parse command accepted_at: %w", err)
	}
	completedAt, err := parseOptionalTime(row.CompletedAt)
	if err != nil {
		return CommandRecord{}, fmt.Errorf("parse command completed_at: %w", err)
	}
	command := CommandRecord{
		ID: CommandID(row.ID), EntityID: EntityID(row.EntityID), AdapterID: row.AdapterID,
		OperationName: OperationName(row.Operation), Parameters: CommandParameters(json.RawMessage(row.ParametersJson)),
		CorrelationID: CorrelationID(row.CorrelationID), Status: CommandStatus(row.Status),
		RequestedAt: requestedAt, DeadlineAt: deadlineAt, AcceptedAt: acceptedAt, CompletedAt: completedAt,
	}
	if row.RuntimeID.Valid {
		id := RuntimeID(row.RuntimeID.String)
		command.RuntimeID = &id
	}
	if row.OutcomeObservationID.Valid {
		id := ObservationID(row.OutcomeObservationID.String)
		command.OutcomeObservationID = &id
	}
	if row.FailureCode.Valid {
		code := CommandFailureCode(row.FailureCode.String)
		command.FailureCode = &code
	}
	return command, nil
}

func validCommandCompletion(completion CommandCompletion) bool {
	if completion.Status == CommandStatusDispatched {
		return !completion.CompletedAt.IsZero() && completion.FailureCode == ""
	}
	expected := map[CommandStatus]CommandFailureCode{ //nolint:exhaustive // Only terminal failure statuses have failure codes.
		CommandStatusRejected:          CommandFailureUpstreamRejected,
		CommandStatusAdapterUnhealthy:  CommandFailureAdapterUnhealthy,
		CommandStatusEntityUnavailable: CommandFailureEntityUnavailable,
		CommandStatusOutcomeTimeout:    CommandFailureOutcomeTimeout,
		CommandStatusInternalFailure:   CommandFailureInternalError,
	}
	return !completion.CompletedAt.IsZero() && expected[completion.Status] == completion.FailureCode &&
		completion.FailureCode != ""
}

func sameCompletion(command CommandRecord, completion CommandCompletion) bool {
	return command.Status == completion.Status && commandFailureMatches(command, completion) &&
		command.CompletedAt != nil &&
		command.CompletedAt.Equal(completion.CompletedAt)
}

// commandFailureMatches treats dispatched (and any outcome with empty
// FailureCode) as matching only when the record has no failure code.
func commandFailureMatches(command CommandRecord, completion CommandCompletion) bool {
	if completion.FailureCode == "" {
		return command.FailureCode == nil
	}
	return command.FailureCode != nil && *command.FailureCode == completion.FailureCode
}

// nullableCompletionFailure encodes an empty CommandCompletion.FailureCode
// as SQL NULL for successful terminal outcomes such as dispatched. The
// existing nullableCommandFailure stays unchanged for command-record
// creation from *CommandFailureCode.
func nullableCompletionFailure(value CommandFailureCode) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func nullableRuntimeID(value *RuntimeID) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableCommandFailure(value *CommandFailureCode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatSortableTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil //nolint:nilnil // SQL NULL is represented by a nil optional time.
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
