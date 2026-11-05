//nolint:gosec,mnd // UUIDv7 field widths and masked byte packing are canonical identifier format.
package dbload

import (
	"crypto/sha256"
	"time"

	"github.com/google/uuid"
)

// fixtureClock is the fixed logical instant every seeded timestamp is derived
// from. Fixtures never consult the wall clock, so two runs with the same
// scenario produce the same logical evidence.
//
//nolint:gochecknoglobals // One immutable logical clock is shared by every fixture row.
var fixtureClock = time.Date(2026, time.November, 4, 12, 0, 0, 0, time.UTC)

const (
	fixtureTimestampLayout   = "2006-01-02T15:04:05.000000000Z"
	fixtureUUIDSequenceBits  = 12
	fixtureUUIDSequenceMask  = 0x0f
	fixtureUnknownEntityBase = uint64(1) << 40
	fixtureRunFactSequence   = uint64(1) << 40
)

// fixtureIndex converts a bounded, non-negative fixture count to the uint64
// index space used by fixtureID.
//
//nolint:gosec // Fixture indexes come from non-negative loop bounds.
func fixtureIndex(value int) uint64 { return uint64(value) }

// fixtureStamp renders an offset from the fixed logical clock in the
// fixed-width UTC representation Core writes.
func fixtureStamp(offset time.Duration) string {
	return fixtureClock.Add(offset).Format(fixtureTimestampLayout)
}

// fixtureID builds a deterministic, canonical UUIDv7 for one prefix and
// fixture index. The index is encoded across the timestamp and random fields so
// distinct indexes stay distinct while the text form remains a valid v7 UUID.
func fixtureID(prefix string, index uint64) string {
	var value uuid.UUID
	millis := uint64(fixtureClock.UnixMilli()) + index>>12
	value[0] = byte(millis >> 40)
	value[1] = byte(millis >> 32)
	value[2] = byte(millis >> 24)
	value[3] = byte(millis >> 16)
	value[4] = byte(millis >> 8)
	value[5] = byte(millis)
	value[6] = 0x70 | byte(index>>8)&fixtureUUIDSequenceMask
	value[7] = byte(index)
	value[8] = 0x80 | byte(index>>58)&0x3f
	value[9] = byte(index >> 50)
	value[10] = byte(index >> 42)
	value[11] = byte(index >> 34)
	value[12] = byte(index >> 26)
	value[13] = byte(index >> 18)
	value[14] = byte(index >> 10)
	value[15] = byte(index >> 2)
	return prefix + value.String()
}

// fixtureFingerprint returns a deterministic 32-byte evidence fingerprint.
func fixtureFingerprint(index uint64) []byte {
	sum := sha256.Sum256([]byte(fixtureID("fingerprint", index)))
	return sum[:]
}

// fixtureDigest returns a deterministic 64-character hexadecimal digest used
// where Core stores an availability request fingerprint.
func fixtureDigest(index uint64) string {
	return hashBytes([]byte(fixtureID("digest", index)))
}
