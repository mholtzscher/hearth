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

const historyBatchSize = 64

// validateHistoryRead checks structural reader input. The service owns the
// evaluation-time bound using its injected clock; the reader has no such time.
func validateHistoryRead(request devices.HistoryReadRequest) error {
	invalid := &devices.HistoryQueryError{Code: "invalid_query"}
	if request.SourceRowLimit < 0 || request.SourceRowLimit > devices.HistoryMaxSourceRows {
		return invalid
	}
	if _, err := devices.ParseEntityID(string(request.EntityID)); err != nil {
		return invalid
	}
	validTime := func(t time.Time) bool {
		return !t.IsZero() && t.Location() == time.UTC && t.Year() >= 1 && t.Year() <= 9999
	}
	if !validTime(request.Range.From) || !validTime(request.Range.To) || !request.Range.From.Before(request.Range.To) ||
		(request.Order != devices.HistoryAscending && request.Order != devices.HistoryDescending) {
		return invalid
	}
	if request.Cursor != nil {
		c := request.Cursor
		if request.IncludeSeed || !validTime(c.ObservedAt) || c.ObservedAt.Before(request.Range.From) ||
			!c.ObservedAt.Before(request.Range.To) ||
			c.ReceiveOrder <= 0 ||
			c.HighWater < c.ReceiveOrder {
			return invalid
		}
	}
	if request.IncludeSeed && request.Order != devices.HistoryAscending {
		return invalid
	}
	return nil
}

// ReadHistoryWindow validates direct callers and copies a bounded prefix from one read snapshot.
func (repository *DeviceRepository) ReadHistoryWindow(
	ctx context.Context,
	request devices.HistoryReadRequest,
) (devices.HistoryWindow, error) {
	if err := validateHistoryRead(request); err != nil {
		return devices.HistoryWindow{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return devices.HistoryWindow{}, fmt.Errorf("begin history read: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck // Rollback after commit or failure is best-effort.
	q := repository.queries.WithTx(tx)
	typeID, err := q.HistoryEntityType(ctx, dbsqlc.HistoryEntityTypeParams{ID: string(request.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.HistoryWindow{}, devices.ErrEntityNotFound
	}
	if err != nil {
		return devices.HistoryWindow{}, fmt.Errorf("read history entity: %w", err)
	}
	stateless, err := repository.catalog.IsStateless(devices.EntityTypeID(typeID))
	if err != nil {
		return devices.HistoryWindow{}, fmt.Errorf("classify history entity: %w", err)
	}
	if stateless {
		return devices.HistoryWindow{}, &devices.HistoryQueryError{Code: "unsupported_source"}
	}
	result := devices.HistoryWindow{Rows: make([]devices.EntityStateHistoryEntry, 0)}
	if request.Cursor == nil {
		result.HighWater, err = q.HistoryHighWater(
			ctx,
			dbsqlc.HistoryHighWaterParams{EntityID: string(request.EntityID)},
		)
		if err != nil {
			return devices.HistoryWindow{}, fmt.Errorf("read history high water: %w", err)
		}
	} else {
		result.HighWater = request.Cursor.HighWater
	}
	if err = historyScan(ctx, q, request, &result); err != nil {
		return devices.HistoryWindow{}, err
	}
	if err = tx.Commit(); err != nil {
		return devices.HistoryWindow{}, fmt.Errorf("commit history read: %w", err)
	}
	return result, nil
}

//nolint:gocognit // The ordered scan keeps its row and byte budgets together.
func historyScan(
	ctx context.Context,
	q *dbsqlc.Queries,
	request devices.HistoryReadRequest,
	result *devices.HistoryWindow,
) error {
	count, bytes := 0, int64(0)
	rowLimit := request.SourceRowLimit
	if rowLimit == 0 {
		rowLimit = devices.HistoryMaxSourceRows
	}
	if request.IncludeSeed {
		seedBytes, err := historyLoadSeed(ctx, q, request, result)
		if err != nil {
			return err
		}
		if result.Seed != nil {
			count++
			bytes = seedBytes
		}
	}
	positionTime, positionOrder := formatTime(request.Range.From), int64(0)
	if request.Order == devices.HistoryDescending {
		positionTime = formatTime(request.Range.To)
	}
	if request.Cursor != nil {
		positionTime, positionOrder = formatTime(request.Cursor.ObservedAt), request.Cursor.ReceiveOrder
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		rows, batchErr := historyQueryBatch(ctx, q, request, positionTime, positionOrder, result.HighWater)
		if batchErr != nil {
			return batchErr
		}
		for _, row := range rows {
			if count >= rowLimit || row.ValueBytes > int64(devices.HistoryMaxSourceBytes)-bytes {
				if count == 0 {
					return &devices.HistoryQueryError{Code: "query_limit"}
				}
				result.HasMore = true
				return nil
			}
			entry, mapErr := historyLoadEntry(ctx, q, row)
			if mapErr != nil {
				return mapErr
			}
			result.Rows = append(result.Rows, entry)
			count++
			bytes += row.ValueBytes
			positionTime, positionOrder = row.ObservedAt, row.ReceiveOrder
		}
		if len(rows) < historyBatchSize {
			break
		}
	}
	return nil
}

func historyLoadSeed(
	ctx context.Context,
	q *dbsqlc.Queries,
	request devices.HistoryReadRequest,
	result *devices.HistoryWindow,
) (int64, error) {
	seed, err := q.HistorySeed(
		ctx,
		dbsqlc.HistorySeedParams{
			EntityID:   string(request.EntityID),
			BeforeTime: formatTime(request.Range.From),
			HighWater:  result.HighWater,
		},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read history seed: %w", err)
	}
	if seed.ValueBytes > devices.HistoryMaxSourceBytes {
		return 0, &devices.HistoryQueryError{Code: "query_limit"}
	}
	entry, err := historyLoadEntry(ctx, q, historyQueryRow(seed))
	if err != nil {
		return 0, err
	}
	result.Seed = &entry
	return seed.ValueBytes, nil
}

// historyLoadEntry is called only after the row's JSON byte budget is admitted.
func historyLoadEntry(
	ctx context.Context,
	q *dbsqlc.Queries,
	row historyQueryRow,
) (devices.EntityStateHistoryEntry, error) {
	value, err := q.HistoryValue(ctx, dbsqlc.HistoryValueParams{ReceiveOrder: row.ReceiveOrder})
	if err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("read retained history value: %w", err)
	}
	return historyQueryEntryFromRow(row, value)
}

func historyQueryBatch(
	ctx context.Context,
	q *dbsqlc.Queries,
	request devices.HistoryReadRequest,
	positionTime string,
	positionOrder, highWater int64,
) ([]historyQueryRow, error) {
	var rows []historyQueryRow
	if request.Order == devices.HistoryAscending {
		batch, batchErr := q.HistoryAscendingRows(
			ctx,
			dbsqlc.HistoryAscendingRowsParams{
				EntityID:      string(request.EntityID),
				FromTime:      formatTime(request.Range.From),
				ToTime:        formatTime(request.Range.To),
				PositionTime:  positionTime,
				PositionOrder: positionOrder,
				HighWater:     highWater,
				BatchLimit:    historyBatchSize,
			},
		)
		if batchErr != nil {
			return nil, fmt.Errorf("read history rows: %w", batchErr)
		}
		for _, row := range batch {
			rows = append(rows, historyQueryRow(row))
		}
	} else {
		batch, batchErr := q.HistoryDescendingRows(
			ctx,
			dbsqlc.HistoryDescendingRowsParams{
				EntityID:      string(request.EntityID),
				FromTime:      formatTime(request.Range.From),
				ToTime:        formatTime(request.Range.To),
				PositionTime:  positionTime,
				PositionOrder: positionOrder,
				HighWater:     highWater,
				BatchLimit:    historyBatchSize,
			},
		)
		if batchErr != nil {
			return nil, fmt.Errorf("read history rows: %w", batchErr)
		}
		for _, row := range batch {
			rows = append(rows, historyQueryRow(row))
		}
	}
	return rows, nil
}
