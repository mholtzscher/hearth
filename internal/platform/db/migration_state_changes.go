package db

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/mholtzscher/hearth/internal/platform/changevalue"
)

const stateChangesMigrationVersion = 31

// Frozen at migration 31; yes/no or text fields added to existing types need
// another backfill. The Devices guard test checks these lists against its catalog.
const stateChangeFields = `
-- change-fields-begin
WITH fields(type_id, field_path) AS (VALUES
 ('hearth.binarysensor/v1', ''),
 ('hearth.colorhs/v1', '/active'),
 ('hearth.colormode/v1', ''),
 ('hearth.colortemp/v1', '/active'),
 ('hearth.colorxy/v1', '/active'),
 ('hearth.enumsetting/v1', ''),
 ('hearth.numericsetting/v1', '/choice'),
 ('hearth.numericsetting/v1', '/mode'),
 ('hearth.power/v1', ''))
-- change-fields-end
`

func stateChangesSchema(ctx context.Context, tx *sql.Tx, up bool) error {
	source, err := migrationFiles.ReadFile("migrations/00031_state_changes.sql")
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

// migrateStateChanges replays retained accepted Observations per Entity in
// receive order, holding only the previous value of each field, and records
// every change exactly as live ingest does. The first retained accepted report
// of an Entity records every field, because its previous State is unknown. It
// may be an unchanged report when retention removed the applied one before it;
// later unchanged reports repeat the held value and record nothing.
func migrateStateChanges(ctx context.Context, tx *sql.Tx) error {
	if err := stateChangesSchema(ctx, tx, true); err != nil {
		return err
	}
	fields, err := frozenStateChangeFields(ctx, tx)
	if err != nil {
		return err
	}
	insert, err := tx.PrepareContext(ctx, `INSERT INTO state_changes
 (entity_id, field_path, observed_at, receive_order, value_json) VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return err
	}
	defer insert.Close()
	rows, err := tx.QueryContext(ctx, `SELECT o.entity_id, e.type_id, o.observed_at, o.receive_order, o.state_value_json
 FROM observations o JOIN entities e ON e.id=o.entity_id
 WHERE o.disposition IN ('applied','unchanged')
 ORDER BY o.entity_id, o.receive_order`)
	if err != nil {
		return err
	}
	defer rows.Close()
	var entity string
	previous := map[string]json.RawMessage{}
	for rows.Next() {
		var nextEntity, typeID, observedAt, state string
		var receiveOrder int64
		if err = rows.Scan(&nextEntity, &typeID, &observedAt, &receiveOrder, &state); err != nil {
			return err
		}
		if nextEntity != entity {
			entity, previous = nextEntity, map[string]json.RawMessage{}
		}
		for _, field := range fields[typeID] {
			if err = backfillStateChange(
				ctx,
				insert,
				previous,
				entity,
				field,
				observedAt,
				receiveOrder,
				state,
			); err != nil {
				return err
			}
		}
	}
	if err = rows.Err(); err != nil {
		return err
	}
	return rows.Close()
}

// backfillStateChange records one field of one applied report when its value
// differs from the field's previous value, or when no previous value is known.
func backfillStateChange(
	ctx context.Context, insert *sql.Stmt, previous map[string]json.RawMessage,
	entity, field, observedAt string, receiveOrder int64, state string,
) error {
	value, err := changevalue.Select(json.RawMessage(state), field)
	if err != nil {
		return err
	}
	if prior, known := previous[field]; known && bytes.Equal(prior, value) {
		return nil
	}
	if _, err = insert.ExecContext(ctx, entity, field, observedAt, receiveOrder, string(value)); err != nil {
		return err
	}
	previous[field] = value
	return nil
}

func frozenStateChangeFields(ctx context.Context, tx *sql.Tx) (map[string][]string, error) {
	rows, err := tx.QueryContext(
		ctx,
		stateChangeFields+`SELECT type_id, field_path FROM fields ORDER BY type_id, field_path`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	fields := map[string][]string{}
	for rows.Next() {
		var typeID, path string
		if err = rows.Scan(&typeID, &path); err != nil {
			return nil, err
		}
		fields[typeID] = append(fields[typeID], path)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	return fields, rows.Close()
}

func reverseStateChanges(ctx context.Context, tx *sql.Tx) error {
	return stateChangesSchema(ctx, tx, false)
}
