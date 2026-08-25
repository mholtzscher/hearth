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

type observationProjection struct {
	receipt          ObservationReceipt
	state            *State
	satisfiedCommand *CommandResult
}

func getEntityView(ctx context.Context, database sqliteDBTX, id EntityID) (EntityView, error) {
	row, err := statesqlc.New(database).GetEntityView(ctx, statesqlc.GetEntityViewParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return EntityView{}, ErrEntityNotFound
	}
	if err != nil {
		return EntityView{}, fmt.Errorf("get entity view: %w", err)
	}
	return entityViewFromRow(row)
}

func (service *Service) projectObservation(
	ctx context.Context,
	received ReceivedObservation,
	receiptExpiresAt time.Time,
) (observationProjection, error) {
	tx, err := service.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return observationProjection{}, fmt.Errorf("begin observation projection: %w", err)
	}
	defer tx.Rollback()

	observation := received.Observation
	receiptQueries := receiptsqlc.New(tx)
	_, err = receiptQueries.GetObservationReceipt(ctx, receiptsqlc.GetObservationReceiptParams{
		ObservationID: string(observation.ID),
	})
	switch {
	case err == nil:
		if err := tx.Commit(); err != nil {
			return observationProjection{}, fmt.Errorf("commit duplicate observation: %w", err)
		}
		return observationProjection{receipt: ObservationReceipt{Disposition: DispositionDuplicate}}, nil
	case !errors.Is(err, sql.ErrNoRows):
		return observationProjection{}, fmt.Errorf("get observation receipt: %w", err)
	}

	view, err := getEntityView(ctx, tx, observation.EntityID)
	var rejection *ObservationRejection
	switch {
	case errors.Is(err, ErrEntityNotFound):
		value := RejectionUnknownEntity
		rejection = &value
	case err != nil:
		return observationProjection{}, fmt.Errorf("get entity for observation: %w", err)
	default:
		if view.Entity.AdapterID != received.AdapterID {
			value := RejectionWrongAdapter
			rejection = &value
		}
	}

	var normalized Value
	disposition := DispositionRejected
	if rejection == nil {
		if _, err := service.catalog.normalizeSupport(view.Entity.TypeID, view.Entity.Support); err != nil {
			return observationProjection{}, fmt.Errorf("validate persisted entity support: %w", err)
		}
		normalized, err = service.catalog.normalizeState(view.Entity, observation.Value)
		if err != nil {
			value := RejectionInvalidValue
			rejection = &value
		} else if view.State == nil {
			disposition = DispositionApplied
		} else {
			equal, equalErr := service.catalog.equalState(view.Entity, view.State.Value, normalized)
			if equalErr != nil {
				return observationProjection{}, fmt.Errorf("compare observation state: %w", equalErr)
			}
			if equal {
				disposition = DispositionUnchanged
			} else {
				disposition = DispositionApplied
			}
		}
	}

	receiveOrder, err := receiptQueries.InsertObservationReceipt(ctx, receiptsqlc.InsertObservationReceiptParams{
		ObservationID:     string(observation.ID),
		AdapterID:         received.AdapterID,
		EntityID:          string(observation.EntityID),
		Disposition:       string(disposition),
		RejectionCode:     nullableRejection(rejection),
		AdapterReceivedAt: formatTime(observation.AdapterReceivedAt),
		ObservedAt:        formatTime(received.ObservedAt),
		ExpiresAt:         formatTime(receiptExpiresAt),
	})
	if err != nil {
		return observationProjection{}, fmt.Errorf("insert observation receipt: %w", err)
	}

	projection := observationProjection{receipt: ObservationReceipt{Disposition: disposition, Rejection: rejection}}
	if rejection == nil {
		state := State{
			EntityID:          observation.EntityID,
			Value:             append(Value(nil), normalized...),
			ObservationID:     observation.ID,
			AdapterReceivedAt: observation.AdapterReceivedAt.UTC(),
			SourceUpdatedAt:   copyTimePointer(observation.SourceUpdatedAt),
			ObservedAt:        received.ObservedAt.UTC(),
			ReceiveOrder:      receiveOrder,
		}
		if err := statesqlc.New(tx).UpsertEntityState(ctx, statesqlc.UpsertEntityStateParams{
			EntityID:          string(state.EntityID),
			ObservationID:     string(state.ObservationID),
			ValueJson:         string(state.Value),
			AdapterReceivedAt: formatTime(state.AdapterReceivedAt),
			SourceUpdatedAt:   nullableTime(state.SourceUpdatedAt),
			ObservedAt:        formatTime(state.ObservedAt),
			ReceiveOrder:      state.ReceiveOrder,
		}); err != nil {
			return observationProjection{}, fmt.Errorf("upsert entity state: %w", err)
		}
		projection.state = &state

		if observation.RefreshForCommand != nil {
			satisfied, satisfyErr := service.satisfyCommand(ctx, tx, view.Entity, received, normalized)
			if satisfyErr != nil {
				return observationProjection{}, satisfyErr
			}
			projection.satisfiedCommand = satisfied
		}
	}

	if err := tx.Commit(); err != nil {
		return observationProjection{}, fmt.Errorf("commit observation projection: %w", err)
	}
	return projection, nil
}

func (service *Service) satisfyCommand(
	ctx context.Context,
	tx *sql.Tx,
	entity Entity,
	received ReceivedObservation,
	value Value,
) (*CommandResult, error) {
	observation := received.Observation
	id := *observation.RefreshForCommand
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
	if (command.status != commandStatusRequested && command.status != commandStatusAccepted) ||
		command.entityID != observation.EntityID || command.adapterID != received.AdapterID {
		return nil, nil
	}
	matches, err := service.catalog.satisfies(entity, command, value)
	if err != nil {
		return nil, fmt.Errorf("evaluate linked command outcome: %w", err)
	}
	if !matches {
		return nil, nil
	}
	completedAt, err := serviceNow(service.controls.now, "complete linked Command")
	if err != nil {
		return nil, err
	}
	if completedAt.After(command.deadlineAt) {
		return nil, nil
	}
	rows, err := queries.SatisfyCommandFromObservation(ctx, commandsqlc.SatisfyCommandFromObservationParams{
		CompletedAt:          sql.NullString{String: formatTime(completedAt), Valid: true},
		OutcomeObservationID: sql.NullString{String: string(observation.ID), Valid: true},
		ID:                   string(command.id),
		EntityID:             string(command.entityID),
		AdapterID:            command.adapterID,
	})
	if err != nil {
		return nil, fmt.Errorf("satisfy linked command: %w", err)
	}
	if rows != 1 {
		return nil, nil
	}
	return &CommandResult{
		CommandID: command.id, ObservationID: observation.ID, Value: append(Value(nil), value...),
	}, nil
}

func entityViewFromRow(row statesqlc.GetEntityViewRow) (EntityView, error) {
	view := EntityView{Entity: Entity{
		ID: EntityID(row.ID), DeviceID: DeviceID(row.DeviceID), AdapterID: row.AdapterID,
		Name: row.Name, TypeID: EntityTypeID(row.TypeID), Support: EntitySupport(row.SupportJson),
	}}
	if !row.ObservationID.Valid {
		return view, nil
	}
	if !row.ValueJson.Valid || !row.AdapterReceivedAt.Valid || !row.ObservedAt.Valid || !row.ReceiveOrder.Valid {
		return EntityView{}, errors.New("entity state row is incomplete")
	}
	adapterReceivedAt, err := parseTime(row.AdapterReceivedAt.String)
	if err != nil {
		return EntityView{}, fmt.Errorf("parse state adapter_received_at: %w", err)
	}
	observedAt, err := parseTime(row.ObservedAt.String)
	if err != nil {
		return EntityView{}, fmt.Errorf("parse state observed_at: %w", err)
	}
	sourceUpdatedAt, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return EntityView{}, fmt.Errorf("parse state source_updated_at: %w", err)
	}
	view.State = &State{
		EntityID: EntityID(row.ID), Value: Value(row.ValueJson.String), ObservationID: ObservationID(row.ObservationID.String),
		AdapterReceivedAt: adapterReceivedAt, SourceUpdatedAt: sourceUpdatedAt, ObservedAt: observedAt,
		ReceiveOrder: row.ReceiveOrder.Int64,
	}
	return view, nil
}

func nullableRejection(value *ObservationRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}
