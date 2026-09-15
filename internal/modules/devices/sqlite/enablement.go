package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) SetEntityEnabled(
	ctx context.Context,
	params devices.SetEntityEnabledParams,
) (devices.EntityWithState, error) {
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("begin entity enablement update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	if params.RequiredRuntime != nil {
		if params.RequiredOwner == nil {
			return devices.EntityWithState{}, errors.New("runtime-scoped enablement requires an Adapter owner")
		}
		_, runtimeErr := queries.GetActiveAdapterRuntime(ctx, dbsqlc.GetActiveAdapterRuntimeParams{
			AdapterID: *params.RequiredOwner, RuntimeID: string(*params.RequiredRuntime),
		})
		if errors.Is(runtimeErr, sql.ErrNoRows) {
			return devices.EntityWithState{}, devices.ErrRuntimeFenced
		}
		if runtimeErr != nil {
			return devices.EntityWithState{}, fmt.Errorf("validate entity enablement runtime: %w", runtimeErr)
		}
	}
	row, err := queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.EntityID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.EntityWithState{}, devices.ErrEntityNotFound
	}
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("get entity for enablement update: %w", err)
	}
	if params.RequiredOwner != nil && row.AdapterID != *params.RequiredOwner {
		return devices.EntityWithState{}, devices.ErrEntityWrongAdapter
	}
	if row.Enabled != boolToInt64(params.Enabled) {
		if _, updateErr := queries.UpdateEntityEnablement(ctx, dbsqlc.UpdateEntityEnablementParams{
			Enabled: boolToInt64(params.Enabled), UpdatedAt: formatTime(params.UpdatedAt),
			ID: string(params.EntityID),
		}); updateErr != nil {
			return devices.EntityWithState{}, fmt.Errorf("update entity enablement: %w", updateErr)
		}
		row, err = queries.GetEntity(ctx, dbsqlc.GetEntityParams{ID: string(params.EntityID)})
		if err != nil {
			return devices.EntityWithState{}, fmt.Errorf("get updated entity: %w", err)
		}
	}
	view, err := entityWithStateFromRow(row)
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("map updated entity: %w", err)
	}
	if commitErr := tx.Commit(); commitErr != nil {
		return devices.EntityWithState{}, fmt.Errorf("commit entity enablement update: %w", commitErr)
	}
	return devices.CopyEntityWithState(view), nil
}
