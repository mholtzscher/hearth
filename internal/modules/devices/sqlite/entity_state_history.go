package sqlite

import (
	"context"
	"fmt"
	"math"

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
) ([]dbsqlc.Observation, error) {
	entityID := string(params.EntityID)
	before := int64(math.MaxInt64)
	if params.BeforeReceiveOrder != nil {
		before = *params.BeforeReceiveOrder
	}
	limit := int64(params.Limit + 1)
	var rows []dbsqlc.Observation
	var err error
	switch filter := params.Filter; filter {
	case devices.EntityStateHistoryFilterAll:
		rows, err = repository.queries.ListEntityStateHistory(ctx, dbsqlc.ListEntityStateHistoryParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
	case "", devices.EntityStateHistoryFilterUpdates:
		rows, err = repository.queries.ListEntityStateUpdates(ctx, dbsqlc.ListEntityStateUpdatesParams{
			EntityID: entityID, ReceiveOrder: before, Limit: limit,
		})
	case devices.EntityStateHistoryFilterApplied,
		devices.EntityStateHistoryFilterUnchanged,
		devices.EntityStateHistoryFilterRejected:
		rows, err = repository.queries.ListEntityStateHistoryByDisposition(
			ctx,
			dbsqlc.ListEntityStateHistoryByDispositionParams{
				EntityID: entityID, Disposition: string(filter), ReceiveOrder: before, Limit: limit,
			},
		)
	default:
		return nil, fmt.Errorf("%w: unknown State history filter %q", devices.ErrInvalidPage, filter)
	}
	if err != nil {
		return nil, fmt.Errorf("list entity State history: %w", err)
	}
	return rows, nil
}
