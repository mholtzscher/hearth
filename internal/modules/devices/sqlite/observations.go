package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ProjectObservation(
	ctx context.Context,
	params devices.ProjectObservationParams,
) (devices.ProjectionResult, error) {
	if repository.catalog == nil {
		return devices.ProjectionResult{}, errors.New("project observation: entity type catalog is required")
	}
	if params.Now == nil {
		return devices.ProjectionResult{}, errors.New("project observation: clock is required")
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.ProjectionResult{}, fmt.Errorf("begin observation projection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	observationQueries := repository.queries.WithTx(tx)
	duplicate, err := observationExists(ctx, observationQueries, params.Observation.ID)
	if err != nil {
		return devices.ProjectionResult{}, err
	}
	if duplicate {
		return devices.ProjectionResult{Disposition: devices.DispositionDuplicate}, nil
	}

	stateQueries := repository.queries.WithTx(tx)
	var view devices.EntityWithState
	var linkedCommand *devices.CommandRecord
	var projectionNow time.Time
	var normalized devices.Value
	var rejection *devices.ObservationRejection
	disposition := devices.DispositionRejected
	observationRuntimeID, runtimeActive, err := observationRuntimeState(
		ctx, stateQueries, params.AdapterID, params.RuntimeID,
	)
	if err != nil {
		return devices.ProjectionResult{}, err
	}
	if !runtimeActive {
		staleRuntime := devices.RejectionStaleRuntime
		rejection = &staleRuntime
	} else {
		view, rejection, err = loadObservationEntity(ctx, stateQueries, params)
		if err != nil {
			return devices.ProjectionResult{}, err
		}
		linkedCommand, projectionNow, rejection, err = repository.resolveObservationLink(
			ctx, tx, params, view, rejection,
		)
		if err != nil {
			return devices.ProjectionResult{}, err
		}
		normalized, disposition, rejection, err = devices.ClassifyObservation(
			repository.catalog, view, params.Observation.Value, rejection,
		)
		if err != nil {
			return devices.ProjectionResult{}, err
		}
	}
	var previousValue devices.Value
	if disposition == devices.DispositionApplied || disposition == devices.DispositionUnchanged {
		if view.State != nil {
			previousValue = append(devices.Value(nil), view.State.Value...)
		}
	}

	receiveOrder, err := observationQueries.InsertObservation(ctx, dbsqlc.InsertObservationParams{
		ObservationID:     string(params.Observation.ID),
		AdapterID:         params.AdapterID,
		RuntimeID:         observationRuntimeID,
		EntityID:          string(params.Observation.EntityID),
		Disposition:       string(disposition),
		RejectionCode:     nullableRejection(rejection),
		StateValueJson:    nullableStateValue(disposition, normalized),
		AdapterReceivedAt: formatTime(params.Observation.AdapterReceivedAt),
		SourceUpdatedAt:   nullableTime(params.Observation.SourceUpdatedAt),
		// Fixed-width observed_at keeps the retention cutoff lexicographically comparable.
		ObservedAt: formatSortableTime(params.ObservedAt),
	})
	if err != nil {
		return devices.ProjectionResult{}, fmt.Errorf("insert observation: %w", err)
	}

	state, satisfied, err := repository.persistObservationState(
		ctx, tx, stateQueries, params, view, normalized, rejection, linkedCommand, projectionNow, receiveOrder,
	)
	if err != nil {
		return devices.ProjectionResult{}, err
	}
	// The device fact is queued inside this transaction, after every sibling
	// write, so the committed evidence and its pending fact are one atomic unit.
	pendingFactID, err := repository.queueAcceptedObservationDeviceFact(
		ctx, stateQueries, params, disposition, normalized, previousValue,
	)
	if err != nil {
		return devices.ProjectionResult{}, err
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.ProjectionResult{}, fmt.Errorf("commit observation projection: %w", commitErr)
	}
	return devices.ProjectionResult{
		Disposition: disposition, Rejection: rejection, State: state, SatisfiedCommand: satisfied,
		PendingFactID: pendingFactID,
	}, nil
}

func observationRuntimeState(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID devices.RuntimeID,
) (sql.NullString, bool, error) {
	observationRuntimeID := nullableText(string(runtimeID))
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if err == nil {
		return observationRuntimeID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, false, fmt.Errorf("validate observation runtime: %w", err)
	}
	_, err = queries.GetAdapterRuntime(ctx, dbsqlc.GetAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if err == nil {
		return observationRuntimeID, false, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, false, nil
	}
	return sql.NullString{}, false, fmt.Errorf("lookup stale observation runtime: %w", err)
}

func observationExists(
	ctx context.Context,
	queries *dbsqlc.Queries,
	observationID devices.ObservationID,
) (bool, error) {
	_, err := queries.GetObservation(ctx, dbsqlc.GetObservationParams{
		ObservationID: string(observationID),
	})
	if err == nil {
		return true, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return false, fmt.Errorf("get observation: %w", err)
}

func loadObservationEntity(
	ctx context.Context,
	queries *dbsqlc.Queries,
	params devices.ProjectObservationParams,
) (devices.EntityWithState, *devices.ObservationRejection, error) {
	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.Observation.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		rejection := devices.RejectionUnknownEntity
		return devices.EntityWithState{}, &rejection, nil
	}
	if err != nil {
		return devices.EntityWithState{}, nil, fmt.Errorf("get entity for observation: %w", err)
	}
	view, err := entityWithStateFromRow(row)
	if err != nil {
		return devices.EntityWithState{}, nil, err
	}
	if view.Entity.AdapterID != params.AdapterID {
		rejection := devices.RejectionWrongAdapter
		return view, &rejection, nil
	}
	return view, nil, nil
}

func (repository *DeviceRepository) resolveObservationLink(
	ctx context.Context,
	tx *sql.Tx,
	params devices.ProjectObservationParams,
	view devices.EntityWithState,
	rejection *devices.ObservationRejection,
) (*devices.CommandRecord, time.Time, *devices.ObservationRejection, error) {
	if rejection != nil {
		return nil, time.Time{}, rejection, nil
	}
	var linkedCommand *devices.CommandRecord
	var projectionNow time.Time
	var err error
	if params.Observation.RefreshForCommand != nil {
		linkedCommand, projectionNow, err = repository.activeLinkedCommand(ctx, tx, params)
		if err != nil {
			return nil, time.Time{}, nil, err
		}
	}
	if !view.Entity.Enabled && linkedCommand == nil {
		disabled := devices.RejectionEntityDisabled
		rejection = &disabled
	}
	return linkedCommand, projectionNow, rejection, nil
}

func (repository *DeviceRepository) persistObservationState(
	ctx context.Context,
	tx *sql.Tx,
	queries *dbsqlc.Queries,
	params devices.ProjectObservationParams,
	view devices.EntityWithState,
	normalized devices.Value,
	rejection *devices.ObservationRejection,
	linkedCommand *devices.CommandRecord,
	projectionNow time.Time,
	receiveOrder int64,
) (*devices.State, *devices.CommandResult, error) {
	if rejection != nil {
		return nil, nil, nil
	}
	state := devices.State{
		EntityID: params.Observation.EntityID, Value: append(devices.Value(nil), normalized...),
		ObservationID: params.Observation.ID, AdapterReceivedAt: params.Observation.AdapterReceivedAt.UTC(),
		SourceUpdatedAt: copyTimePointer(params.Observation.SourceUpdatedAt), ObservedAt: params.ObservedAt.UTC(),
		ReceiveOrder: receiveOrder,
	}
	if err := queries.UpsertEntityState(ctx, dbsqlc.UpsertEntityStateParams{
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
		ctx, tx, view.Entity, *linkedCommand, params.RuntimeID,
		params.Observation.ID, normalized, projectionNow,
	)
	if err != nil {
		return nil, nil, err
	}
	return &state, satisfied, nil
}

func (repository *DeviceRepository) activeLinkedCommand(
	ctx context.Context,
	tx *sql.Tx,
	params devices.ProjectObservationParams,
) (*devices.CommandRecord, time.Time, error) {
	id := *params.Observation.RefreshForCommand
	row, err := repository.queries.WithTx(tx).GetCommand(ctx, dbsqlc.GetCommandParams{ID: string(id)})
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
	if (command.Status != devices.CommandStatusRequested && command.Status != devices.CommandStatusAccepted) ||
		command.EntityID != params.Observation.EntityID || command.AdapterID != params.AdapterID ||
		command.RuntimeID == nil || *command.RuntimeID != params.RuntimeID {
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

func (repository *DeviceRepository) satisfyCommand(
	ctx context.Context,
	tx *sql.Tx,
	entity devices.Entity,
	command devices.CommandRecord,
	runtimeID devices.RuntimeID,
	observationID devices.ObservationID,
	value devices.Value,
	completedAt time.Time,
) (*devices.CommandResult, error) {
	matches, err := repository.catalog.Satisfies(entity, command, value)
	if err != nil {
		return nil, fmt.Errorf("evaluate linked command outcome: %w", err)
	}
	if !matches {
		return nil, nil //nolint:nilnil // No matching outcome is a successful projection.
	}
	queries := repository.queries.WithTx(tx)
	rows, err := queries.SatisfyCommandFromObservation(ctx, dbsqlc.SatisfyCommandFromObservationParams{
		CompletedAt:          sql.NullString{String: formatTime(completedAt), Valid: true},
		OutcomeObservationID: sql.NullString{String: string(observationID), Valid: true},
		ID:                   string(command.ID),
		EntityID:             string(command.EntityID),
		AdapterID:            command.AdapterID,
		RuntimeID:            nullableText(string(runtimeID)),
	})
	if err != nil {
		return nil, fmt.Errorf("satisfy linked command: %w", err)
	}
	if rows != 1 {
		return nil, nil //nolint:nilnil // A concurrently completed command has no result.
	}
	clonedValue := append(devices.Value(nil), value...)
	result, err := devices.NewCommandResult(command.ID, devices.OutcomeObserved, &observationID, &clonedValue)
	if err != nil {
		return nil, fmt.Errorf("build observed command result: %w", err)
	}
	return &result, nil
}

func (repository *DeviceRepository) DeleteExpiredObservations(ctx context.Context, before time.Time) error {
	_, err := repository.queries.
		DeleteExpiredObservations(ctx, dbsqlc.DeleteExpiredObservationsParams{
			ObservedAt: formatSortableTime(before),
		})
	if err != nil {
		return fmt.Errorf("delete expired observations: %w", err)
	}
	return nil
}

func nullableRejection(value *devices.ObservationRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableStateValue(disposition devices.ObservationDisposition, value devices.Value) sql.NullString {
	if disposition == devices.DispositionRejected || len(value) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}
