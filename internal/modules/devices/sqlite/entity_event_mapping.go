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

type entityEventHistoryRow struct {
	EventID       string
	EntityID      string
	Name          string
	Disposition   string
	RejectionCode sql.NullString
	EmittedAt     string
	ReceivedAt    string
	RecordedAt    string
	ReceiveOrder  int64
}

func entityEventHistoryRowFromFirstPage(row dbsqlc.ListEntityEventsFirstPageRow) entityEventHistoryRow {
	return entityEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func entityEventHistoryRowFromAfter(row dbsqlc.ListEntityEventsAfterRow) entityEventHistoryRow {
	return entityEventHistoryRow{
		EventID: row.EventID, EntityID: row.EntityID, Name: row.Name,
		Disposition: row.Disposition, RejectionCode: row.RejectionCode,
		EmittedAt: row.EmittedAt, ReceivedAt: row.ReceivedAt, RecordedAt: row.RecordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}
}

func entityEventHistoryEntryFromRow(row entityEventHistoryRow) (devices.EntityEventHistoryEntry, error) {
	disposition := devices.EntityEventDisposition(row.Disposition)
	var rejection *devices.EntityEventRejection
	if row.RejectionCode.Valid {
		code := devices.EntityEventRejection(row.RejectionCode.String)
		rejection = &code
	}
	switch disposition {
	case devices.EntityEventDispositionAccepted:
		if rejection != nil {
			return devices.EntityEventHistoryEntry{}, fmt.Errorf(
				"accepted Entity Event row %q carries a rejection code", row.EventID,
			)
		}
	case devices.EntityEventDispositionRejected:
		if rejection == nil {
			return devices.EntityEventHistoryEntry{}, fmt.Errorf(
				"rejected Entity Event row %q is missing its rejection code", row.EventID,
			)
		}
	default:
		return devices.EntityEventHistoryEntry{}, fmt.Errorf(
			"entity event row %q has unknown disposition %q", row.EventID, row.Disposition,
		)
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
		Name: devices.EntityEventName(row.Name), Disposition: disposition, Rejection: rejection,
		EmittedAt: emittedAt, ReceivedAt: receivedAt, RecordedAt: recordedAt,
		ReceiveOrder: row.ReceiveOrder,
	}, nil
}
