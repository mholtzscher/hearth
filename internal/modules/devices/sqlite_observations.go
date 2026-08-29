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
		row.ID, row.DeviceID, row.AdapterID, row.BindingKey, row.EntityKey, row.ExternalEntityID,
		row.Name, row.TypeID, row.SupportJson, row.Enabled, row.ObservationID, row.ValueJson,
		row.AdapterReceivedAt, row.SourceUpdatedAt, row.ObservedAt, row.ReceiveOrder,
	)
}

func (repository *SQLiteRepository) ProjectObservation(
	ctx context.Context,
	params ProjectObservationParams,
) (ProjectionResult, error) {
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
	defer func() { _ = tx.Rollback() }()

	receiptQueries := receiptsqlc.New(tx)
	duplicate, err := observationReceiptExists(ctx, receiptQueries, params.Observation.ID)
	if err != nil {
		return ProjectionResult{}, err
	}
	if duplicate {
		return ProjectionResult{Disposition: DispositionDuplicate}, nil
	}

	stateQueries := statesqlc.New(tx)
	view, rejection, err := loadObservationEntity(ctx, stateQueries, params)
	if err != nil {
		return ProjectionResult{}, err
	}
	linkedCommand, projectionNow, rejection, err := repository.resolveObservationLink(ctx, tx, params, view, rejection)
	if err != nil {
		return ProjectionResult{}, err
	}
	normalized, disposition, rejection, err := repository.classifyObservation(view, params.Observation.Value, rejection)
	if err != nil {
		return ProjectionResult{}, err
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

	state, satisfied, err := repository.persistObservationState(
		ctx, tx, stateQueries, params, view, normalized, rejection, linkedCommand, projectionNow, receiveOrder,
	)
	if err != nil {
		return ProjectionResult{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return ProjectionResult{}, fmt.Errorf("commit observation projection: %w", commitErr)
	}
	return ProjectionResult{
		Disposition: disposition, Rejection: rejection, State: state, SatisfiedCommand: satisfied,
	}, nil
}

func observationReceiptExists(
	ctx context.Context,
	queries *receiptsqlc.Queries,
	observationID ObservationID,
) (bool, error) {
	_, err := queries.GetObservationReceipt(ctx, receiptsqlc.GetObservationReceiptParams{
		ObservationID: string(observationID),
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("get observation receipt: %w", err)
}

func loadObservationEntity(
	ctx context.Context,
	queries *statesqlc.Queries,
	params ProjectObservationParams,
) (EntityWithState, *ObservationRejection, error) {
	row, err := queries.GetEntity(ctx, statesqlc.GetEntityParams{ID: string(params.Observation.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		rejection := RejectionUnknownEntity
		return EntityWithState{}, &rejection, nil
	}
	if err != nil {
		return EntityWithState{}, nil, fmt.Errorf("get entity for observation: %w", err)
	}
	view, err := entityWithStateFromValues(
		row.ID, row.DeviceID, row.AdapterID, row.BindingKey, row.EntityKey, row.ExternalEntityID,
		row.Name, row.TypeID, row.SupportJson, row.Enabled, row.ObservationID, row.ValueJson,
		row.AdapterReceivedAt, row.SourceUpdatedAt, row.ObservedAt, row.ReceiveOrder,
	)
	if err != nil {
		return EntityWithState{}, nil, err
	}
	if view.Entity.AdapterID != params.AdapterID {
		rejection := RejectionWrongAdapter
		return view, &rejection, nil
	}
	return view, nil, nil
}

func (repository *SQLiteRepository) resolveObservationLink(
	ctx context.Context,
	tx *sql.Tx,
	params ProjectObservationParams,
	view EntityWithState,
	rejection *ObservationRejection,
) (*CommandRecord, time.Time, *ObservationRejection, error) {
	if rejection != nil {
		return nil, time.Time{}, rejection, nil
	}
	var linkedCommand *CommandRecord
	var projectionNow time.Time
	var err error
	if params.Observation.RefreshForCommand != nil {
		linkedCommand, projectionNow, err = repository.activeLinkedCommand(ctx, tx, params)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
	}
	if !view.Entity.Enabled && linkedCommand == nil {
		disabled := RejectionEntityDisabled
		rejection = &disabled
	}
	return linkedCommand, projectionNow, rejection, nil
}

func (repository *SQLiteRepository) classifyObservation(
	view EntityWithState,
	value Value,
	rejection *ObservationRejection,
) (Value, ObservationDisposition, *ObservationRejection, error) {
	if rejection != nil {
		return nil, DispositionRejected, rejection, nil
	}
	if _, err := repository.catalog.NormalizeSupport(view.Entity.TypeID, view.Entity.Support); err != nil {
		return nil, "", nil, fmt.Errorf("validate persisted entity support: %w", err)
	}
	normalized, valid := repository.normalizeObservationState(view.Entity, value)
	if !valid {
		invalid := RejectionInvalidValue
		return nil, DispositionRejected, &invalid, nil
	}
	if view.State == nil {
		return normalized, DispositionApplied, nil, nil
	}
	equal, err := repository.catalog.EqualState(view.Entity, view.State.Value, normalized)
	if err != nil {
		return nil, "", nil, fmt.Errorf("compare observation state: %w", err)
	}
	if equal {
		return normalized, DispositionUnchanged, nil, nil
	}
	return normalized, DispositionApplied, nil, nil
}

func (repository *SQLiteRepository) normalizeObservationState(entity Entity, value Value) (Value, bool) {
	normalized, err := repository.catalog.NormalizeState(entity, value)
	return normalized, err == nil
}

func (repository *SQLiteRepository) persistObservationState(
	ctx context.Context,
	tx *sql.Tx,
	queries *statesqlc.Queries,
	params ProjectObservationParams,
	view EntityWithState,
	normalized Value,
	rejection *ObservationRejection,
	linkedCommand *CommandRecord,
	projectionNow time.Time,
	receiveOrder int64,
) (*State, *CommandResult, error) {
	if rejection != nil {
		return nil, nil, nil
	}
	state := State{
		EntityID: params.Observation.EntityID, Value: append(Value(nil), normalized...),
		ObservationID: params.Observation.ID, AdapterReceivedAt: params.Observation.AdapterReceivedAt.UTC(),
		SourceUpdatedAt: copyTimePointer(params.Observation.SourceUpdatedAt), ObservedAt: params.ObservedAt.UTC(),
		ReceiveOrder: receiveOrder,
	}
	if err := queries.UpsertEntityState(ctx, statesqlc.UpsertEntityStateParams{
		EntityID: string(state.EntityID), ObservationID: string(state.ObservationID), ValueJson: string(state.Value),
		AdapterReceivedAt: formatTime(state.AdapterReceivedAt), SourceUpdatedAt: nullableTime(state.SourceUpdatedAt),
		ObservedAt: formatTime(state.ObservedAt), ReceiveOrder: state.ReceiveOrder,
	}); err != nil {
		return nil, nil, fmt.Errorf("upsert entity state: %w", err)
	}
	if linkedCommand == nil {
		return &state, nil, nil
	}
	satisfied, err := repository.satisfyCommand(
		ctx, tx, view.Entity, *linkedCommand, params.Observation.ID, normalized, projectionNow,
	)
	if err != nil {
		return nil, nil, err
	}
	return &state, satisfied, nil
}

func (repository *SQLiteRepository) activeLinkedCommand(
	ctx context.Context,
	tx *sql.Tx,
	params ProjectObservationParams,
) (*CommandRecord, time.Time, error) {
	id := *params.Observation.RefreshForCommand
	row, err := commandsqlc.New(tx).GetCommand(ctx, commandsqlc.GetCommandParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, time.Time{}, nil
	}
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("get linked command: %w", err)
	}
	command, err := commandFromRow(row)
	if err != nil {
		return nil, time.Time{}, fmt.Errorf("map linked command: %w", err)
	}
	if (command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted) ||
		command.EntityID != params.Observation.EntityID || command.AdapterID != params.AdapterID {
		return nil, time.Time{}, nil
	}
	completedAt := params.Now().UTC()
	if completedAt.IsZero() {
		return nil, time.Time{}, errors.New("complete linked command: clock returned zero time")
	}
	if completedAt.After(command.DeadlineAt) {
		return nil, time.Time{}, nil
	}
	return &command, completedAt, nil
}

func (repository *SQLiteRepository) satisfyCommand(
	ctx context.Context,
	tx *sql.Tx,
	entity Entity,
	command CommandRecord,
	observationID ObservationID,
	value Value,
	completedAt time.Time,
) (*CommandResult, error) {
	matches, err := repository.catalog.Satisfies(entity, command, value)
	if err != nil {
		return nil, fmt.Errorf("evaluate linked command outcome: %w", err)
	}
	if !matches {
		return nil, nil //nolint:nilnil // No matching outcome is a successful projection.
	}
	rows, err := commandsqlc.New(tx).SatisfyCommandFromObservation(ctx, commandsqlc.SatisfyCommandFromObservationParams{
		CompletedAt:          sql.NullString{String: formatTime(completedAt), Valid: true},
		OutcomeObservationID: sql.NullString{String: string(observationID), Valid: true},
		ID:                   string(command.ID),
		EntityID:             string(command.EntityID),
		AdapterID:            command.AdapterID,
	})
	if err != nil {
		return nil, fmt.Errorf("satisfy linked command: %w", err)
	}
	if rows != 1 {
		return nil, nil //nolint:nilnil // A concurrently completed command has no result.
	}
	return &CommandResult{
		CommandID: command.ID, ObservationID: observationID, Value: append(Value(nil), value...),
	}, nil
}

func (repository *SQLiteRepository) DeleteExpiredObservationReceipts(ctx context.Context, before time.Time) error {
	_, err := receiptsqlc.New(repository.database).
		DeleteExpiredObservationReceipts(ctx, receiptsqlc.DeleteExpiredObservationReceiptsParams{
			ExpiresAt: formatTime(before),
		})
	if err != nil {
		return fmt.Errorf("delete expired observation receipts: %w", err)
	}
	return nil
}

func entityWithStateFromValues(
	id, deviceID, adapterID, bindingKey, entityKey, externalID, name, typeID, supportJSON string,
	enabled int64,
	observationID, valueJSON, adapterReceivedAt, sourceUpdatedAt, observedAt sql.NullString,
	receiveOrder sql.NullInt64,
) (EntityWithState, error) {
	if adapterID == "" || bindingKey == "" || entityKey == "" || externalID == "" {
		return EntityWithState{}, errors.New("entity binding row is incomplete")
	}
	view := EntityWithState{Entity: Entity{
		ID: EntityID(id), DeviceID: DeviceID(deviceID), AdapterID: adapterID,
		BindingKey: bindingKey, EntityKey: entityKey, ExternalID: externalID,
		Name: name, TypeID: EntityTypeID(typeID), Support: EntitySupport(supportJSON), Enabled: enabled != 0,
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
	cloned := value.UTC()
	return &cloned
}
