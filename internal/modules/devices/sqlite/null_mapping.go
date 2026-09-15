package sqlite

import (
	"database/sql"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// nullableCompletionFailure encodes an empty CommandCompletion.FailureCode
// as SQL NULL for successful terminal outcomes such as dispatched. The
// existing nullableCommandFailure stays unchanged for command-record
// creation from *CommandFailureCode.
func nullableCompletionFailure(value devices.CommandFailureCode) sql.NullString {
	if value == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: string(value), Valid: true}
}

func boolToInt64(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func nullableRuntimeID(value *devices.RuntimeID) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableCommandFailure(value *devices.CommandFailureCode) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: string(*value), Valid: true}
}

func nullableString(value *string) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *value, Valid: true}
}

func nullableText(value string) sql.NullString {
	return sql.NullString{String: value, Valid: value != ""}
}
