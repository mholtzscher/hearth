package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

var _ devices.LabelRepository = (*DeviceRepository)(nil)

// CreateLabel checks the catalog bound and name uniqueness in the transaction
// that inserts the Label.
func (repository *DeviceRepository) CreateLabel(
	ctx context.Context,
	record devices.CreateLabelRecord,
) (devices.Label, error) {
	prepared, err := devices.PrepareCreateLabel(
		devices.CreateLabelParams{Name: record.Label.Name, Kind: record.Label.Kind},
		record.Label.ID,
		record.Label.CreatedAt,
	)
	if err != nil {
		return devices.Label{}, err
	}
	_, nameKey, err := devices.NormalizeLabelName(prepared.Label.Name)
	if err != nil {
		return devices.Label{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.Label{}, fmt.Errorf("begin label creation: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	count, err := queries.CountLabels(ctx)
	if err != nil {
		return devices.Label{}, fmt.Errorf("count labels: %w", err)
	}
	if count >= devices.MaxLabels {
		return devices.Label{}, devices.ErrLabelLimitReached
	}
	if err = checkLabelNameFree(ctx, queries, nameKey, ""); err != nil {
		return devices.Label{}, err
	}
	label := prepared.Label
	if err = queries.InsertLabel(ctx, dbsqlc.InsertLabelParams{
		ID: string(label.ID), Name: label.Name, NameKey: nameKey, Kind: string(label.Kind),
		CreatedAt: formatTime(label.CreatedAt), UpdatedAt: formatTime(label.UpdatedAt),
	}); err != nil {
		return devices.Label{}, fmt.Errorf("insert label: %w", err)
	}
	created, err := getLabel(ctx, queries, label.ID)
	if err != nil {
		return devices.Label{}, err
	}
	if err = tx.Commit(); err != nil {
		return devices.Label{}, fmt.Errorf("commit label creation: %w", err)
	}
	return created, nil
}

// RenameLabel changes only the display name. An identical name is a no-op
// that preserves the update time.
func (repository *DeviceRepository) RenameLabel(
	ctx context.Context,
	record devices.RenameLabelRecord,
) (devices.Label, error) {
	prepared, err := devices.PrepareRenameLabel(record.ID, record.Name, record.UpdatedAt)
	if err != nil {
		return devices.Label{}, err
	}
	_, nameKey, err := devices.NormalizeLabelName(prepared.Name)
	if err != nil {
		return devices.Label{}, err
	}
	tx, err := repository.database.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
	if err != nil {
		return devices.Label{}, fmt.Errorf("begin label rename: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	queries := repository.queries.WithTx(tx)
	current, err := getLabel(ctx, queries, prepared.ID)
	if err != nil {
		return devices.Label{}, err
	}
	if current.Name != prepared.Name {
		if err = checkLabelNameFree(ctx, queries, nameKey, prepared.ID); err != nil {
			return devices.Label{}, err
		}
		if err = queries.UpdateLabelName(ctx, dbsqlc.UpdateLabelNameParams{
			ID: string(prepared.ID), Name: prepared.Name, NameKey: nameKey,
			UpdatedAt: formatTime(prepared.UpdatedAt),
		}); err != nil {
			return devices.Label{}, fmt.Errorf("rename label: %w", err)
		}
		if current, err = getLabel(ctx, queries, prepared.ID); err != nil {
			return devices.Label{}, err
		}
	}
	if err = tx.Commit(); err != nil {
		return devices.Label{}, fmt.Errorf("commit label rename: %w", err)
	}
	return current, nil
}

// DeleteLabel relies on the schema's foreign keys to clear Areas and remove
// tag assignments in the deleting statement.
func (repository *DeviceRepository) DeleteLabel(ctx context.Context, id devices.LabelID) error {
	if _, err := devices.ParseLabelID(string(id)); err != nil {
		return fmt.Errorf("%w: %w", devices.ErrInvalidLabel, err)
	}
	deleted, err := repository.queries.DeleteLabel(ctx, dbsqlc.DeleteLabelParams{ID: string(id)})
	if err != nil {
		return fmt.Errorf("delete label: %w", err)
	}
	if deleted == 0 {
		return devices.ErrLabelNotFound
	}
	return nil
}

func (repository *DeviceRepository) ListLabels(ctx context.Context) ([]devices.Label, error) {
	rows, err := repository.queries.ListLabels(ctx)
	if err != nil {
		return nil, fmt.Errorf("list labels: %w", err)
	}
	labels := make([]devices.Label, 0, len(rows))
	for _, row := range rows {
		label, decodeErr := labelFromRow(row)
		if decodeErr != nil {
			return nil, decodeErr
		}
		labels = append(labels, label)
	}
	return labels, nil
}

func getLabel(ctx context.Context, queries *dbsqlc.Queries, id devices.LabelID) (devices.Label, error) {
	row, err := queries.GetLabel(ctx, dbsqlc.GetLabelParams{ID: string(id)})
	if errors.Is(err, sql.ErrNoRows) {
		return devices.Label{}, devices.ErrLabelNotFound
	}
	if err != nil {
		return devices.Label{}, fmt.Errorf("get label: %w", err)
	}
	return labelFromRow(row)
}

// checkLabelNameFree reports a conflict when another Label holds the key. A
// rename may keep its own key, for example to change only letter case.
func checkLabelNameFree(ctx context.Context, queries *dbsqlc.Queries, key string, self devices.LabelID) error {
	holder, err := queries.GetLabelIDByNameKey(ctx, dbsqlc.GetLabelIDByNameKeyParams{NameKey: key})
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("check label name: %w", err)
	}
	if devices.LabelID(holder) == self {
		return nil
	}
	return devices.ErrLabelNameConflict
}
