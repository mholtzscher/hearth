package devices

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

func (repository *SQLiteRepository) GetEntity(ctx context.Context, id EntityID) (EntityWithState, error) {
	row, err := repository.queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return EntityWithState{}, ErrEntityNotFound
	}
	if err != nil {
		return EntityWithState{}, fmt.Errorf("get entity: %w", err)
	}
	return entityWithStateFromRow(row)
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

	receiptQueries := repository.queries.WithTx(tx)
	duplicate, err := observationReceiptExists(ctx, receiptQueries, params.Observation.ID)
	if err != nil {
		return ProjectionResult{}, err
	}
	if duplicate {
		return ProjectionResult{Disposition: DispositionDuplicate}, nil
	}

	stateQueries := repository.queries.WithTx(tx)
	var view EntityWithState
	var linkedCommand *CommandRecord
	var projectionNow time.Time
	var normalized Value
	var rejection *ObservationRejection
	disposition := DispositionRejected
	receiptRuntimeID, runtimeActive, err := observationRuntimeState(
		ctx, stateQueries, params.AdapterID, params.RuntimeID,
	)
	if err != nil {
		return ProjectionResult{}, err
	}
	if !runtimeActive {
		staleRuntime := RejectionStaleRuntime
		rejection = &staleRuntime
	} else {
		view, rejection, err = loadObservationEntity(ctx, stateQueries, params)
		if err != nil {
			return ProjectionResult{}, err
		}
		linkedCommand, projectionNow, rejection, err = repository.resolveObservationLink(
			ctx, tx, params, view, rejection,
		)
		if err != nil {
			return ProjectionResult{}, err
		}
		normalized, disposition, rejection, err = repository.classifyObservation(
			view, params.Observation.Value, rejection,
		)
		if err != nil {
			return ProjectionResult{}, err
		}
	}

	receiveOrder, err := receiptQueries.InsertObservationReceipt(ctx, dbsqlc.InsertObservationReceiptParams{
		ObservationID:     string(params.Observation.ID),
		AdapterID:         params.AdapterID,
		RuntimeID:         receiptRuntimeID,
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

func observationRuntimeState(
	ctx context.Context,
	queries *dbsqlc.Queries,
	adapterID string,
	runtimeID RuntimeID,
) (sql.NullString, bool, error) {
	receiptRuntimeID := nullableText(string(runtimeID))
	_, err := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if err == nil {
		return receiptRuntimeID, true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, false, fmt.Errorf("validate observation runtime: %w", err)
	}
	_, err = queries.GetAdapterRuntime(ctx, dbsqlc.GetAdapterRuntimeParams{
		AdapterID: adapterID, RuntimeID: string(runtimeID),
	})
	if err == nil {
		return receiptRuntimeID, false, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return sql.NullString{}, false, nil
	}
	return sql.NullString{}, false, fmt.Errorf("lookup stale observation runtime: %w", err)
}

func observationReceiptExists(
	ctx context.Context,
	queries *dbsqlc.Queries,
	observationID ObservationID,
) (bool, error) {
	_, err := queries.GetObservationReceipt(ctx, dbsqlc.GetObservationReceiptParams{
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
	queries *dbsqlc.Queries,
	params ProjectObservationParams,
) (EntityWithState, *ObservationRejection, error) {
	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.Observation.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		rejection := RejectionUnknownEntity
		return EntityWithState{}, &rejection, nil
	}
	if err != nil {
		return EntityWithState{}, nil, fmt.Errorf("get entity for observation: %w", err)
	}
	view, err := entityWithStateFromRow(row)
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
	queries *dbsqlc.Queries,
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

func (repository *SQLiteRepository) activeLinkedCommand(
	ctx context.Context,
	tx *sql.Tx,
	params ProjectObservationParams,
) (*CommandRecord, time.Time, error) {
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
	if (command.Status != CommandStatusRequested && command.Status != CommandStatusAccepted) ||
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

func (repository *SQLiteRepository) satisfyCommand(
	ctx context.Context,
	tx *sql.Tx,
	entity Entity,
	command CommandRecord,
	runtimeID RuntimeID,
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
	return &CommandResult{
		CommandID: command.ID, ObservationID: observationID, Value: append(Value(nil), value...),
	}, nil
}

func (repository *SQLiteRepository) DeleteExpiredObservationReceipts(ctx context.Context, before time.Time) error {
	_, err := repository.queries.
		DeleteExpiredObservationReceipts(ctx, dbsqlc.DeleteExpiredObservationReceiptsParams{
			ExpiresAt: formatTime(before),
		})
	if err != nil {
		return fmt.Errorf("delete expired observation receipts: %w", err)
	}
	return nil
}

type sqliteEntityAvailability struct {
	entityCreatedAt    string
	adapterStatus      sql.NullString
	adapterReasonCode  sql.NullString
	adapterSince       sql.NullString
	adapterEvidenceAt  sql.NullString
	reportedStatus     sql.NullString
	reportedReasonCode sql.NullString
	reportedSourceAt   sql.NullString
	reportedEvidenceAt sql.NullString
	reportedSince      sql.NullString
}

func entityWithStateFromRow(row dbsqlc.EntityReadProjection) (EntityWithState, error) {
	availability, err := entityAvailabilityFromValues(sqliteEntityAvailability{
		entityCreatedAt: row.EntityCreatedAt,
		adapterStatus:   row.AdapterHealthStatus, adapterReasonCode: row.AdapterHealthReasonCode,
		adapterSince: row.AdapterHealthSince, adapterEvidenceAt: row.AdapterHealthEvidenceAt,
		reportedStatus:     row.ReportedAvailabilityStatus,
		reportedReasonCode: row.ReportedAvailabilityReasonCode,
		reportedSourceAt:   row.ReportedAvailabilitySourceObservedAt,
		reportedEvidenceAt: row.ReportedAvailabilityEvidenceAt,
		reportedSince:      row.ReportedAvailabilitySince,
	})
	if err != nil {
		return EntityWithState{}, err
	}
	view := EntityWithState{
		Entity: Entity{
			ID: EntityID(row.ID), DeviceID: DeviceID(row.DeviceID), AdapterID: row.AdapterID,
			Name: row.Name, TypeID: EntityTypeID(row.TypeID), Support: EntitySupport(row.SupportJson),
			Enabled: row.Enabled != 0,
		},
		Availability: availability,
	}
	if !row.ObservationID.Valid {
		return view, nil
	}
	if !row.ValueJson.Valid || !row.AdapterReceivedAt.Valid || !row.ObservedAt.Valid || !row.ReceiveOrder.Valid {
		return EntityWithState{}, errors.New("entity state row is incomplete")
	}
	adapterTime, err := parseTime(row.AdapterReceivedAt.String)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state adapter_received_at: %w", err)
	}
	observedTime, err := parseTime(row.ObservedAt.String)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state observed_at: %w", err)
	}
	sourceTime, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return EntityWithState{}, fmt.Errorf("parse state source_updated_at: %w", err)
	}
	view.State = &State{
		EntityID: EntityID(row.ID), Value: Value(row.ValueJson.String),
		ObservationID: ObservationID(row.ObservationID.String), AdapterReceivedAt: adapterTime,
		SourceUpdatedAt: sourceTime, ObservedAt: observedTime, ReceiveOrder: row.ReceiveOrder.Int64,
	}
	return view, nil
}

func entityAvailabilityFromValues(values sqliteEntityAvailability) (EntityAvailability, error) {
	if !values.adapterStatus.Valid {
		observedAt, err := parseTime(values.entityCreatedAt)
		if err != nil {
			return EntityAvailability{}, fmt.Errorf("parse Entity creation time for availability: %w", err)
		}
		return EntityAvailability{
			Status: EntityAvailabilityUnknown, Source: healthSourceCore, Since: observedAt, EvidenceAt: observedAt,
			Reason: &HealthReason{Code: "hearth.awaiting_runtime"},
		}, nil
	}

	adapterStatus := AdapterHealthStatus(values.adapterStatus.String)
	if adapterStatus == AdapterHealthHealthy && values.reportedStatus.Valid {
		since, evidenceAt, sourceObservedAt, err := parseAvailabilityTimes(
			values.reportedSince, values.reportedEvidenceAt, values.reportedSourceAt,
		)
		if err != nil {
			return EntityAvailability{}, err
		}
		return EntityAvailability{
			Status: EntityAvailabilityStatus(values.reportedStatus.String), Source: "entity_report",
			Since: since, EvidenceAt: evidenceAt, SourceObservedAt: sourceObservedAt,
			Reason: healthReasonFromNull(values.reportedReasonCode),
		}, nil
	}

	since, err := parseRequiredTime(values.adapterSince, "Adapter health since for Entity availability")
	if err != nil {
		return EntityAvailability{}, err
	}
	evidenceAt, err := parseRequiredTime(values.adapterEvidenceAt, "Adapter health evidence for Entity availability")
	if err != nil {
		return EntityAvailability{}, err
	}
	if adapterStatus == AdapterHealthHealthy {
		return EntityAvailability{
			Status: EntityAvailabilityUnknown, Source: healthSourceCore, Since: since, EvidenceAt: evidenceAt,
			Reason: &HealthReason{Code: "hearth.awaiting_entity_report"},
		}, nil
	}
	status := EntityAvailabilityUnknown
	if adapterStatus == AdapterHealthUnhealthy {
		status = EntityAvailabilityUnavailable
	}
	return EntityAvailability{
		Status: status, Source: "adapter_health", Since: since, EvidenceAt: evidenceAt,
		Reason: healthReasonFromNull(values.adapterReasonCode),
	}, nil
}

func parseAvailabilityTimes(
	sinceValue, evidenceValue, sourceValue sql.NullString,
) (time.Time, time.Time, *time.Time, error) {
	since, err := parseRequiredTime(sinceValue, "Entity availability since")
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	evidenceAt, err := parseRequiredTime(evidenceValue, "Entity availability evidence")
	if err != nil {
		return time.Time{}, time.Time{}, nil, err
	}
	sourceObservedAt, err := parseOptionalTime(sourceValue)
	if err != nil {
		return time.Time{}, time.Time{}, nil, fmt.Errorf("parse Entity availability source time: %w", err)
	}
	return since, evidenceAt, sourceObservedAt, nil
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
