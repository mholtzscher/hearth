package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) CreateCommand(
	ctx context.Context,
	command devices.CommandRecord,
) (devices.CommandRecord, error) {
	if command.Status != devices.CommandStatusRequested || command.AcceptedAt != nil || command.CompletedAt != nil ||
		command.OutcomeObservationID != nil ||
		command.FailureCode != nil {
		return devices.CommandRecord{}, errors.New("new command must be in requested status without terminal fields")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("begin command creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	entity, err := repository.queries.WithTx(tx).GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(command.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.CommandRecord{}, devices.ErrEntityNotFound
	}
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("get entity for command: %w", err)
	}
	command.AdapterID = entity.AdapterID
	if entity.Enabled == 0 {
		completedAt := command.RequestedAt
		failureCode := devices.CommandFailureEntityDisabled
		command.Status = devices.CommandStatusEntityDisabled
		command.CompletedAt = &completedAt
		command.FailureCode = &failureCode
	} else {
		instance, healthErr := repository.queries.WithTx(tx).GetAdapterInstance(
			ctx,
			dbsqlc.GetAdapterInstanceParams{AdapterID: entity.AdapterID},
		)
		if healthErr != nil && !errors.Is(healthErr, sql.ErrNoRows) {
			return devices.CommandRecord{}, fmt.Errorf("get Adapter health for command: %w", healthErr)
		}
		if errors.Is(healthErr, sql.ErrNoRows) || !instance.ActiveRuntimeID.Valid ||
			instance.HealthStatus == string(devices.AdapterHealthUnhealthy) {
			completedAt := command.RequestedAt
			failureCode := devices.CommandFailureAdapterUnhealthy
			command.Status = devices.CommandStatusAdapterUnhealthy
			command.CompletedAt = &completedAt
			command.FailureCode = &failureCode
		} else {
			runtimeID := devices.RuntimeID(instance.ActiveRuntimeID.String)
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
			return devices.CommandRecord{}, fmt.Errorf("create command: %w", devices.ErrCommandIDConflict)
		}
		return devices.CommandRecord{}, fmt.Errorf("create command: %w", createErr)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.CommandRecord{}, fmt.Errorf("commit command creation: %w", commitErr)
	}
	return devices.CopyCommandRecord(command), nil
}

func (repository *DeviceRepository) GetCommand(
	ctx context.Context,
	id devices.CommandID,
) (devices.CommandRecord, error) {
	row, err := repository.queries.GetCommand(ctx, dbsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.CommandRecord{}, devices.ErrCommandNotFound
	}
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("get command: %w", err)
	}
	return commandFromRow(row)
}

func (repository *DeviceRepository) MarkCommandAccepted(
	ctx context.Context,
	id devices.CommandID,
	acceptedAt time.Time,
) error {
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
	return devices.ErrCommandTerminal
}

func (repository *DeviceRepository) CompleteCommand(ctx context.Context, completion devices.CommandCompletion) error {
	if !devices.ValidCommandCompletion(completion) {
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
		return devices.ErrCommandNotFound
	}
	if err != nil {
		return fmt.Errorf("get command for completion: %w", err)
	}
	command, err := commandFromRow(row)
	if err != nil {
		return err
	}
	if command.Status != devices.CommandStatusRequested && command.Status != devices.CommandStatusAccepted {
		if devices.MatchesCommandCompletion(command, completion) {
			return nil
		}
		return devices.ErrCommandTerminal
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
		return devices.ErrCommandTerminal
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return fmt.Errorf("commit command completion: %w", commitErr)
	}
	return nil
}

func (repository *DeviceRepository) InterruptActiveCommands(ctx context.Context, completedAt time.Time) error {
	_, err := repository.queries.
		InterruptActiveCommands(ctx, dbsqlc.InterruptActiveCommandsParams{
			CompletedAt: sql.NullString{String: formatTime(completedAt), Valid: true},
		})
	if err != nil {
		return fmt.Errorf("interrupt active commands: %w", err)
	}
	return nil
}

func commandFromRow(row dbsqlc.Command) (devices.CommandRecord, error) {
	requestedAt, err := parseTime(row.RequestedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command requested_at: %w", err)
	}
	deadlineAt, err := parseTime(row.DeadlineAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command deadline_at: %w", err)
	}
	acceptedAt, err := parseOptionalTime(row.AcceptedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command accepted_at: %w", err)
	}
	completedAt, err := parseOptionalTime(row.CompletedAt)
	if err != nil {
		return devices.CommandRecord{}, fmt.Errorf("parse command completed_at: %w", err)
	}
	command := devices.CommandRecord{
		ID:        devices.CommandID(row.ID),
		EntityID:  devices.EntityID(row.EntityID),
		AdapterID: row.AdapterID,
		OperationName: devices.OperationName(
			row.Operation,
		),
		Parameters:    devices.CommandParameters(json.RawMessage(row.ParametersJson)),
		CorrelationID: devices.CorrelationID(row.CorrelationID),
		Status:        devices.CommandStatus(row.Status),
		RequestedAt:   requestedAt,
		DeadlineAt:    deadlineAt,
		AcceptedAt:    acceptedAt,
		CompletedAt:   completedAt,
	}
	if row.RuntimeID.Valid {
		id := devices.RuntimeID(row.RuntimeID.String)
		command.RuntimeID = &id
	}
	if row.OutcomeObservationID.Valid {
		id := devices.ObservationID(row.OutcomeObservationID.String)
		command.OutcomeObservationID = &id
	}
	if row.FailureCode.Valid {
		code := devices.CommandFailureCode(row.FailureCode.String)
		command.FailureCode = &code
	}
	return command, nil
}
