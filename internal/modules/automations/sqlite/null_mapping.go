package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func encodeNullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func commandIDString(value *devices.CommandID) *string {
	if value == nil {
		return nil
	}
	text := string(*value)
	return &text
}

func stringPointer(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}

func commandIDPointer(value sql.NullString) *devices.CommandID {
	if !value.Valid {
		return nil
	}
	id := devices.CommandID(value.String)
	return &id
}

func correlationIDPointer(value sql.NullString) *devices.CorrelationID {
	if !value.Valid {
		return nil
	}
	id := devices.CorrelationID(value.String)
	return &id
}
