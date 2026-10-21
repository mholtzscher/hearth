package mcpapi

import (
	"time"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func optionalTime(value *time.Time) *string {
	if value == nil {
		return nil
	}
	text := formatTime(*value)
	return &text
}
