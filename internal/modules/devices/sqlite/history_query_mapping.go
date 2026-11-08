package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// SQL guarantees accepted disposition and JSON shape; this projection validates
// canonical identity and the timestamp representation used for lexical ordering.
type historyQueryRow struct {
	ReceiveOrder      int64
	ObservationID     string
	Disposition       string
	AdapterReceivedAt string
	SourceUpdatedAt   sql.NullString
	ObservedAt        string
	ValueBytes        int64
	StateValue        sql.NullString
}

func historyQueryEntryFromRow(row historyQueryRow) (devices.EntityStateHistoryEntry, error) {
	if _, err := devices.ParseObservationID(row.ObservationID); err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("invalid retained history observation ID: %w", err)
	}
	if row.ReceiveOrder <= 0 || row.ValueBytes <= 0 {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("invalid retained history row")
	}
	observed, err := parseTime(row.ObservedAt)
	if err != nil || formatTime(observed) != row.ObservedAt {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("invalid retained history observed_at")
	}
	entry, err := entityStateHistoryEntryFromRow(
		entityStateHistoryRow{
			ObservationID:     row.ObservationID,
			StateValueJSON:    row.StateValue,
			Disposition:       row.Disposition,
			AdapterReceivedAt: row.AdapterReceivedAt,
			SourceUpdatedAt:   row.SourceUpdatedAt,
			ObservedAt:        row.ObservedAt,
			ReceiveOrder:      row.ReceiveOrder,
		},
	)
	if err != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf("map retained history: %w", err)
	}
	return entry, nil
}
