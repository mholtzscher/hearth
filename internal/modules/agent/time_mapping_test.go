package agent //nolint:testpackage // Tests drive the unexported timestamp helpers.

import (
	"strings"
	"testing"
	"time"
)

func TestAgentTimestampIsFixedWidthAndSortable(t *testing.T) {
	t.Parallel()
	base := time.Date(2026, 9, 10, 12, 0, 0, 0, time.UTC)
	early := encodeAgentTimestamp(base)
	late := encodeAgentTimestamp(base.Add(500 * time.Millisecond))

	if len(early) != len(agentTimestampLayout) || len(late) != len(agentTimestampLayout) {
		t.Fatalf("widths = %d, %d, want %d", len(early), len(late), len(agentTimestampLayout))
	}
	if early >= late {
		t.Fatalf("lexical order %q >= %q, want chronological order", early, late)
	}
	if !strings.HasSuffix(late, "500000000Z") {
		t.Fatalf("late = %q, want a nine-digit fraction", late)
	}
	parsed, err := decodeAgentTimestamp(late)
	if err != nil {
		t.Fatal(err)
	}
	if !parsed.Equal(base.Add(500*time.Millisecond)) || parsed.Location() != time.UTC {
		t.Fatalf("round trip = %v, want the encoded instant in UTC", parsed)
	}
	// Legacy variable-width rows still decode during the upgrade.
	if _, legacyErr := decodeAgentTimestamp("2026-09-10T12:00:00Z"); legacyErr != nil {
		t.Fatalf("legacy RFC3339 decode: %v", legacyErr)
	}
}
