package sqlite

import (
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

// entityStateHistoryEntryFromRow maps one retained Observation. The table's
// CHECK constraint pairs a value with accepted dispositions and a rejection
// code with rejected ones, so the row needs no consistency recheck.
func entityStateHistoryEntryFromRow(row dbsqlc.Observation) (devices.EntityStateHistoryEntry, error) {
	var value devices.Value
	if row.StateValueJson.Valid {
		value = devices.Value(row.StateValueJson.String)
	}
	var rejection *devices.ObservationRejection
	if row.RejectionCode.Valid {
		code := devices.ObservationRejection(row.RejectionCode.String)
		rejection = &code
	}
	adapterReceivedAt, err := parseTime(row.AdapterReceivedAt)
	if err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("parse State history adapter_received_at: %w", err)
	}
	sourceUpdatedAt, err := parseOptionalTime(row.SourceUpdatedAt)
	if err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("parse State history source_updated_at: %w", err)
	}
	observedAt, err := parseTime(row.ObservedAt)
	if err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("parse State history observed_at: %w", err)
	}
	return devices.EntityStateHistoryEntry{
		ObservationID:     devices.ObservationID(row.ObservationID),
		Value:             value,
		Disposition:       devices.ObservationDisposition(row.Disposition),
		Rejection:         rejection,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      row.ReceiveOrder,
	}, nil
}
