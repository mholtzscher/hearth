package sqlite

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/changevalue"
)

// History query error codes and fields shared by the SQLite history readers.
const (
	historyInvalidQuery = "invalid_query"
	historySelectField  = "select_field"
)

// insertStateChanges records, for an applied report, each catalogued yes/no
// or text field whose value differs from the Entity's previous State, or every
// such field when there was no previous State. It runs inside the
// Observation's projection transaction, so changes and evidence commit together.
func insertStateChanges(
	ctx context.Context,
	queries *dbsqlc.Queries,
	view devices.EntityWithState,
	disposition devices.ObservationDisposition,
	next devices.Value,
	observedAt time.Time,
	receiveOrder int64,
) error {
	if disposition != devices.DispositionApplied {
		return nil
	}
	for _, field := range devices.HistoryChangeFields(view.Entity.TypeID) {
		value, err := changevalue.Select(json.RawMessage(next), field)
		if err != nil {
			return err
		}
		if view.State != nil {
			previous, previousErr := changevalue.Select(json.RawMessage(view.State.Value), field)
			if previousErr != nil {
				return previousErr
			}
			if bytes.Equal(previous, value) {
				continue
			}
		}
		if err = queries.InsertStateChange(ctx, dbsqlc.InsertStateChangeParams{
			EntityID:     string(view.Entity.ID),
			FieldPath:    field,
			ObservedAt:   formatSortableTime(observedAt),
			ReceiveOrder: receiveOrder,
			ValueJson:    string(value),
		}); err != nil {
			return fmt.Errorf("insert state change: %w", err)
		}
	}
	return nil
}

// DeleteExpiredStateChanges deletes changes observed before the cutoff,
// keeping each field's newest change at or before it.
func (repository *DeviceRepository) DeleteExpiredStateChanges(ctx context.Context, before time.Time) error {
	if _, err := repository.queries.DeleteExpiredStateChanges(ctx, dbsqlc.DeleteExpiredStateChangesParams{
		Cutoff: formatSortableTime(before),
	}); err != nil {
		return fmt.Errorf("delete expired state changes: %w", err)
	}
	return nil
}

// ReadStateChanges validates the Entity and field, then reads the newest change
// before the range and every change inside it from one snapshot.
func (repository *DeviceRepository) ReadStateChanges(
	ctx context.Context,
	read devices.StateChangeRead,
) (devices.StateChangeRows, error) {
	if _, err := devices.ParseEntityID(string(read.EntityID)); err != nil || read.Limit < 1 ||
		!read.Range.From.Before(read.Range.To) {
		return devices.StateChangeRows{}, &devices.HistoryQueryError{Code: historyInvalidQuery}
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return devices.StateChangeRows{}, fmt.Errorf("begin state change read: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback after commit or failure is best-effort.
	queries := repository.queries.WithTx(tx)
	if err = repository.checkStateChangeSource(ctx, queries, read); err != nil {
		return devices.StateChangeRows{}, err
	}
	var result devices.StateChangeRows
	seed, err := queries.StateChangeSeed(ctx, dbsqlc.StateChangeSeedParams{
		EntityID: string(read.EntityID), FieldPath: read.Field, RangeFrom: formatSortableTime(read.Range.From),
	})
	switch {
	case err == nil:
		row, mapErr := stateChangeRow(seed.ObservedAt, seed.ValueJson)
		if mapErr != nil {
			return devices.StateChangeRows{}, mapErr
		}
		result.Seed = &row
	case !errors.Is(err, sql.ErrNoRows):
		return devices.StateChangeRows{}, fmt.Errorf("read state change seed: %w", err)
	}
	rows, err := queries.StateChangesInRange(ctx, dbsqlc.StateChangesInRangeParams{
		EntityID: string(read.EntityID), FieldPath: read.Field,
		RangeFrom: formatSortableTime(read.Range.From), RangeTo: formatSortableTime(read.Range.To),
		RowLimit: int64(read.Limit) + 1,
	})
	if err != nil {
		return devices.StateChangeRows{}, fmt.Errorf("read state changes: %w", err)
	}
	if len(rows) > read.Limit {
		return devices.StateChangeRows{}, &devices.HistoryQueryError{Code: "query_limit", Field: "range"}
	}
	result.Rows = make([]devices.StateChangeRow, 0, len(rows))
	for _, stored := range rows {
		row, mapErr := stateChangeRow(stored.ObservedAt, stored.ValueJson)
		if mapErr != nil {
			return devices.StateChangeRows{}, mapErr
		}
		result.Rows = append(result.Rows, row)
	}
	if err = tx.Commit(); err != nil {
		return devices.StateChangeRows{}, fmt.Errorf("commit state change read: %w", err)
	}
	return result, nil
}

// checkStateChangeSource rejects unknown and stateless Entities, fields that
// record no changes, and a text field where a yes/no field is required.
func (repository *DeviceRepository) checkStateChangeSource(
	ctx context.Context, queries *dbsqlc.Queries, read devices.StateChangeRead,
) error {
	typeID, err := queries.HistoryEntityType(ctx, dbsqlc.HistoryEntityTypeParams{ID: string(read.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.ErrEntityNotFound
	}
	if err != nil {
		return fmt.Errorf("read state change entity: %w", err)
	}
	entityType := devices.EntityTypeID(typeID)
	stateless, err := repository.catalog.IsStateless(entityType)
	if err != nil {
		return fmt.Errorf("classify state change entity: %w", err)
	}
	if stateless {
		return &devices.HistoryQueryError{Code: "unsupported_source"}
	}
	if !slices.Contains(devices.HistoryChangeFields(entityType), read.Field) {
		return &devices.HistoryQueryError{Code: historyInvalidQuery, Field: historySelectField}
	}
	if field, _ := devices.BuiltinStateField(entityType, read.Field); read.Boolean {
		if _, boolean := field.Value.(devices.BooleanField); !boolean {
			return &devices.HistoryQueryError{Code: "value_type", Field: historySelectField}
		}
	}
	return nil
}

// stateChangeRow maps a stored row; a malformed stored value is corrupt data,
// not caller input.
func stateChangeRow(observedAt, value string) (devices.StateChangeRow, error) {
	at, err := parseTime(observedAt)
	if err != nil {
		return devices.StateChangeRow{}, fmt.Errorf("decode state change time: %w", err)
	}
	if !json.Valid([]byte(value)) {
		return devices.StateChangeRow{}, errors.New("decode state change value: invalid JSON")
	}
	return devices.StateChangeRow{ObservedAt: at.UTC(), Value: json.RawMessage(value)}, nil
}
