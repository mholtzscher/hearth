package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

func nullableEntityEventRejection(value *devices.EntityEventRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

// entityEventHistoryEntryFromRow maps one retained Entity Event. The table's
// CHECK constraints fix the disposition and pair rejection codes with it.
func entityEventHistoryEntryFromRow(row dbsqlc.ListEntityEventsRow) (devices.EntityEventHistoryEntry, error) {
	var rejection *devices.EntityEventRejection
	if row.RejectionCode.Valid {
		code := devices.EntityEventRejection(row.RejectionCode.String)
		rejection = &code
	}
	emittedAt, err := parseTime(row.EmittedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event emitted_at: %w", err)
	}
	receivedAt, err := parseTime(row.ReceivedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event received_at: %w", err)
	}
	recordedAt, err := parseTime(row.RecordedAt)
	if err != nil {
		return devices.EntityEventHistoryEntry{}, fmt.Errorf("parse Entity Event recorded_at: %w", err)
	}
	return devices.EntityEventHistoryEntry{
		EventID: devices.EntityEventID(row.EventID), EntityID: devices.EntityID(row.EntityID),
		Name: devices.EntityEventName(row.Name), Disposition: devices.EntityEventDisposition(row.Disposition),
		Rejection: rejection,
		EmittedAt: emittedAt, ReceivedAt: receivedAt, RecordedAt: recordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}, nil
}
