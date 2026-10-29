package sqlite //nolint:testpackage // Tests cover the module-owned storage encoding.

import (
	"database/sql"
	"testing"
	"time"
)

func TestTimestampEncodingPreservesInstant(t *testing.T) {
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
			want := sql.NullString{String: encoded, Valid: true}
			if nullable := formatNullableTime(at); nullable != want {
				t.Fatalf("nullable timestamp = %#v, want %q", nullable, encoded)
			}
			if nullable := nullableTime(&at); nullable != want {
				t.Fatalf("optional timestamp = %#v, want %q", nullable, encoded)
			}
		})
	}
	if _, err := parseTime("not-a-time"); err == nil {
		t.Fatal("malformed timestamp accepted")
	}
}

func TestOptionalTimestampEncodingPreservesNull(t *testing.T) {
	t.Parallel()
	if formatNullableTime(time.Time{}).Valid || nullableTime(nil).Valid {
		t.Fatal("absent timestamp must encode SQL NULL")
	}
	zero := time.Time{}
	if value := nullableTime(&zero); !value.Valid || value.String != "0001-01-01T00:00:00.000000000Z" {
		t.Fatalf("present zero timestamp = %#v", value)
	}
}
