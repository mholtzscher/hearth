package agent

import "time"

// agentTimestampLayout is the fixed-width, sortable UTC layout every stored
// agent conversation timestamp uses. Nanoseconds are always printed so TEXT
// ordering matches chronology; RFC3339Nano trims trailing zeros and breaks it.
const agentTimestampLayout = "2006-01-02T15:04:05.000000000Z"

// encodeAgentTimestamp renders one instant in the stored timestamp layout.
func encodeAgentTimestamp(value time.Time) string {
	return value.UTC().Format(agentTimestampLayout)
}

// decodeAgentTimestamp parses a stored timestamp. RFC3339Nano accepts both the
// fixed-width layout and the legacy variable-width rows that migration 00005
// rewrites.
func decodeAgentTimestamp(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}
