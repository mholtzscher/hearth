package devices

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices/dbsqlc"
)

// ListEntityStateHistory returns one keyset page of an Entity's retained
// first-seen observations, newest-first by receive order. The caller
// validates the Entity, filter, cursor, and limit; the adapter owns query
// selection, limit+1 truncation, row mapping, and error wrapping.
func (repository *SQLiteRepository) ListEntityStateHistory(
	ctx context.Context,
	params ListEntityStateHistoryParams,
) (Page[EntityStateHistoryEntry], error) {
	if !validPageLimit(params.Limit) {
		return Page[EntityStateHistoryEntry]{}, ErrInvalidPage
	}
	rows, err := repository.entityStateHistoryRows(ctx, params)
	if err != nil {
		return Page[EntityStateHistoryEntry]{}, err
	}
	items := make([]EntityStateHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry, mappingErr := entityStateHistoryEntryFromRow(row)
		if mappingErr != nil {
			return Page[EntityStateHistoryEntry]{}, fmt.Errorf("map entity State history: %w", mappingErr)
		}
		items = append(items, entry)
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *SQLiteRepository) entityStateHistoryRows(
	ctx context.Context,
	params ListEntityStateHistoryParams,
) ([]entityStateHistoryRow, error) {
	queries := repository.queries
	limit := int64(params.Limit + 1)
	entityID := string(params.EntityID)
	filter := params.Filter
	if filter == "" {
		filter = EntityStateHistoryFilterUpdates
	}
	if params.BeforeReceiveOrder == nil {
		switch filter {
		case EntityStateHistoryFilterAll:
			rows, err := queries.ListEntityStateHistoryFirstPage(ctx, dbsqlc.ListEntityStateHistoryFirstPageParams{
				EntityID: entityID, Limit: limit,
			})
			if err != nil {
				return nil, fmt.Errorf("list entity State history: %w", err)
			}
			return toEntityStateHistoryRowsHistoryFirstPage(rows), nil
		case EntityStateHistoryFilterUpdates:
			rows, err := queries.ListEntityStateUpdatesFirstPage(ctx, dbsqlc.ListEntityStateUpdatesFirstPageParams{
				EntityID: entityID, Limit: limit,
			})
			if err != nil {
				return nil, fmt.Errorf("list entity State history: %w", err)
			}
			return toEntityStateHistoryRowsUpdatesFirstPage(rows), nil
		case EntityStateHistoryFilterApplied,
			EntityStateHistoryFilterUnchanged,
			EntityStateHistoryFilterRejected:
			rows, err := queries.ListEntityStateHistoryByDispositionFirstPage(
				ctx,
				dbsqlc.ListEntityStateHistoryByDispositionFirstPageParams{
					EntityID: entityID, Disposition: string(filter), Limit: limit,
				},
			)
			if err != nil {
				return nil, fmt.Errorf("list entity State history: %w", err)
			}
			return toEntityStateHistoryRowsDispositionFirstPage(rows), nil
		default:
			return nil, fmt.Errorf("%w: unknown State history filter %q", ErrInvalidPage, params.Filter)
		}
	}
	before := *params.BeforeReceiveOrder
	switch filter {
	case EntityStateHistoryFilterAll:
		rows, err := queries.ListEntityStateHistoryAfter(ctx, dbsqlc.ListEntityStateHistoryAfterParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list entity State history: %w", err)
		}
		return toEntityStateHistoryRowsHistoryAfter(rows), nil
	case EntityStateHistoryFilterUpdates:
		rows, err := queries.ListEntityStateUpdatesAfter(ctx, dbsqlc.ListEntityStateUpdatesAfterParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list entity State history: %w", err)
		}
		return toEntityStateHistoryRowsUpdatesAfter(rows), nil
	case EntityStateHistoryFilterApplied,
		EntityStateHistoryFilterUnchanged,
		EntityStateHistoryFilterRejected:
		rows, err := queries.ListEntityStateHistoryByDispositionAfter(
			ctx,
			dbsqlc.ListEntityStateHistoryByDispositionAfterParams{
				EntityID: entityID, Disposition: string(filter), ReceiveOrder: before, Limit: limit,
			},
		)
		if err != nil {
			return nil, fmt.Errorf("list entity State history: %w", err)
		}
		return toEntityStateHistoryRowsDispositionAfter(rows), nil
	default:
		return nil, fmt.Errorf("%w: unknown State history filter %q", ErrInvalidPage, params.Filter)
	}
}

type entityStateHistoryRow struct {
	ObservationID     string
	StateValueJSON    sql.NullString
	Disposition       string
	RejectionCode     sql.NullString
	AdapterReceivedAt string
	SourceUpdatedAt   sql.NullString
	ObservedAt        string
	ReceiveOrder      int64
}

func entityStateHistoryEntryFromRow(row entityStateHistoryRow) (EntityStateHistoryEntry, error) {
	disposition := ObservationDisposition(row.Disposition)
	var value Value
	if row.StateValueJSON.Valid {
		value = Value(row.StateValueJSON.String)
	}
	if disposition == DispositionRejected && value != nil {
		return EntityStateHistoryEntry{}, fmt.Errorf("rejected State history row %q carries a value", row.ObservationID)
	}
	if disposition != DispositionRejected && value == nil {
		return EntityStateHistoryEntry{}, fmt.Errorf(
			"accepted State history row %q is missing its value",
			row.ObservationID,
		)
	}
	var rejection *ObservationRejection
	if row.RejectionCode.Valid {
		code := ObservationRejection(row.RejectionCode.String)
		rejection = &code
	}
	adapterReceivedAt, err := parseTime(row.AdapterReceivedAt)
	if err != nil {
		return EntityStateHistoryEntry{}, fmt.Errorf("parse State history adapter_received_at: %w", err)
	}
	sourceUpdatedAt, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return EntityStateHistoryEntry{}, fmt.Errorf("parse State history source_updated_at: %w", err)
	}
	observedAt, err := parseTime(row.ObservedAt)
	if err != nil {
		return EntityStateHistoryEntry{}, fmt.Errorf("parse State history observed_at: %w", err)
	}
	return EntityStateHistoryEntry{
		ObservationID:     ObservationID(row.ObservationID),
		Value:             value,
		Disposition:       disposition,
		Rejection:         rejection,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      row.ReceiveOrder,
	}, nil
}

func toEntityStateHistoryRowsUpdatesFirstPage(
	rows []dbsqlc.ListEntityStateUpdatesFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsUpdatesAfter(
	rows []dbsqlc.ListEntityStateUpdatesAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsHistoryFirstPage(
	rows []dbsqlc.ListEntityStateHistoryFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsHistoryAfter(
	rows []dbsqlc.ListEntityStateHistoryAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsDispositionFirstPage(
	rows []dbsqlc.ListEntityStateHistoryByDispositionFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsDispositionAfter(
	rows []dbsqlc.ListEntityStateHistoryByDispositionAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}
