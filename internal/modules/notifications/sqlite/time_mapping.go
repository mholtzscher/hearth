package sqlite

import (
	"database/sql"
	"time"
)

func timestamp(at time.Time) string { return at.UTC().Format("2006-01-02T15:04:05.000000000Z") }

// Reads retain RFC3339Nano acceptance while writes use fixed-width UTC.
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
