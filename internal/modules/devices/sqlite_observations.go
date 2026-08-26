package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	commandsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/commands"
	receiptsqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/receipts"
	statesqlc "github.com/mholtzscher/hearth/internal/platform/db/sqlc/state"
)

func (repository *SQLiteRepository) GetEntity(ctx context.Context, id EntityID) (EntityWithState, error) {
	row, err := statesqlc.New(repository.database).GetEntity(ctx, statesqlc.GetEntityParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return EntityWithState{}, ErrEntityNotFound
	}
	if err != nil {
		return EntityWithState{}, fmt.Errorf("get entity: %w", err)
	}
	return entityWithStateFromValues(
		row.ID, row.DeviceID, row.AdapterID, row.Name, row.TypeID, row.SupportJson,
		row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
		row.ObservedAt, row.ReceiveOrder,
	)
}

func (repository *SQLiteRepository) ProjectObservation(ctx context.Context, params ProjectObservationParams) (ProjectionResult, error) {
	if repository.catalog == nil {
		return ProjectionResult{}, errors.New("project observation: entity type catalog is required")
	}
	if params.Now == nil {
		return ProjectionResult{}, errors.New("project observation: clock is required")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return ProjectionResult{}, fmt.Errorf("begin observation projection: %w", err)
	}
	defer tx.Rollback()

	receiptQueries := receiptsqlc.New(tx)
	_, err = receiptQueries.GetObservationReceipt(ctx, receiptsqlc.GetObservationReceiptParams{
		ObservationID: string(params.Observation.ID),
	})
	switch {
	case err == nil:
		return ProjectionResult{Disposition: DispositionDuplicate}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return ProjectionResult{}, fmt.Errorf("get observation receipt: %w", err)
	}

	stateQueries := statesqlc.New(tx)
	row, err := stateQueries.GetEntity(ctx, statesqlc.GetEntityParams{ID: string(params.Observation.EntityID)})
	var view EntityWithState
	var rejection *ObservationRejection
	switch {
	case errors.Is(err, sql.ErrNoRows):
		value := RejectionUnknownEntity
		rejection = &value
	case err != nil:
		return ProjectionResult{}, fmt.Errorf("get entity for observation: %w", err)
	default:
		view, err = entityWithStateFromValues(
			row.ID, row.DeviceID, row.AdapterID, row.Name, row.TypeID, row.SupportJson,
			row.ObservationID, row.ValueJson, row.AdapterReceivedAt, row.SourceUpdatedAt,
			row.ObservedAt, row.ReceiveOrder,
		)
		if err != nil {
			return ProjectionResult{}, err
		}
		if view.Entity.AdapterID != params.AdapterID {
			value := RejectionWrongAdapter
			rejection = &value
		}
	}

	var normalized Value
	disposition := DispositionRejected
	if rejection == nil {
		if _, err := repository.catalog.NormalizeSupport(view.Entity.TypeID, view.Entity.Support); err != nil {
			return ProjectionResult{}, fmt.Errorf("validate persisted entity support: %w", err)
		}
		normalized, err = repository.catalog.NormalizeState(view.Entity, params.Observation.Value)
		if err != nil {
			value := RejectionInvalidValue
			rejection = &value
		} else if view.State == nil {
			disposition = DispositionApplied
		} else {
			equal, equalErr := repository.catalog.EqualState(view.Entity, view.State.Value, normalized)
			if equalErr != nil {
				return ProjectionResult{}, fmt.Errorf("compare observation state: %w", equalErr)
			}
			if equal {
				disposition = DispositionUnchanged
			} else {
				disposition = DispositionApplied
			}
		}
	}

	receiveOrder, err := receiptQueries.InsertObservationReceipt(ctx, receiptsqlc.InsertObservationReceiptParams{
		ObservationID:     string(params.Observation.ID),
		AdapterID:         params.AdapterID,
		EntityID:          string(params.Observation.EntityID),
		Disposition:       string(disposition),
		RejectionCode:     nullableRejection(rejection),
		AdapterReceivedAt: formatTime(params.Observation.AdapterReceivedAt),
		ObservedAt:        formatTime(params.ObservedAt),
		ExpiresAt:         formatTime(params.ReceiptExpiresAt),
	})
	if err != nil {
		return ProjectionResult{}, fmt.Errorf("insert observation receipt: %w", err)
	}

	result := ProjectionResult{Disposition: disposition, Rejection: rejection}
	if rejection == nil {
		state := State{
			EntityID:          params.Observation.EntityID,
			Value:             append(Value(nil), normalized...),
			ObservationID:     params.Observation.ID,
			AdapterReceivedAt: params.Observation.AdapterReceivedAt.UTC(),
			SourceUpdatedAt:   copyTimePointer(params.Observation.SourceUpdatedAt),
			ObservedAt:        params.ObservedAt.UTC(),
			ReceiveOrder:      receiveOrder,
		}
		if err := stateQueries.UpsertEntityState(ctx, statesqlc.UpsertEntityStateParams{
			EntityID:          string(state.EntityID),
			ObservationID:     string(state.ObservationID),
			ValueJson:         string(state.Value),
			AdapterReceivedAt: formatTime(state.AdapterReceivedAt),
			SourceUpdatedAt:   nullableTime(state.SourceUpdatedAt),
			ObservedAt:        formatTime(state.ObservedAt),
			ReceiveOrder:      state.ReceiveOrder,
		}); err != nil {
			return ProjectionResult{}, fmt.Errorf("upsert entity state: %w", err)
		}
		result.State = &state

		if params.Observation.RefreshForCommand != nil {
			satisfied, satisfyErr := repository.satisfyCommand(ctx, tx, view.Entity, params, normalized)
			if satisfyErr != nil {
				return ProjectionResult{}, satisfyErr
			}
			result.SatisfiedCommand = satisfied
		}
	}

	if err := tx.Commit(); err != nil {
		return ProjectionResult{}, fmt.Errorf("commit observation projection: %w", err)
	}
	return result, nil
}

func (repository *SQLiteRepository) satisfyCommand(
	ctx context.Context,
	tx *sql.Tx,
	entity Entity,
	params ProjectObservationParams,
	value Value,
) (*CommandResult, error) {
	id := *params.Observation.RefreshForCommand
	queries := commandsqlc.New(tx)
	row, err := queries.GetCommand(ctx, commandsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get linked command: %w", err)
	}
	command, err := commandFromRow(row)
	if err != nil {
		return nil, fmt.Errorf("map linked command: %w", err)
	}
	if (command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted) ||
		command.EntityID != params.Observation.EntityID || command.AdapterID != params.AdapterID {
		return nil, nil
	}
	matches, err := repository.catalog.Satisfies(entity, command, value)
	if err != nil {
		return nil, fmt.Errorf("evaluate linked command outcome: %w", err)
	}
	if !matches {
		return nil, nil
	}
	// The receipt and State writes already hold SQLite's writer lock, so this
	// deadline decision and the Command update are atomic with timeout writes.
	completedAt := params.Now().UTC()
	if completedAt.IsZero() {
		return nil, errors.New("complete linked command: clock returned zero time")
	}
	if completedAt.After(command.DeadlineAt) {
		return nil, nil
	}
	rows, err := queries.SatisfyCommandFromObservation(ctx, commandsqlc.SatisfyCommandFromObservationParams{
		CompletedAt:          sql.NullString{String: formatTime(completedAt), Valid: true},
		OutcomeObservationID: sql.NullString{String: string(params.Observation.ID), Valid: true},
		ID:                   string(command.ID),
		EntityID:             string(command.EntityID),
		AdapterID:            command.AdapterID,
	})
	if err != nil {
		return nil, fmt.Errorf("satisfy linked command: %w", err)
	}
	if rows != 1 {
		return nil, nil
	}
	return &CommandResult{
		CommandID: command.ID, ObservationID: params.Observation.ID, Value: append(Value(nil), value...),
	}, nil
}

func (repository *SQLiteRepository) DeleteExpiredObservationReceipts(ctx context.Context, before time.Time) error {
	_, err := receiptsqlc.New(repository.database).DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
		ExpiresAt: formatTime(before),
	})
	if err != nil {
		return fmt.Errorf("delete expired observation receipts: %w", err)
	}
	return nil
}

func entityWithStateFromValues(
	id, deviceID, adapterID, name, typeID, supportJSON string,
	observationID, valueJSON, adapterReceivedAt, sourceUpdatedAt, observedAt sql.NullString,
	receiveOrder sql.NullInt64,
) (EntityWithState, error) {
	view := EntityWithState{Entity: Entity{
		ID: EntityID(id), DeviceID: DeviceID(deviceID), AdapterID: adapterID,
		Name: name, TypeID: EntityTypeID(typeID), Support: EntitySupport(supportJSON),
	}}
	if !observationID.Valid {
		return view, nil
	}
	if !valueJSON.Valid || !adapterReceivedAt.Valid || !observedAt.Valid || !receiveOrder.Valid {
		return EntityWithState{}, errors.New("entity state row is incomplete")
	}
	adapterTime, err := parseTime(adapterReceivedAt.String)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state adapter_received_at: %w", err)
	}
	observedTime, err := parseTime(observedAt.String)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state observed_at: %w", err)
	}
	sourceTime, err := parseOptionalTime(sourceUpdatedAt)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state source_updated_at: %w", err)
	}
	view.State = &State{
		EntityID: EntityID(id), Value: Value(valueJSON.String), ObservationID: ObservationID(observationID.String),
		AdapterReceivedAt: adapterTime, SourceUpdatedAt: sourceTime, ObservedAt: observedTime,
		ReceiveOrder: receiveOrder.Int64,
	}
	return view, nil
}

func nullableRejection(value *ObservationRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*value), Valid: true}
}

func copyTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copy := value.UTC()
	return &copy
}
