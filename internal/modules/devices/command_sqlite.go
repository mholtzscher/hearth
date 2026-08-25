package devices

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
)

type commandStatus string

const (
	commandStatusRequested          commandStatus = "requested"
	commandStatusAccepted           commandStatus = "accepted"
	commandStatusSatisfied          commandStatus = "satisfied"
	commandStatusRejected           commandStatus = "rejected"
	commandStatusAdapterUnavailable commandStatus = "adapter_unavailable"
	commandStatusOutcomeTimeout     commandStatus = "outcome_timeout"
	commandStatusInternalFailure    commandStatus = "internal_failure"
	commandStatusInterrupted        commandStatus = "interrupted"
)

type commandFailureCode string

const (
	commandFailureAdapterUnavailable commandFailureCode = "adapter_unavailable"
	commandFailureUpstreamRejected   commandFailureCode = "upstream_rejected"
	commandFailureOutcomeTimeout     commandFailureCode = "outcome_timeout"
	commandFailureInternalError      commandFailureCode = "internal_error"
	commandFailureCoreRestarted      commandFailureCode = "core_restarted"
)

type commandRecord struct {
	id                   CommandID
	entityID             EntityID
	adapterID            string
	operationName        OperationName
	parameters           CommandParameters
	correlationID        CorrelationID
	status               commandStatus
	requestedAt          time.Time
	deadlineAt           time.Time
	acceptedAt           *time.Time
	completedAt          *time.Time
	outcomeObservationID *ObservationID
	failureCode          *commandFailureCode
}

type commandCompletion struct {
	id          CommandID
	status      commandStatus
	completedAt time.Time
	failureCode commandFailureCode
}

func (service *Service) createCommand(ctx context.Context, command commandRecord) error {
	if err := commandsqlc.New(service.database).CreateCommand(ctx, commandsqlc.CreateCommandParams{
		ID: string(command.id), EntityID: string(command.entityID), AdapterID: command.adapterID,
		Operation: string(command.operationName), ParametersJson: string(command.parameters),
		CorrelationID: string(command.correlationID), Status: string(command.status),
		RequestedAt: formatTime(command.requestedAt), DeadlineAt: formatTime(command.deadlineAt),
	}); err != nil {
		return fmt.Errorf("create command: %w", err)
	}
	return nil
}

func getCommand(ctx context.Context, database sqliteDBTX, id CommandID) (commandRecord, error) {
	row, err := commandsqlc.New(database).GetCommand(ctx, commandsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return commandRecord{}, errCommandNotFound
	}
	if err != nil {
		return commandRecord{}, fmt.Errorf("get command: %w", err)
	}
	return commandFromRow(row)
}

func (service *Service) markCommandAccepted(ctx context.Context, id CommandID, acceptedAt time.Time) error {
	rows, err := commandsqlc.New(service.database).MarkCommandAccepted(ctx, commandsqlc.MarkCommandAcceptedParams{
		AcceptedAt: sql.NullString{String: formatTime(acceptedAt), Valid: true}, ID: string(id),
	})
	if err != nil {
		return fmt.Errorf("mark command accepted: %w", err)
	}
	if rows > 0 {
		return nil
	}
	if _, err := getCommand(ctx, service.database, id); err != nil {
		return err
	}
	return errCommandTerminal
}

func (service *Service) completeCommand(ctx context.Context, completion commandCompletion) error {
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return fmt.Errorf("begin command completion: %w", err)
	}
	defer tx.Rollback()
	command, err := getCommand(ctx, tx, completion.id)
	if err != nil {
		return err
	}
	if command.status != commandStatusRequested && command.status != commandStatusAccepted {
		if sameCompletion(command, completion) {
			return nil
		}
		return errCommandTerminal
	}
	rows, err := commandsqlc.New(tx).CompleteCommand(ctx, commandsqlc.CompleteCommandParams{
		Status:      string(completion.status),
		CompletedAt: sql.NullString{String: formatTime(completion.completedAt), Valid: true},
		FailureCode: sql.NullString{String: string(completion.failureCode), Valid: true},
		ID:          string(completion.id),
	})
	if err != nil {
		return fmt.Errorf("complete command: %w", err)
	}
	if rows != 1 {
		return errCommandTerminal
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit command completion: %w", err)
	}
	return nil
}

func commandFromRow(row commandsqlc.Command) (commandRecord, error) {
	requestedAt, err := parseTime(row.RequestedAt)
	if err != nil {
		return commandRecord{}, fmt.Errorf("parse command requested_at: %w", err)
	}
	deadlineAt, err := parseTime(row.DeadlineAt)
	if err != nil {
		return commandRecord{}, fmt.Errorf("parse command deadline_at: %w", err)
	}
	acceptedAt, err := parseOptionalTime(row.AcceptedAt)
	if err != nil {
		return commandRecord{}, fmt.Errorf("parse command accepted_at: %w", err)
	}
	completedAt, err := parseOptionalTime(row.CompletedAt)
	if err != nil {
		return commandRecord{}, fmt.Errorf("parse command completed_at: %w", err)
	}
	command := commandRecord{
		id: CommandID(row.ID), entityID: EntityID(row.EntityID), adapterID: row.AdapterID,
		operationName: OperationName(row.Operation), parameters: CommandParameters(json.RawMessage(row.ParametersJson)),
		correlationID: CorrelationID(row.CorrelationID), status: commandStatus(row.Status),
		requestedAt: requestedAt, deadlineAt: deadlineAt, acceptedAt: acceptedAt, completedAt: completedAt,
	}
	if row.OutcomeObservationID.Valid {
		id := ObservationID(row.OutcomeObservationID.String)
		command.outcomeObservationID = &id
	}
	if row.FailureCode.Valid {
		code := commandFailureCode(row.FailureCode.String)
		command.failureCode = &code
	}
	return command, nil
}

func sameCompletion(command commandRecord, completion commandCompletion) bool {
	return command.status == completion.status && command.failureCode != nil &&
		*command.failureCode == completion.failureCode && command.completedAt != nil &&
		command.completedAt.Equal(completion.completedAt)
}
