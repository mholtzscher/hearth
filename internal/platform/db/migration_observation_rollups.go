package db

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/numstats"
)

const observationRollupsMigrationVersion = 30

// Frozen at migration 30; numeric fields added to existing types need another
// backfill. The Devices guard test checks these lists against its catalog.
const observationRollupFields = `
-- rollup-fields-begin
WITH fields(type_id, field_path) AS (VALUES
 ('hearth.brightness/v1', ''),
 ('hearth.colorhs/v1', '/hue'),
 ('hearth.colorhs/v1', '/saturation'),
 ('hearth.colortemp/v1', '/value'),
 ('hearth.colorxy/v1', '/x'),
 ('hearth.colorxy/v1', '/y'),
 ('hearth.numericsensor/v1', ''),
 ('hearth.numericsetting/v1', '/value'),
 ('hearth.pressure/v1', ''),
 ('hearth.relativehumidity/v1', ''),
 ('hearth.speed/v1', ''),
 ('hearth.temperature/v1', ''))
-- rollup-fields-end
`

func observationRollupSchema(ctx context.Context, tx *sql.Tx, up bool) error {
	source, err := migrationFiles.ReadFile("migrations/00030_observation_rollups.sql")
	if err != nil {
		return err
	}
	before, after, _ := strings.Cut(string(source), "-- +goose Down")
	if !up {
		before = after
	}
	_, err = tx.ExecContext(ctx, before)
	return err
}

// migrateObservationRollups streams ordered samples, holding only one field's
// minute cell at a time. It uses the same exact accumulator as live ingest and
// query merging. The old ordered index remains available until backfill ends.
//
//nolint:gocognit // Streaming holds a single cell and flushes only when its key changes.
func migrateObservationRollups(ctx context.Context, tx *sql.Tx) error {
	if err := observationRollupSchema(ctx, tx, true); err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO observation_rollups
 (entity_id, field_path, cell_start, sample_count, null_count, invalid_count, sum_value, min_value, max_value)
 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	rows, err := tx.QueryContext(ctx, observationRollupFields+`
 SELECT o.entity_id, f.field_path, o.observed_at, o.state_value_json
 FROM observations o JOIN entities e ON e.id=o.entity_id JOIN fields f ON f.type_id=e.type_id
 WHERE o.disposition IN ('applied','unchanged')
 ORDER BY o.entity_id, f.field_path, o.observed_at, o.receive_order`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var entity, field string
	var cell int64
	stats := new(numstats.Stats)
	flush := func() error {
		if entity == "" {
			return nil
		}
		var minimum, maximum any
		if stats.Count > 0 {
			minimum, maximum = stats.Min.Value(), stats.Max.Value()
		}
		_, writeErr := insert.ExecContext(ctx, entity, field, cell, stats.Count, stats.Nulls, stats.Invalid,
			stats.Sum.RatString(), minimum, maximum)
		return writeErr
	}
	for rows.Next() {
		var nextEntity, nextField, stamp string
		var value string
		if err = rows.Scan(&nextEntity, &nextField, &stamp, &value); err != nil {
			return err
		}
		at, parseErr := time.Parse(time.RFC3339Nano, stamp)
		if parseErr != nil {
			return fmt.Errorf("decode rollup backfill time: %w", parseErr)
		}
		nextCell := at.Truncate(time.Minute).Unix()
		if nextEntity != entity || nextField != field || nextCell != cell {
			if err = flush(); err != nil {
				return err
			}
			entity, field, cell, stats = nextEntity, nextField, nextCell, new(numstats.Stats)
		}
		sample, sampleErr := numstats.Select(json.RawMessage(value), field)
		if sampleErr != nil {
			return sampleErr
		}
		stats.Add(sample)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	if err = flush(); err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, "DROP INDEX observations_entity_observed_history_idx")
	return err
}

func reverseObservationRollups(ctx context.Context, tx *sql.Tx) error {
	return observationRollupSchema(ctx, tx, false)
}
