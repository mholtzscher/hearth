package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func (repository *DeviceRepository) PatchDevice(
	ctx context.Context,
	params devices.PatchDeviceParams,
) (devices.Device, error) {
	if _, err := devices.ParseDeviceID(string(params.DeviceID)); err != nil {
		return devices.Device{}, fmt.Errorf("%w: %w", devices.ErrInvalidMetadataPatch, err)
	}
	patch, err := devices.PrepareMetadataPatch(nil, params.Patch.NameEdit)
	if err != nil {
		return devices.Device{}, err
	}
	if params.UpdatedAt.IsZero() {
		return devices.Device{}, devices.ErrInvalidMetadataPatch
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.Device{}, fmt.Errorf("begin device metadata: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	row, err := queries.GetDevice(ctx, dbsqlc.GetDeviceParams{ID: string(params.DeviceID)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.Device{}, devices.ErrDeviceNotFound
	}
	if err != nil {
		return devices.Device{}, fmt.Errorf("load device metadata: %w", err)
	}
	if override := nullableString(patch.NameEdit.Override); override != row.NameOverride {
		if err = queries.UpdateDeviceNameOverride(
			ctx,
			dbsqlc.UpdateDeviceNameOverrideParams{
				ID:           row.ID,
				NameOverride: override,
				UpdatedAt:    formatTime(params.UpdatedAt),
			},
		); err != nil {
			return devices.Device{}, fmt.Errorf("update device metadata: %w", err)
		}
		row, err = queries.GetDevice(ctx, dbsqlc.GetDeviceParams{ID: row.ID})
		if err != nil {
			return devices.Device{}, fmt.Errorf("load updated device metadata: %w", err)
		}
	}
	if err = tx.Commit(); err != nil {
		return devices.Device{}, fmt.Errorf("commit device metadata: %w", err)
	}
	return devices.Device{
		ID:           devices.DeviceID(row.ID),
		Kind:         devices.DeviceKind(row.Kind),
		Name:         row.Name,
		AdapterName:  row.AdapterName,
		NameOverride: nameOverrideFromSQL(row.NameOverride),
	}, nil
}
