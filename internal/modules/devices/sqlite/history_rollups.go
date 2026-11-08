package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
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
		sample, err := devices.ClassifyHistoryRollupSample(value, field)
		if err != nil {
			return fmt.Errorf("classify history rollup sample: %w", err)
		}
		if err = queries.UpsertObservationRollup(
			ctx, observationRollupParams(entity.ID, field, cellStart, sample),
		); err != nil {
			return fmt.Errorf("upsert observation rollup: %w", err)
		}
	}
	return nil
}

// validateHistoryRollupRead checks structural reader input independently of
// the service: grid-aligned bounds and a whole-cell bucket width.
func validateHistoryRollupRead(request devices.HistoryRollupRequest) error {
	invalid := &devices.HistoryQueryError{Code: "invalid_query"}
	if _, err := devices.ParseEntityID(string(request.EntityID)); err != nil {
		return invalid
	}
	aligned := func(at time.Time) bool {
		return at.Location() == time.UTC && at.Nanosecond() == 0 &&
			at.Unix()%int64(devices.HistoryRollupCellDuration/time.Second) == 0
	}
	if !aligned(request.From) || !aligned(request.End) || !request.From.Before(request.End) ||
		request.Bucket <= 0 || request.Bucket%devices.HistoryRollupCellDuration != 0 {
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
		return nil, &devices.HistoryQueryError{Code: "invalid_query", Field: "select_field"}
	}
	rows, err := queries.HistoryRollupBuckets(ctx, dbsqlc.HistoryRollupBucketsParams{
		EntityID:      string(request.EntityID),
		FieldPath:     request.Field,
		FromUnix:      request.From.Unix(),
		EndUnix:       request.End.Unix(),
		BucketSeconds: int64(request.Bucket / time.Second),
	})
	if err != nil {
		return nil, fmt.Errorf("read history rollups: %w", err)
	}
	buckets := make([]devices.HistoryRollupBucket, 0, len(rows))
	for _, row := range rows {
		bucket, mapErr := historyRollupBucketFromRow(row)
		if mapErr != nil {
			return nil, mapErr
		}
		buckets = append(buckets, bucket)
	}
	if err = tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit history rollup read: %w", err)
	}
	return buckets, nil
}
