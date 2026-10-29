package sqlite

import (
	"context"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// getEntityStateSnapshotSQL reads one coherent State snapshot for a whole batch
// of Entity IDs. The requested IDs travel as one JSON array parameter that
// json_each expands into the requested-ID relation, so a cross-Automation
// Condition fan-out never spends one SQLite host parameter per Entity. The
// relation is left-joined to entities, entity_states, and observations, so a
// requested ID that matches no Entity, or an Entity with no accepted State,
// still yields exactly one row of negative evidence instead of silently
// disappearing.
//
// The State's backing Observation joins on both the canonical Observation
// identity and the receive order. entity_states holds those as two independent
// foreign keys, so only requiring both to name the same Observations row can
// reject a State whose identity and receive order point at different rows.
// Every backing column stays NULL for such a State, and one row of NULL is the
// evidence the scanner rejects as corrupt rather than serving as Condition
// evidence.
//
// The statement is hand-written rather than generated because sqlc v1.31.1
// cannot parse SQLite table-valued functions such as json_each. Its shape is
// fixed, so the JSON array parameter is the only variable input.
const getEntityStateSnapshotSQL = `
SELECT
    requested.value       AS requested_entity_id,
    e.id                  AS entity_id,
    s.observation_id      AS observation_id,
    s.value_json          AS value_json,
    s.adapter_received_at AS adapter_received_at,
    s.source_updated_at   AS source_updated_at,
    s.observed_at         AS observed_at,
    s.receive_order       AS receive_order,
    o.entity_id           AS backing_entity_id,
    o.disposition         AS backing_disposition,
    o.state_value_json    AS backing_value_json,
    o.adapter_received_at AS backing_adapter_received_at,
    o.source_updated_at   AS backing_source_updated_at,
    o.observed_at         AS backing_observed_at
FROM json_each(CAST(? AS TEXT)) AS requested
LEFT JOIN entities AS e ON e.id = requested.value
LEFT JOIN entity_states AS s ON s.entity_id = e.id
LEFT JOIN observations AS o
    ON o.observation_id = s.observation_id
    AND o.receive_order = s.receive_order
ORDER BY requested.key`

// GetEntityStateSnapshot implements devices.ReadRepository over one statement,
// so every requested Entity is read from one database snapshot without a
// cross-module transaction. An empty request returns an empty snapshot without
// SQL. Every other failure, including corrupt or unbacked stored State, fails
// the whole read so a caller never evaluates a partial batch.
func (repository *DeviceRepository) GetEntityStateSnapshot(
	ctx context.Context,
	ids []devices.EntityID,
) (devices.EntityStateSnapshot, error) {
	if len(ids) == 0 {
		return devices.EntityStateSnapshot{
			Entries: map[devices.EntityID]devices.EntityStateSnapshotEntry{},
		}, nil
	}
	requested, err := marshalEntityStateSnapshotIDs(ids)
	if err != nil {
		return devices.EntityStateSnapshot{}, err
	}
	rows, err := repository.database.QueryContext(ctx, getEntityStateSnapshotSQL, requested)
	if err != nil {
		return devices.EntityStateSnapshot{}, fmt.Errorf("get entity state snapshot: %w", err)
	}
	defer rows.Close()

	entries := make(map[devices.EntityID]devices.EntityStateSnapshotEntry, len(ids))
	for rows.Next() {
		entry, scanErr := scanEntityStateSnapshotEntry(rows)
		if scanErr != nil {
			return devices.EntityStateSnapshot{}, scanErr
		}
		entries[entry.EntityID] = entry
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return devices.EntityStateSnapshot{}, fmt.Errorf("get entity state snapshot: %w", rowsErr)
	}
	return devices.EntityStateSnapshot{Entries: entries}, nil
}
