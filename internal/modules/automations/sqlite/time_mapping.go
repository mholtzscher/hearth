package sqlite

import (
	"database/sql"
	"time"
)

// automationTimestampLayout is the fixed-width UTC layout every stored
// automation timestamp uses, so retention cutoffs compare lexicographically.
const automationTimestampLayout = "2006-01-02T15:04:05.000000000Z"

func encodeAutomationTimestamp(value time.Time) string {
	return value.UTC().Format(automationTimestampLayout)
}

func decodeAutomationTimestamp(value string) (time.Time, error) {
	parsed, err := time.Parse(automationTimestampLayout, value)
	if err != nil {
		return time.Time{}, err
	}
	return parsed.UTC(), nil
}

func parseNullableAutomationTimestamp(value sql.NullString) (*time.Time, error) {
	if !value.Valid {
		return nil, nil //nolint:nilnil // An absent optional timestamp is not an error.
	}
	parsed, err := decodeAutomationTimestamp(value.String)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}
