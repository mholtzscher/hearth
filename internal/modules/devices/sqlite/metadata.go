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

func (repository *DeviceRepository) PatchEntity(
	ctx context.Context,
	params devices.PatchEntityParams,
) (devices.EntityWithState, error) {
	patch, err := devices.PrepareMetadataPatch(params.Patch.Enabled, params.Patch.NameEdit)
	if err != nil {
		return devices.EntityWithState{}, err
	}
	return repository.mutateEntity(ctx, params.EntityID, patch, params.UpdatedAt, nil, nil)
}

func (repository *DeviceRepository) mutateEntity(
	ctx context.Context,
	id devices.EntityID,
	patch devices.EntityPatch,
	updatedAt time.Time,
	owner *string,
	runtime *devices.RuntimeID,
) (devices.EntityWithState, error) {
	if _, err := devices.ParseEntityID(string(id)); err != nil {
		return devices.EntityWithState{}, fmt.Errorf("%w: %w", devices.ErrInvalidMetadataPatch, err)
	}
	if updatedAt.IsZero() {
		return devices.EntityWithState{}, devices.ErrInvalidMetadataPatch
	}
	params := devices.SetEntityEnabledParams{
		EntityID:        id,
		RequiredOwner:   owner,
		RequiredRuntime: runtime,
		UpdatedAt:       updatedAt,
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.EntityWithState{}, fmt.Errorf("begin entity enablement update: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	if err = checkMetadataRuntime(ctx, queries, owner, runtime); err != nil {
		return devices.EntityWithState{}, err
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
	changed := false
	if patch.Enabled != nil && row.Enabled != boolToInt64(*patch.Enabled) {
		if _, updateErr := queries.UpdateEntityEnablement(ctx, dbsqlc.UpdateEntityEnablementParams{
			Enabled: boolToInt64(*patch.Enabled), UpdatedAt: formatTime(params.UpdatedAt),
			ID: string(params.EntityID),
		}); updateErr != nil {
			return devices.EntityWithState{}, fmt.Errorf("update entity enablement: %w", updateErr)
		}
		changed = true
	}
	if entityNameChanged(row.NameOverride, patch.NameEdit) {
		if err = queries.UpdateEntityNameOverride(
			ctx,
			dbsqlc.UpdateEntityNameOverrideParams{
				ID:           string(id),
				NameOverride: nullableString(patch.NameEdit.Override),
				UpdatedAt:    formatTime(updatedAt),
			},
		); err != nil {
			return devices.EntityWithState{}, fmt.Errorf("update entity name: %w", err)
		}
		changed = true
	}
	if changed {
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

func entityNameChanged(current sql.NullString, edit *devices.NameEdit) bool {
	return edit != nil && current != nullableString(edit.Override)
}

func checkMetadataRuntime(
	ctx context.Context,
	queries *dbsqlc.Queries,
	owner *string,
	runtime *devices.RuntimeID,
) error {
	if runtime == nil {
		return nil
	}
	if owner == nil {
		return errors.New("runtime-scoped enablement requires an Adapter owner")
	}
	_, err := queries.GetActiveAdapterRuntime(
		ctx,
		dbsqlc.GetActiveAdapterRuntimeParams{AdapterID: *owner, RuntimeID: string(*runtime)},
	)
	if errors.Is(err, sql.ErrNoRows) {
		return devices.ErrRuntimeFenced
	}
	if err != nil {
		return fmt.Errorf("validate entity enablement runtime: %w", err)
	}
	return nil
}
