package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

// upsertObservationRollups adds an accepted (applied or unchanged) report to
// the cell of every catalogued numeric field of its Entity's type; rejected
// reports change no cell. It runs inside the Observation's projection
// transaction, so cells and evidence commit together.
func upsertObservationRollups(
	ctx context.Context,
	queries *dbsqlc.Queries,
	entity devices.Entity,
	disposition devices.ObservationDisposition,
	value devices.Value,
	observedAt time.Time,
) error {
	if disposition != devices.DispositionApplied && disposition != devices.DispositionUnchanged {
		return nil
	}
	cellStart := historyRollupCellStart(observedAt)
	for _, field := range devices.HistoryRollupFields(entity.TypeID) {
		stats := new(numstats.Stats)
		row, readErr := queries.ObservationRollupCell(ctx, dbsqlc.ObservationRollupCellParams{
			EntityID: string(entity.ID), FieldPath: field, CellStart: cellStart,
		})
		if readErr == nil {
			var err error
			stats, err = historyRollupFromRow(row)
			if err != nil {
				return err
			}
		} else if !errors.Is(readErr, sql.ErrNoRows) {
			return fmt.Errorf("read observation rollup cell: %w", readErr)
		}
		sample, err := numstats.Select(json.RawMessage(value), field)
		if err != nil {
			return fmt.Errorf("classify history rollup sample: %w", err)
		}
		stats.Add(sample)
		if err = queries.UpsertObservationRollup(
			ctx, observationRollupParams(entity.ID, field, cellStart, stats),
		); err != nil {
			return fmt.Errorf("upsert observation rollup: %w", err)
		}
	}
	return nil
}

// DeleteExpiredObservationRollups deletes whole cells whose end is at or
// before the supplied cutoff. The cell containing a fractional cutoff stays.
func (repository *DeviceRepository) DeleteExpiredObservationRollups(ctx context.Context, before time.Time) error {
	_, err := repository.queries.DeleteExpiredObservationRollups(ctx, dbsqlc.DeleteExpiredObservationRollupsParams{
		LastExpiredStart: before.Unix() - int64(devices.HistoryRollupCellDuration/time.Second),
	})
	if err != nil {
		return fmt.Errorf("delete expired observation rollups: %w", err)
	}
	return nil
}

// validateHistoryRollupRead checks structural reader input independently of
// the service: grid-aligned bounds and a whole-cell bucket width.
func validateHistoryRollupRead(request devices.HistoryRollupRequest) error {
	invalid := &devices.HistoryQueryError{Code: historyInvalidQuery}
	if _, err := devices.ParseEntityID(string(request.EntityID)); err != nil {
		return invalid
	}
	aligned := func(at time.Time) bool {
		return at.Location() == time.UTC && at.Nanosecond() == 0 &&
			at.Unix()%int64(devices.HistoryRollupCellDuration/time.Second) == 0
	}
	if !aligned(request.From) || !aligned(request.End) || !request.From.Before(request.End) ||
		request.Bucket < 0 || request.Bucket%devices.HistoryRollupCellDuration != 0 {
		return invalid
	}
	return nil
}

// ReadHistoryRollupBuckets groups one field's cells in [From, End) into
// buckets of the requested width from one read snapshot.
func (repository *DeviceRepository) ReadHistoryRollupBuckets(
	ctx context.Context,
	request devices.HistoryRollupRequest,
) ([]devices.HistoryRollupBucket, error) {
	if err := validateHistoryRollupRead(request); err != nil {
		return nil, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin history rollup read: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback after commit or failure is best-effort.
	queries := repository.queries.WithTx(tx)
	typeID, err := queries.HistoryEntityType(ctx, dbsqlc.HistoryEntityTypeParams{ID: string(request.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, devices.ErrEntityNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("read history entity: %w", err)
	}
	stateless, err := repository.catalog.IsStateless(devices.EntityTypeID(typeID))
	if err != nil {
		return nil, fmt.Errorf("classify history entity: %w", err)
	}
	if stateless {
		return nil, &devices.HistoryQueryError{Code: "unsupported_source"}
	}
	if !slices.Contains(devices.HistoryRollupFields(devices.EntityTypeID(typeID)), request.Field) {
		return nil, &devices.HistoryQueryError{Code: historyInvalidQuery, Field: historySelectField}
	}
	rows, err := queries.HistoryRollupCells(ctx, dbsqlc.HistoryRollupCellsParams{
		EntityID:  string(request.EntityID),
		FieldPath: request.Field,
		FromUnix:  request.From.Unix(),
		EndUnix:   request.End.Unix(),
	})
	if err != nil {
		return nil, fmt.Errorf("read history rollups: %w", err)
	}
	var buckets []devices.HistoryRollupBucket
	for _, row := range rows {
		stats, mapErr := historyRollupCellFromRow(row)
		if mapErr != nil {
			return nil, mapErr
		}
		index := 0
		if request.Bucket > 0 {
			index = int((row.CellStart - request.From.Unix()) / int64(request.Bucket/time.Second))
		}
		if len(buckets) == 0 || buckets[len(buckets)-1].Index != index {
			buckets = append(buckets, devices.HistoryRollupBucket{Index: index})
		}
		buckets[len(buckets)-1].Merge(stats)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit history rollup read: %w", err)
	}
	return buckets, nil
}
