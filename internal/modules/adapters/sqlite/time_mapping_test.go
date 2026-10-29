package sqlite //nolint:testpackage // Tests cover the module-owned storage encoding.

import (
	"testing"
	"time"
)

func TestTimestampEncodingPreservesInstantAndNull(t *testing.T) {
	t.Parallel()
	for _, text := range []string{
		"2026-10-28T12:00:00Z",
		"2026-10-28T12:00:00.1Z",
		"2026-10-28T12:00:00.100000000Z",
		"2026-10-28T12:00:00.123456789Z",
		"2026-10-28T14:30:00.123456789+02:30",
	} {
		t.Run(text, func(t *testing.T) {
			t.Parallel()
			at, err := parseTime(text)
			if err != nil {
				t.Fatal(err)
			}
			encoded := formatTime(at)
			if len(encoded) != 30 || encoded[29] != 'Z' {
				t.Fatalf("stored timestamp = %q, want fixed-width UTC", encoded)
			}
			decoded, err := parseTime(encoded)
			if err != nil || !decoded.Equal(at) {
				t.Fatalf("decoded timestamp = %v, error = %v, want %v", decoded, err, at)
			}
			nullable := formatNullableTime(at)
			if !nullable.Valid || nullable.String != encoded {
				t.Fatalf("nullable timestamp = %#v, want %q", nullable, encoded)
			}
		})
	}
	if value := formatNullableTime(time.Time{}); value.Valid {
		t.Fatalf("absent timestamp = %#v, want SQL NULL", value)
	}
	if _, err := parseTime("not-a-time"); err == nil {
		t.Fatal("malformed timestamp accepted")
	}
}
