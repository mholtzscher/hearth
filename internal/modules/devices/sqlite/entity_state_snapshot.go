package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// getEntityStateSnapshotSQL reads one coherent State snapshot for a whole batch
// of Entity IDs. The requested IDs travel as one JSON array parameter that
// json_each expands into the requested-ID relation, so a cross-Automation
// Condition fan-out never spends one SQLite host parameter per Entity. The
// relation is left-joined to entities, entity_states, and observations, so a
// requested ID that matches no Entity, or an Entity with no accepted State,
// still yields exactly one row of negative evidence instead of silently
// disappearing.
//
// The State's backing Observation joins on both the canonical Observation
// identity and the receive order. entity_states holds those as two independent
// foreign keys, so only requiring both to name the same Observations row can
// reject a State whose identity and receive order point at different rows.
// Every backing column stays NULL for such a State, and one row of NULL is the
// evidence the scanner rejects as corrupt rather than serving as Condition
// evidence.
//
// The statement is hand-written rather than generated because sqlc v1.31.1
// cannot parse SQLite table-valued functions such as json_each. Its shape is
// fixed, so the JSON array parameter is the only variable input.
const getEntityStateSnapshotSQL = `
SELECT
    requested.value       AS requested_entity_id,
    e.id                  AS entity_id,
    s.observation_id      AS observation_id,
    s.value_json          AS value_json,
    s.adapter_received_at AS adapter_received_at,
    s.source_updated_at   AS source_updated_at,
    s.observed_at         AS observed_at,
    s.receive_order       AS receive_order,
    o.entity_id           AS backing_entity_id,
    o.disposition         AS backing_disposition,
    o.state_value_json    AS backing_value_json,
    o.adapter_received_at AS backing_adapter_received_at,
    o.source_updated_at   AS backing_source_updated_at,
    o.observed_at         AS backing_observed_at
FROM json_each(CAST(? AS TEXT)) AS requested
LEFT JOIN entities AS e ON e.id = requested.value
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN observations AS o
    ON o.observation_id = s.observation_id
    AND o.receive_order = s.receive_order
ORDER BY requested.key`

// GetEntityStateSnapshot implements devices.ReadRepository over one statement,
// so every requested Entity is read from one database snapshot without a
// cross-module transaction. An empty request returns an empty snapshot without
// SQL. Every other failure, including corrupt or unbacked stored State, fails
// the whole read so a caller never evaluates a partial batch.
func (repository *DeviceRepository) GetEntityStateSnapshot(
	ctx context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	if len(ids) == 0 {
		return devices.EntityStateSnapshot{
			Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{},
		}, nil
	}
	requested, err := marshalEntityStateSnapshotIDs(ids)
	if err != nil {
		return devices.EntityStateSnapshot{}, err
	}
	rows, err := repository.database.QueryContext(ctx, getEntityStateSnapshotSQL, requested)
	if err != nil {
		return devices.EntityStateSnapshot{}, fmt.Errorf("get entity state snapshot: %w", err)
	}
	defer rows.Close()

	entries := make(map[devices.EntityID]devices.EntityStateSnapshotEntry, len(ids))
	for rows.Next() {
		entry, scanErr := scanEntityStateSnapshotEntry(rows)
		if scanErr != nil {
			return devices.EntityStateSnapshot{}, scanErr
		}
		entries[entry.EntityID] = entry
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return devices.EntityStateSnapshot{}, fmt.Errorf("get entity state snapshot: %w", rowsErr)
	}
	return devices.EntityStateSnapshot{Entries: entries}, nil
}

// marshalEntityStateSnapshotIDs encodes the already-validated requested
// identities as one JSON array. EntityID is a string type, so encoding cannot
// fail for a value that reached persistence.
func marshalEntityStateSnapshotIDs(ids []devices.EntityID) (string, error) {
	encoded, err := json.Marshal(ids)
	if err != nil {
		return "", fmt.Errorf("encode entity state snapshot request: %w", err)
	}
	return string(encoded), nil
}

// materializedState is the entity_states row one requested Entity owns. Every
// column is optional in the result set because a requested ID with no accepted
// State still produces one row, and that row carries no State columns at all.
type materializedState struct {
	observationID     sql.NullString
	valueJSON         sql.NullString
	adapterReceivedAt sql.NullString
	sourceUpdatedAt   sql.NullString
	observedAt        sql.NullString
	receiveOrder      sql.NullInt64
}

// backingObservation is the Observations row one materialized State claims as
// its evidence. It is read in the same statement as the State, so the identity
// and the evidence it must match come from one coherent read. A State whose
// Observation identity and receive order do not name one row leaves every
// column NULL.
type backingObservation struct {
	entityID          sql.NullString
	disposition       sql.NullString
	valueJSON         sql.NullString
	adapterReceivedAt sql.NullString
	sourceUpdatedAt   sql.NullString
	observedAt        sql.NullString
}

// scanEntityStateSnapshotEntry maps one requested-ID row into owned domain
// evidence. A row with no Entity is requested-but-missing; a row with an Entity
// and no State columns is requested-but-never-observed; a row with an Entity and
// a complete State is that State. Partial State columns, a State that identifies
// a different Entity, a State without a canonical Observation identity, stored
// State bytes that are not valid JSON, and a State whose backing Observation row
// is missing or disagrees with it are unusable evidence and are reported as
// [devices.ErrEntityStateSnapshotCorrupt].
func scanEntityStateSnapshotEntry(rows *sql.Rows) (devices.EntityStateSnapshotEntry, error) {
	var (
		requestedEntityID sql.NullString
		entityID          sql.NullString
		materialized      materializedState
		backing           backingObservation
	)
	if err := rows.Scan(
		&requestedEntityID,
		&entityID,
		&materialized.observationID,
		&materialized.valueJSON,
		&materialized.adapterReceivedAt,
		&materialized.sourceUpdatedAt,
		&materialized.observedAt,
		&materialized.receiveOrder,
		&backing.entityID,
		&backing.disposition,
		&backing.valueJSON,
		&backing.adapterReceivedAt,
		&backing.sourceUpdatedAt,
		&backing.observedAt,
	); err != nil {
		return devices.EntityStateSnapshotEntry{}, fmt.Errorf("scan entity state snapshot: %w", err)
	}
	if !requestedEntityID.Valid {
		return devices.EntityStateSnapshotEntry{}, fmt.Errorf(
			"%w: snapshot row has no requested entity ID", devices.ErrEntityStateSnapshotCorrupt,
		)
	}
	requested := devices.EntityID(requestedEntityID.String)
	entry := devices.EntityStateSnapshotEntry{EntityID: requested}
	if !entityID.Valid {
		return entry, nil
	}
	if devices.EntityID(entityID.String) != requested {
		return devices.EntityStateSnapshotEntry{}, fmt.Errorf(
			"%w: snapshot row for entity %q returned entity %q",
			devices.ErrEntityStateSnapshotCorrupt, requested, entityID.String,
		)
	}
	entry.Exists = true
	state, stateErr := buildEntityStateSnapshotState(requested, materialized, backing)
	if stateErr != nil {
		return devices.EntityStateSnapshotEntry{}, stateErr
	}
	entry.State = state
	return entry, nil
}

// buildEntityStateSnapshotState turns one Entity's materialized State columns
// and its backing Observation columns into owned State evidence. It returns a
// nil State for an Entity that exists but has never been observed, and
// [devices.ErrEntityStateSnapshotCorrupt] for any State whose stored columns are
// partial, unparseable, or not backed by the accepted Observation row the State
// names.
func buildEntityStateSnapshotState(
	entityID devices.EntityID,
	materialized materializedState,
	backing backingObservation,
) (*devices.State, error) {
	if !materialized.observationID.Valid {
		if materialized.valueJSON.Valid || materialized.adapterReceivedAt.Valid ||
			materialized.sourceUpdatedAt.Valid || materialized.observedAt.Valid || materialized.receiveOrder.Valid {
			return nil, fmt.Errorf(
				"%w: entity %q has partial State without observation identity",
				devices.ErrEntityStateSnapshotCorrupt, entityID,
			)
		}
		return nil, nil //nolint:nilnil // No accepted State is covered negative evidence, not an error.
	}
	if !materialized.valueJSON.Valid || !materialized.adapterReceivedAt.Valid ||
		!materialized.observedAt.Valid || !materialized.receiveOrder.Valid {
		return nil, fmt.Errorf(
			"%w: entity %q has incomplete State", devices.ErrEntityStateSnapshotCorrupt, entityID,
		)
	}
	if !json.Valid([]byte(materialized.valueJSON.String)) {
		return nil, fmt.Errorf(
			"%w: entity %q has invalid stored State JSON", devices.ErrEntityStateSnapshotCorrupt, entityID,
		)
	}
	if _, err := devices.ParseObservationID(materialized.observationID.String); err != nil {
		return nil, fmt.Errorf(
			"%w: entity %q State Observation ID is not canonical",
			devices.ErrEntityStateSnapshotCorrupt, entityID,
		)
	}
	adapterTime, err := parseTime(materialized.adapterReceivedAt.String)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: entity %q State adapter_received_at: %w", devices.ErrEntityStateSnapshotCorrupt, entityID, err,
		)
	}
	observedTime, err := parseTime(materialized.observedAt.String)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: entity %q State observed_at: %w", devices.ErrEntityStateSnapshotCorrupt, entityID, err,
		)
	}
	sourceTime, err := parseOptionalTime(materialized.sourceUpdatedAt)
	if err != nil {
		return nil, fmt.Errorf(
			"%w: entity %q State source_updated_at: %w", devices.ErrEntityStateSnapshotCorrupt, entityID, err,
		)
	}
	state := devices.State{
		EntityID:          entityID,
		Value:             devices.Value(materialized.valueJSON.String),
		ObservationID:     devices.ObservationID(materialized.observationID.String),
		AdapterReceivedAt: adapterTime,
		SourceUpdatedAt:   sourceTime,
		ObservedAt:        observedTime,
		ReceiveOrder:      materialized.receiveOrder.Int64,
	}
	if backingErr := validateBackingObservation(state, backing); backingErr != nil {
		return nil, backingErr
	}
	return &state, nil
}

// validateBackingObservation proves a materialized State is the same evidence
// as the accepted Observation row it names. entity_states stores the canonical
// Observation identity and the receive order as separate foreign keys, so each
// is valid on its own even when they describe different Observations; the
// snapshot join already requires both to name one row, and this check rejects a
// State whose one joined row belongs to another Entity, was rejected rather than
// accepted, or does not repeat the retained value and timestamps the State
// claims. Any disagreement makes the State untrustworthy evidence and fails the
// whole read as [devices.ErrEntityStateSnapshotCorrupt].
func validateBackingObservation(state devices.State, backing backingObservation) error {
	if !backing.entityID.Valid {
		return fmt.Errorf(
			"%w: entity %q State names Observation %q, which no backing row matches",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID,
		)
	}
	if devices.EntityID(backing.entityID.String) != state.EntityID {
		return fmt.Errorf(
			"%w: entity %q State Observation %q belongs to entity %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID, backing.entityID.String,
		)
	}
	disposition := devices.ObservationDisposition(backing.disposition.String)
	if disposition != devices.DispositionApplied && disposition != devices.DispositionUnchanged {
		return fmt.Errorf(
			"%w: entity %q State backs onto %s Observation %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, backing.disposition.String, state.ObservationID,
		)
	}
	if backing.valueJSON.String != string(state.Value) {
		return fmt.Errorf(
			"%w: entity %q State value disagrees with Observation %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID,
		)
	}
	backingReceivedAt, err := parseTime(backing.adapterReceivedAt.String)
	if err != nil {
		return fmt.Errorf(
			"%w: entity %q Observation %q adapter_received_at: %w",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID, err,
		)
	}
	if !backingReceivedAt.Equal(state.AdapterReceivedAt) {
		return fmt.Errorf(
			"%w: entity %q State adapter_received_at disagrees with Observation %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID,
		)
	}
	backingObservedAt, err := parseTime(backing.observedAt.String)
	if err != nil {
		return fmt.Errorf(
			"%w: entity %q Observation %q observed_at: %w",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID, err,
		)
	}
	if !backingObservedAt.Equal(state.ObservedAt) {
		return fmt.Errorf(
			"%w: entity %q State observed_at disagrees with Observation %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID,
		)
	}
	backingSourceUpdatedAt, err := parseOptionalTime(backing.sourceUpdatedAt)
	if err != nil {
		return fmt.Errorf(
			"%w: entity %q Observation %q source_updated_at: %w",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID, err,
		)
	}
	if !sameOptionalTime(state.SourceUpdatedAt, backingSourceUpdatedAt) {
		return fmt.Errorf(
			"%w: entity %q State source_updated_at disagrees with Observation %q",
			devices.ErrEntityStateSnapshotCorrupt, state.EntityID, state.ObservationID,
		)
	}
	return nil
}

// sameOptionalTime compares two optional instants by value. A State and its
// backing Observation must agree about whether an upstream change time exists
// and, when it does, name the same instant.
func sameOptionalTime(left *time.Time, right *time.Time) bool {
	if left == nil || right == nil {
		return left == nil && right == nil
	}
	return left.Equal(*right)
}
