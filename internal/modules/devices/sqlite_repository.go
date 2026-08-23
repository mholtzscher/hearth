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

	mapping, err := queries.GetEntityMapping(ctx, registrationsqlc.GetEntityMappingParams{
		AdapterID: params.AdapterID, BindingKey: params.BindingKey, EntityKey: params.Entity.Key,
	})
	var entityID EntityID
	switch {
	case err == nil:
		entityID = EntityID(mapping.EntityID)
		if mapping.DeviceID != string(deviceID) {
			return Binding{}, fmt.Errorf("entity mapping references a different device")
		}
		if EntityTypeID(mapping.TypeID) != params.Entity.TypeID {
			return Binding{}, errImmutableTypeChange
		}
		if err := ensureExternalEntityAvailable(ctx, queries, params.AdapterID, params.Entity.ExternalID, entityID); err != nil {
			return Binding{}, err
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
		if err := ensureExternalEntityAvailable(ctx, queries, params.AdapterID, params.Entity.ExternalID, entityID); err != nil {
			return Binding{}, err
		}
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

func ensureExternalEntityAvailable(ctx context.Context, queries *registrationsqlc.Queries, adapterID, externalID string, entityID EntityID) error {
	mapping, err := queries.GetEntityMappingByExternalID(ctx, registrationsqlc.GetEntityMappingByExternalIDParams{
		AdapterID: adapterID, ExternalEntityID: externalID,
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check external entity ID: %w", err)
	}
	if mapping.EntityID != string(entityID) {
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

func (repository *SQLiteRepository) CreateCommand(ctx context.Context, command CommandRecord) error {
	if command.Status != CommandStatusRequested || command.AcceptedAt != nil || command.CompletedAt != nil || command.OutcomeObservationID != nil || command.FailureCode != nil {
		return errors.New("new command must be in requested status without terminal fields")
	}
	queries := commandsqlc.New(repository.database)
	if err := queries.CreateCommand(ctx, commandsqlc.CreateCommandParams{
		ID: string(command.ID), EntityID: string(command.EntityID), AdapterID: command.AdapterID,
		Operation: string(command.OperationName), ParametersJson: string(command.Parameters),
		CorrelationID: string(command.CorrelationID), Status: string(command.Status),
		RequestedAt: formatTime(command.RequestedAt), DeadlineAt: formatTime(command.DeadlineAt),
	}); err != nil {
		return fmt.Errorf("create command: %w", err)
	}
	return nil
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

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
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
