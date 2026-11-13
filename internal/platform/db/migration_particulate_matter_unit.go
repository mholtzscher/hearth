package db

import (
	"context"
	"database/sql"
	"errors"
)

const particulateMatterUnitMigrationVersion = 33

// Stored observations have no unit and the numeric scale is unchanged.
const particulateMatterUnitUp = `
UPDATE entities
SET support_json = json_set(support_json,
    '$.state.unit', 'µg/m³',
    '$.state.quantity_kind', 'particulate_matter')
WHERE type_id = 'hearth.numericsensor/v1'
  AND json_extract(support_json, '$.state.unit') = 'ug/m3'
  AND json_type(support_json, '$.state.quantity_kind') IS NULL
  AND id IN (SELECT entity_id FROM adapter_entity_mappings WHERE entity_key = 'pm25')`

func migrateParticulateMatterUnit(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, particulateMatterUnitUp)
	return err
}

func refuseParticulateMatterUnitDown(context.Context, *sql.Tx) error {
	return errors.New(
		"restore the pre-upgrade backup with its matching binary; the PM2.5 unit has no lossless Down migration",
	)
}
