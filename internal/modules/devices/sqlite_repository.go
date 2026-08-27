package devices

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
	registrationsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/registration"
	statesqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/state"
)

var (
	_ Repository    = (*SQLiteRepository)(nil)
	_ CommandLedger = (*SQLiteRepository)(nil)
)

type SQLiteRepository struct {
	database *sql.DB
	catalog  *TypeCatalog
}

func NewSQLiteRepository(database *sql.DB, catalog *TypeCatalog) *SQLiteRepository {
	return &SQLiteRepository{database: database, catalog: catalog}
}

func (repository *SQLiteRepository) RegisterBinding(ctx context.Context, params RegisterBindingParams) (Binding, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
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
		if err := ensureExternalDeviceAvailable(ctx, queries, params.AdapterID, params.Device.ExternalID, deviceID); err != nil {
			return Binding{}, err
		}
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
		if err := ensureExternalDeviceAvailable(ctx, queries, params.AdapterID, params.Device.ExternalID, deviceID); err != nil {
			return Binding{}, err
		}
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

	type entityReconciliation struct {
		params   RegisterEntityParams
		mapping  registrationsqlc.GetEntityMappingRow
		entityID EntityID
		exists   bool
	}
	reconciliations := make([]entityReconciliation, len(params.Entities))
	for index, entity := range params.Entities {
		reconciliation := entityReconciliation{params: entity, entityID: entity.EntityID}
		mapping, err := queries.GetEntityMapping(ctx, registrationsqlc.GetEntityMappingParams{
			AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Entity.Key,
		})
		switch {
		case err == nil:
			reconciliation.mapping = mapping
			reconciliation.entityID = EntityID(mapping.EntityID)
			reconciliation.exists = true
			if mapping.DeviceID != string(deviceID) {
				return Binding{}, fmt.Errorf("entity mapping references a different device")
			}
			if EntityTypeID(mapping.TypeID) != entity.Entity.TypeID {
				return Binding{}, errImmutableTypeChange
			}
		case errors.Is(err, sql.ErrNoRows):
		default:
			return Binding{}, fmt.Errorf("get entity mapping: %w", err)
		}

		owner, err := queries.GetEntityMappingByExternalID(ctx, registrationsqlc.GetEntityMappingByExternalIDParams{
			AdapterID: params.AdapterID, ExternalEntityID: entity.Entity.ExternalID,
		})
		if err == nil && owner.EntityID != string(reconciliation.entityID) {
			return Binding{}, errIdentityConflict
		}
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return Binding{}, fmt.Errorf("check external entity ID: %w", err)
		}
		reconciliations[index] = reconciliation
	}

	entityBindings := make([]EntityBinding, len(reconciliations))
	for index, reconciliation := range reconciliations {
		entity := reconciliation.params.Entity
		if reconciliation.exists {
			if err := queries.UpdateEntityDescriptor(ctx, registrationsqlc.UpdateEntityDescriptorParams{
				Name: entity.Name, SupportJson: string(entity.Support), UpdatedAt: updatedAt, ID: reconciliation.mapping.EntityID,
			}); err != nil {
				return Binding{}, fmt.Errorf("update entity descriptor: %w", err)
			}
			if err := queries.UpdateEntityMappingExternalID(ctx, registrationsqlc.UpdateEntityMappingExternalIDParams{
				ExternalEntityID: entity.ExternalID, UpdatedAt: updatedAt,
				AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Key,
			}); err != nil {
				return Binding{}, mapRegistrationWriteError("update entity external ID", err)
			}
		} else {
			initiallyEnabled := true
			if entity.InitiallyEnabled != nil {
				initiallyEnabled = *entity.InitiallyEnabled
			}
			if err := queries.CreateEntity(ctx, registrationsqlc.CreateEntityParams{
				ID: string(reconciliation.entityID), DeviceID: string(deviceID), Name: entity.Name,
				TypeID: string(entity.TypeID), SupportJson: string(entity.Support), Enabled: boolToInt64(initiallyEnabled),
				CreatedAt: updatedAt, UpdatedAt: updatedAt,
			}); err != nil {
				return Binding{}, fmt.Errorf("create entity: %w", err)
			}
			if err := queries.CreateEntityMapping(ctx, registrationsqlc.CreateEntityMappingParams{
				AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: entity.Key,
				EntityID: string(reconciliation.entityID), ExternalEntityID: entity.ExternalID,
				CreatedAt: updatedAt, UpdatedAt: updatedAt,
			}); err != nil {
				return Binding{}, mapRegistrationWriteError("create entity mapping", err)
			}
		}
		enabled := true
		if reconciliation.exists {
			enabled = reconciliation.mapping.Enabled != 0
		} else if entity.InitiallyEnabled != nil {
			enabled = *entity.InitiallyEnabled
		}
		entityBindings[index] = EntityBinding{Key: entity.Key, EntityID: reconciliation.entityID, Enabled: enabled}
	}

	if err := tx.Commit(); err != nil {
		return Binding{}, fmt.Errorf("commit registration: %w", err)
	}
	return Binding{
		BindingKey: params.BindingKey,
		DeviceID:   deviceID,
		Entities:   entityBindings,
	}, nil
}

func ensureExternalDeviceAvailable(ctx context.Context, queries *registrationsqlc.Queries, adapterID string, externalID *string, deviceID DeviceID) error {
	if externalID == nil {
		return nil
	}
	binding, err := queries.GetBindingByExternalDeviceID(ctx, registrationsqlc.GetBindingByExternalDeviceIDParams{
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

func (repository *SQLiteRepository) SetEntityEnabled(ctx context.Context, params SetEntityEnabledParams) (EntityWithState, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return EntityWithState{}, fmt.Errorf("begin entity enablement update: %w", err)
	}
	defer tx.Rollback()
	queries := statesqlc.New(tx)
	row, err := queries.GetEntity(ctx, statesqlc.GetEntityParams{ID: string(params.EntityID)})
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
		if _, err := queries.UpdateEntityEnablement(ctx, statesqlc.UpdateEntityEnablementParams{
			Enabled: boolToInt64(params.Enabled), UpdatedAt: formatTime(params.UpdatedAt),
			ID: string(params.EntityID),
		}); err != nil {
			return EntityWithState{}, fmt.Errorf("update entity enablement: %w", err)
		}
		row, err = queries.GetEntity(ctx, statesqlc.GetEntityParams{ID: string(params.EntityID)})
		if err != nil {
			return EntityWithState{}, fmt.Errorf("get updated entity: %w", err)
		}
	}
	view, err := entityWithStateFromValues(
		row.ID, row.DeviceID, row.AdapterID, row.Name, row.TypeID, row.SupportJson, row.Enabled,
		row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
		row.ObservedAt, row.ReceiveOrder,
	)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("map updated entity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return EntityWithState{}, fmt.Errorf("commit entity enablement update: %w", err)
	}
	return copyEntityWithState(view), nil
}

func (repository *SQLiteRepository) CreateCommand(ctx context.Context, command CommandRecord) (CommandRecord, error) {
	if command.Status != CommandStatusRequested || command.AcceptedAt != nil || command.CompletedAt != nil || command.OutcomeObservationID != nil || command.FailureCode != nil {
		return CommandRecord{}, errors.New("new command must be in requested status without terminal fields")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return CommandRecord{}, fmt.Errorf("begin command creation: %w", err)
	}
	defer tx.Rollback()
	entity, err := statesqlc.New(tx).GetEntity(ctx, statesqlc.GetEntityParams{ID: string(command.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRecord{}, ErrEntityNotFound
	}
	if err != nil {
		return CommandRecord{}, fmt.Errorf("get entity for command: %w", err)
	}
	if entity.Enabled == 0 {
		completedAt := command.RequestedAt
		failureCode := CommandFailureEntityDisabled
		command.Status = CommandStatusEntityDisabled
		command.CompletedAt = &completedAt
		command.FailureCode = &failureCode
	}
	queries := commandsqlc.New(tx)
	if err := queries.CreateCommand(ctx, commandsqlc.CreateCommandParams{
		ID: string(command.ID), EntityID: string(command.EntityID), AdapterID: command.AdapterID,
		Operation: string(command.OperationName), ParametersJson: string(command.Parameters),
		CorrelationID: string(command.CorrelationID), Status: string(command.Status),
		RequestedAt: formatSortableTime(command.RequestedAt), DeadlineAt: formatTime(command.DeadlineAt),
		CompletedAt: nullableTime(command.CompletedAt), FailureCode: nullableCommandFailure(command.FailureCode),
	}); err != nil {
		return CommandRecord{}, fmt.Errorf("create command: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return CommandRecord{}, fmt.Errorf("commit command creation: %w", err)
	}
	return copyCommandRecord(command), nil
}

func (repository *SQLiteRepository) GetCommand(ctx context.Context, id CommandID) (CommandRecord, error) {
	row, err := commandsqlc.New(repository.database).GetCommand(ctx, commandsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return CommandRecord{}, ErrCommandNotFound
	}
	if err != nil {
		return CommandRecord{}, fmt.Errorf("get command: %w", err)
	}
	return commandFromRow(row)
}

func (repository *SQLiteRepository) MarkCommandAccepted(ctx context.Context, id CommandID, acceptedAt time.Time) error {
	queries := commandsqlc.New(repository.database)
	rows, err := queries.MarkCommandAccepted(ctx, commandsqlc.MarkCommandAcceptedParams{
		AcceptedAt: sql.NullString{String: formatTime(acceptedAt), Valid: true}, ID: string(id),
	})
	if err != nil {
		return fmt.Errorf("mark command accepted: %w", err)
	}
	if rows > 0 {
		return nil
	}
	if _, err := repository.GetCommand(ctx, id); err != nil {
		return err
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
	defer tx.Rollback()
	queries := commandsqlc.New(tx)
	row, err := queries.GetCommand(ctx, commandsqlc.GetCommandParams{ID: string(completion.ID)})
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
	rows, err := queries.CompleteCommand(ctx, commandsqlc.CompleteCommandParams{
		Status:      string(completion.Status),
		CompletedAt: sql.NullString{String: formatTime(completion.CompletedAt), Valid: true},
		FailureCode: sql.NullString{String: string(completion.FailureCode), Valid: true},
		ID:          string(completion.ID),
	})
	if err != nil {
		return fmt.Errorf("complete command: %w", err)
	}
	if rows != 1 {
		return ErrCommandTerminal
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit command completion: %w", err)
	}
	return nil
}

func (repository *SQLiteRepository) InterruptActiveCommands(ctx context.Context, completedAt time.Time) error {
	_, err := commandsqlc.New(repository.database).InterruptActiveCommands(ctx, commandsqlc.InterruptActiveCommandsParams{
		CompletedAt: sql.NullString{String: formatTime(completedAt), Valid: true},
	})
	if err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	return nil
}

func commandFromRow(row commandsqlc.Command) (CommandRecord, error) {
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
	expected := map[CommandStatus]CommandFailureCode{
		CommandStatusRejected:           CommandFailureUpstreamRejected,
		CommandStatusAdapterUnavailable: CommandFailureAdapterUnavailable,
		CommandStatusOutcomeTimeout:     CommandFailureOutcomeTimeout,
		CommandStatusInternalFailure:    CommandFailureInternalError,
	}
	return !completion.CompletedAt.IsZero() && expected[completion.Status] == completion.FailureCode && completion.FailureCode != ""
}

func sameCompletion(command CommandRecord, completion CommandCompletion) bool {
	return command.Status == completion.Status && command.FailureCode != nil &&
		*command.FailureCode == completion.FailureCode && command.CompletedAt != nil &&
		command.CompletedAt.Equal(completion.CompletedAt)
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
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
		return nil, nil
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
