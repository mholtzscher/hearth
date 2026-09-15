package sqlite

import (
	"context"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) ListOwnedMappings(
	ctx context.Context,
	params devices.ListOwnedMappingsParams,
) (devices.Page[devices.OwnedMapping], error) {
	afterBindingKey := ""
	afterEntityKey := ""
	hasAfter := int64(0)
	if params.After != nil {
		afterBindingKey = params.After.BindingKey
		afterEntityKey = params.After.EntityKey
		hasAfter = 1
	}
	rows, err := repository.queries.ListOwnedMappings(ctx, dbsqlc.ListOwnedMappingsParams{
		AdapterID:       params.AdapterID,
		HasAfter:        hasAfter,
		AfterBindingKey: afterBindingKey,
		AfterEntityKey:  afterEntityKey,
		ResultLimit:     int64(params.Limit + 1),
	})
	if err != nil {
		return devices.Page[devices.OwnedMapping]{}, fmt.Errorf("list owned mappings: %w", err)
	}
	items := make([]devices.OwnedMapping, len(rows))
	for index, row := range rows {
		items[index] = devices.OwnedMapping{
			BindingKey: row.BindingKey,
			DeviceID:   devices.DeviceID(row.DeviceID),
			EntityKey:  row.EntityKey,
			EntityID:   devices.EntityID(row.EntityID),
		}
	}
	return pageFromExtra(items, params.Limit), nil
}
