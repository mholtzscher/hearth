package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func nullableRejection(value *devices.ObservationRejection) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableStateValue(disposition devices.ObservationDisposition, value devices.Value) sql.NullString {
	if disposition == devices.DispositionRejected || len(value) == 0 {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}
