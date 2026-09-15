package sqlite

import (
	"database/sql"
	"fmt"
	"time"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatSortableTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.000000000Z")
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func parseOptionalTime(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil //nolint:nilnil // SQL NULL is represented by a nil optional time.
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func parseRequiredTime(value sql.NullString, name string) (time.Time, error) {
	if !value.Valid {
		return time.Time{}, fmt.Errorf("%s is missing", name)
	}
	parsed, err := parseTime(value.String)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse %s: %w", name, err)
	}
	return parsed, nil
}

func formatNullableTime(value time.Time) sql.NullString {
	if value.IsZero() {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(value), Valid: true}
}

func nullableTime(value *time.Time) sql.NullString {
	if value == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: formatTime(*value), Valid: true}
}

func copyTimePointer(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	cloned := value.UTC()
	return &cloned
}
