package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// listEntitiesBySelectorSQL pages the Entity read projection by an optional
// Device, any of a set of types, and all of a set of effective Labels. The type
// and Label sets travel as JSON arrays expanded by json_each, so the statement
// shape is fixed. It is hand-written because sqlc v1.31.1 cannot parse
// json_each. A sqlc.slice version is not a substitute: sqlc numbers named or
// repeated arguments, and SQLite then misbinds the positional parameters a
// slice expands to.
//
// The selected columns are the projection's, in dbsqlc.EntityReadProjection
// order, so the generated row mapping applies.
const listEntitiesBySelectorSQL = `
SELECT p.id, p.device_id, p.adapter_id, p.name, p.adapter_name, p.name_override,
    p.type_id, p.support_json, p.enabled, p.entity_created_at, p.observation_id,
    p.value_json, p.adapter_received_at, p.source_updated_at, p.observed_at,
    p.receive_order, p.availability_status, p.availability_source,
    p.availability_reason_code, p.availability_since, p.availability_evidence_at,
    p.availability_source_observed_at
FROM entity_read_projection AS p
WHERE p.id > ?1
  AND (?2 IS NULL OR p.device_id = ?2)
  AND (?3 = 0 OR p.type_id IN (SELECT value FROM json_each(?4)))
  AND (?5 = 0 OR ?5 = (
      SELECT COUNT(DISTINCT el.label_id)
      FROM entity_effective_labels AS el
      WHERE el.entity_id = p.id
        AND el.label_id IN (SELECT value FROM json_each(?6))
  ))
ORDER BY p.id ASC
LIMIT ?7`

// listEntitiesBySelector reads one page for a non-empty selector. A selected
// Label that does not exist is ErrLabelNotFound rather than an empty page, so
// a caller holding a stale ID can tell.
func listEntitiesBySelector(
	ctx context.Context,
	database *sql.DB,
	queries *dbsqlc.Queries,
	params devices.ListEntitiesParams,
	afterID string,
) ([]dbsqlc.EntityReadProjection, error) {
	selector := params.Selector
	if len(selector.Labels) > 0 {
		ids := make([]string, len(selector.Labels))
		for index, id := range selector.Labels {
			ids[index] = string(id)
		}
		found, err := queries.GetLabelKinds(ctx, dbsqlc.GetLabelKindsParams{Ids: ids})
		if err != nil {
			return nil, fmt.Errorf("load selector labels: %w", err)
		}
		if len(found) != len(ids) {
			return nil, devices.ErrLabelNotFound
		}
	}
	types, err := json.Marshal(nonNilStrings(selector.Types))
	if err != nil {
		return nil, fmt.Errorf("encode selector types: %w", err)
	}
	labels, err := json.Marshal(nonNilStrings(selector.Labels))
	if err != nil {
		return nil, fmt.Errorf("encode selector labels: %w", err)
	}
	var deviceID sql.NullString
	if params.DeviceID != nil {
		deviceID = sql.NullString{String: string(*params.DeviceID), Valid: true}
	}
	rows, err := database.QueryContext(
		ctx, listEntitiesBySelectorSQL,
		afterID, deviceID,
		len(selector.Types), string(types),
		len(selector.Labels), string(labels),
		params.Limit+1,
	)
	if err != nil {
		return nil, fmt.Errorf("list entities by selector: %w", err)
	}
	defer rows.Close()
	var items []dbsqlc.EntityReadProjection
	for rows.Next() {
		var row dbsqlc.EntityReadProjection
		if err = rows.Scan(
			&row.ID, &row.DeviceID, &row.AdapterID, &row.Name, &row.AdapterName, &row.NameOverride,
			&row.TypeID, &row.SupportJson, &row.Enabled, &row.EntityCreatedAt, &row.ObservationID,
			&row.ValueJson, &row.AdapterReceivedAt, &row.SourceUpdatedAt, &row.ObservedAt,
			&row.ReceiveOrder, &row.AvailabilityStatus, &row.AvailabilitySource,
			&row.AvailabilityReasonCode, &row.AvailabilitySince, &row.AvailabilityEvidenceAt,
			&row.AvailabilitySourceObservedAt,
		); err != nil {
			return nil, fmt.Errorf("scan selected entity: %w", err)
		}
		items = append(items, row)
	}
	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("list entities by selector: %w", err)
	}
	return items, nil
}

func nonNilStrings[T ~string](values []T) []string {
	result := make([]string, len(values))
	for index, value := range values {
		result[index] = string(value)
	}
	return result
}
