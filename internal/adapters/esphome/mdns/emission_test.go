package mdns //nolint:testpackage // Emission decisions are internal to the browser.

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

func packetAt(t *testing.T, c *recordCache, now time.Time, records ...dns.RR) map[string]bool {
	t.Helper()
	msg := &dns.Msg{Answer: records}
	msg.Response = true
	require.NoError(t, c.ingest(msg, "eth0", now))
	return positiveRecords(msg, "eth0")
}

func TestKnownAddressHistoryHasVisibleLimit(t *testing.T) {
	t.Parallel()
	known := make(map[string]bool, maxCacheRecords)
	for i := range maxCacheRecords {
		known[fmt.Sprintf("address-%d", i)] = true
	}
	_, _, err := mergeKnown(known, map[string]bool{"another": true}, maxCacheRecords)
	require.ErrorContains(t, err, "known address limit")
}

func flush(t *testing.T, text string) dns.RR {
	t.Helper()
	r := rr(t, text)
	r.Header().Class |= 0x8000
	return r
}

func TestEmissionIgnoresExpiryButAcceptsPositiveUpdates(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	out := make(chan esphome.DiscoveryRecord, 10)
	sent := make(map[esphome.MAC]resolvedRecord)
	emitAt := func(at time.Time, evidence map[string]bool) {
		require.NoError(t, emit(context.Background(), out, nil, sent, c, at, evidence))
	}
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 5 IN A 192.0.2.10"),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	emitAt(now, first)
	initial := <-out
	require.Equal(t, "192.0.2.10", initial.Address)
	emitAt(now.Add(6*time.Second), nil)
	require.Empty(t, out, "expiry alone must not replace a connected endpoint")
	unrelated := packetAt(t, c, now.Add(7*time.Second), rr(t, "unrelated.local. 120 IN A 192.0.2.77"))
	emitAt(now.Add(7*time.Second), unrelated)
	require.Empty(t, out, "unrelated packets must not turn expiry into an update")
	txt := packetAt(
		t,
		c,
		now.Add(8*time.Second),
		flush(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff" "api_encryption=1"`),
	)
	emitAt(now.Add(8*time.Second), txt)
	updated := <-out
	require.Equal(t, initial.Address, updated.Address)
	require.True(t, updated.Encrypted)
	address := packetAt(t, c, now.Add(9*time.Second), rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	emitAt(now.Add(9*time.Second), address)
	require.Empty(t, out, "a previously known alternate refresh must not replace an expired address")
	address = packetAt(t, c, now.Add(10*time.Second), rr(t, "room.local. 120 IN AAAA 2001:db8::2"))
	emitAt(now.Add(10*time.Second), address)
	updated = <-out
	require.Equal(t, "2001:db8::2", updated.Address)
	srv := packetAt(
		t,
		c,
		now.Add(11*time.Second),
		flush(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6054 room.local."),
	)
	emitAt(now.Add(11*time.Second), srv)
	updated = <-out
	require.Equal(t, uint16(6054), updated.Port)
}

func TestAlternateLearnedAfterEmissionRemainsKnown(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	out := make(chan esphome.DiscoveryRecord, 4)
	sent := make(map[esphome.MAC]resolvedRecord)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 5 IN A 192.0.2.10"))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now, first))
	require.Equal(t, "192.0.2.10", (<-out).Address)
	alt := rr(t, "room.local. 120 IN AAAA 2001:db8::1")
	evidence := packetAt(t, c, now.Add(time.Second), alt)
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now.Add(time.Second), evidence))
	require.Empty(t, out)
	evidence = packetAt(t, c, now.Add(6*time.Second), alt)
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now.Add(6*time.Second), evidence))
	require.Empty(t, out)
}

func TestSplitFlushRefreshAndReplacement(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	out := make(chan esphome.DiscoveryRecord, 10)
	sent := make(map[esphome.MAC]resolvedRecord)
	emitAt := func(at time.Time, evidence map[string]bool) {
		require.NoError(t, emit(context.Background(), out, nil, sent, c, at, evidence))
	}
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		flush(t, "room.local. 120 IN A 192.0.2.10"),
		flush(t, "room.local. 120 IN A 192.0.2.11"))
	emitAt(now, first)
	require.Equal(t, "192.0.2.10", (<-out).Address)
	for _, elapsed := range []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second} {
		at := now.Add(elapsed)
		one := packetAt(t, c, at, flush(t, "room.local. 120 IN A 192.0.2.10"))
		emitAt(at, one)
		two := packetAt(t, c, at.Add(300*time.Millisecond), flush(t, "room.local. 120 IN A 192.0.2.11"))
		emitAt(at.Add(300*time.Millisecond), two)
		emitAt(at.Add(1100*time.Millisecond), nil)
		require.Empty(t, out)
		require.Contains(t, c.records, recordKey(rr(t, "room.local. 120 IN A 192.0.2.10"), "eth0"))
		require.Contains(t, c.records, recordKey(rr(t, "room.local. 120 IN A 192.0.2.11"), "eth0"))
	}
	at := now.Add(40 * time.Second)
	replacement := packetAt(t, c, at, flush(t, "room.local. 120 IN A 192.0.2.12"))
	emitAt(at, replacement)
	require.Empty(t, out, "the previous address is usable during the grace period")
	late := at.Add(1100 * time.Millisecond)
	require.Equal(t, late, c.nextWake(late, late.Add(time.Hour)))
	emitAt(late, nil)
	require.Equal(t, "192.0.2.12", (<-out).Address)
}

func TestFlushDoesNotPromoteAnUnrelatedAddressFamily(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	out := make(chan esphome.DiscoveryRecord, 4)
	sent := make(map[esphome.MAC]resolvedRecord)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now, first))
	require.Equal(t, "192.0.2.10", (<-out).Address)
	at := now.Add(2 * time.Second)
	evidence := packetAt(t, c, at, flush(t, "room.local. 120 IN A 0.0.0.0"))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, at, evidence))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, at.Add(time.Second), nil))
	require.Empty(t, out)
}

func TestNewIPv4EvidenceCanReplaceIPv6(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	out := make(chan esphome.DiscoveryRecord, 4)
	sent := make(map[esphome.MAC]resolvedRecord)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now, first))
	require.Equal(t, "2001:db8::1", (<-out).Address)
	evidence := packetAt(t, c, now.Add(time.Second), rr(t, "room.local. 120 IN A 192.0.2.10"))
	require.NoError(t, emit(context.Background(), out, nil, sent, c, now.Add(time.Second), evidence))
	require.Equal(t, "192.0.2.10", (<-out).Address)
}
