package sqlite

import (
	"context"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// ListEntityStateHistory returns one keyset page of an Entity's retained
// first-seen observations, newest-first by receive order. The caller
// validates the Entity, filter, cursor, and limit; the adapter owns query
// selection, limit+1 truncation, row mapping, and error wrapping.
func (repository *DeviceRepository) ListEntityStateHistory(
	ctx context.Context,
	params devices.ListEntityStateHistoryParams,
) (devices.Page[devices.EntityStateHistoryEntry], error) {
	if !devices.ValidPageLimit(params.Limit) {
		return devices.Page[devices.EntityStateHistoryEntry]{}, devices.ErrInvalidPage
	}
	rows, err := repository.entityStateHistoryRows(ctx, params)
	if err != nil {
		return devices.Page[devices.EntityStateHistoryEntry]{}, err
	}
	items := make([]devices.EntityStateHistoryEntry, 0, len(rows))
	for _, row := range rows {
		entry, mappingErr := entityStateHistoryEntryFromRow(row)
		if mappingErr != nil {
			return devices.Page[devices.EntityStateHistoryEntry]{}, fmt.Errorf(
				"map entity State history: %w",
				mappingErr,
			)
		}
		items = append(items, entry)
	}
	return pageFromExtra(items, params.Limit), nil
}

func (repository *DeviceRepository) entityStateHistoryRows(
	ctx context.Context,
	params devices.ListEntityStateHistoryParams,
) ([]entityStateHistoryRow, error) {
	queries := repository.queries
	limit := int64(params.Limit + 1)
	entityID := string(params.EntityID)
	filter := params.Filter
	if filter == "" {
		filter = devices.EntityStateHistoryFilterUpdates
	}
	if params.BeforeReceiveOrder == nil {
		switch filter {
		case devices.EntityStateHistoryFilterAll:
			rows, err := queries.ListEntityStateHistoryFirstPage(ctx, dbsqlc.ListEntityStateHistoryFirstPageParams{
				EntityID: entityID, Limit: limit,
			})
			if err != nil {
				return nil, fmt.Errorf("list entity State history: %w", err)
			}
			return toEntityStateHistoryRowsHistoryFirstPage(rows), nil
		case devices.EntityStateHistoryFilterUpdates:
			rows, err := queries.ListEntityStateUpdatesFirstPage(ctx, dbsqlc.ListEntityStateUpdatesFirstPageParams{
				EntityID: entityID, Limit: limit,
			})
			if err != nil {
				return nil, fmt.Errorf("list entity State history: %w", err)
			}
			return toEntityStateHistoryRowsUpdatesFirstPage(rows), nil
		case devices.EntityStateHistoryFilterApplied,
			devices.EntityStateHistoryFilterUnchanged,
			devices.EntityStateHistoryFilterRejected:
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
			return nil, fmt.Errorf("%w: unknown State history filter %q", devices.ErrInvalidPage, params.Filter)
		}
	}
	before := *params.BeforeReceiveOrder
	switch filter {
	case devices.EntityStateHistoryFilterAll:
		rows, err := queries.ListEntityStateHistoryAfter(ctx, dbsqlc.ListEntityStateHistoryAfterParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list entity State history: %w", err)
		}
		return toEntityStateHistoryRowsHistoryAfter(rows), nil
	case devices.EntityStateHistoryFilterUpdates:
		rows, err := queries.ListEntityStateUpdatesAfter(ctx, dbsqlc.ListEntityStateUpdatesAfterParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
		if err != nil {
			return nil, fmt.Errorf("list entity State history: %w", err)
		}
		return toEntityStateHistoryRowsUpdatesAfter(rows), nil
	case devices.EntityStateHistoryFilterApplied,
		devices.EntityStateHistoryFilterUnchanged,
		devices.EntityStateHistoryFilterRejected:
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
		return nil, fmt.Errorf("%w: unknown State history filter %q", devices.ErrInvalidPage, params.Filter)
	}
}
