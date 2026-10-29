package sqlite

import (
	"database/sql"
	"fmt"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/devices/sqlite/dbsqlc"
)

type entityStateHistoryRow struct {
	ObservationID     string
	StateValueJSON    sql.NullString
	Disposition       string
	RejectionCode     sql.NullString
	AdapterReceivedAt string
	SourceUpdatedAt   sql.NullString
	ObservedAt        string
	ReceiveOrder      int64
}

func entityStateHistoryEntryFromRow(row entityStateHistoryRow) (devices.EntityStateHistoryEntry, error) {
	disposition := devices.ObservationDisposition(row.Disposition)
	var value devices.Value
	if row.StateValueJSON.Valid {
		value = devices.Value(row.StateValueJSON.String)
	}
	if disposition == devices.DispositionRejected && value != nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf(
			"rejected State history row %q carries a value",
			row.ObservationID,
		)
	}
	if disposition != devices.DispositionRejected && value == nil {
		return devices.EntityStateHistoryEntry{}, fmt.Errorf(
			"accepted State history row %q is missing its value",
			row.ObservationID,
		)
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
		Disposition:       disposition,
		Rejection:         rejection,
		AdapterReceivedAt: adapterReceivedAt,
		SourceUpdatedAt:   sourceUpdatedAt,
		ObservedAt:        observedAt,
		ReceiveOrder:      row.ReceiveOrder,
	}, nil
}

func toEntityStateHistoryRowsUpdatesFirstPage(
	rows []dbsqlc.ListEntityStateUpdatesFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsUpdatesAfter(
	rows []dbsqlc.ListEntityStateUpdatesAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsHistoryFirstPage(
	rows []dbsqlc.ListEntityStateHistoryFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsHistoryAfter(
	rows []dbsqlc.ListEntityStateHistoryAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsDispositionFirstPage(
	rows []dbsqlc.ListEntityStateHistoryByDispositionFirstPageRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}

func toEntityStateHistoryRowsDispositionAfter(
	rows []dbsqlc.ListEntityStateHistoryByDispositionAfterRow,
) []entityStateHistoryRow {
	mapped := make([]entityStateHistoryRow, len(rows))
	for index, row := range rows {
		mapped[index] = entityStateHistoryRow{
			ObservationID: row.ObservationID, StateValueJSON: row.StateValueJson,
			Disposition: row.Disposition, RejectionCode: row.RejectionCode,
			AdapterReceivedAt: row.AdapterReceivedAt, SourceUpdatedAt: row.SourceUpdatedAt,
			ObservedAt: row.ObservedAt, ReceiveOrder: row.ReceiveOrder,
		}
	}
	return mapped
}
