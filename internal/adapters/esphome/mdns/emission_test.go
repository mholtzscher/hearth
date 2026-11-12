package mdns //nolint:testpackage // Emission decisions are internal to the browser.

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func packetAt(t *testing.T, s *browserState, now time.Time, records ...dns.RR) browserEffects {
	t.Helper()
	msg := &dns.Msg{Answer: records}
	msg.Response = true
	effects, err := s.handlePacket(msg, "eth0", now)
	require.NoError(t, err)
	return effects
}

func tickAt(t *testing.T, s *browserState, now time.Time) browserEffects {
	t.Helper()
	effects, err := s.tick(now)
	require.NoError(t, err)
	return effects
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
	c := newBrowserState([]string{"eth0"}, time.Hour, now)
	tickAt(t, c, now)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 5 IN A 192.0.2.10"),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	initial := first.endpoints[0]
	require.Equal(t, "192.0.2.10", initial.Address)
	require.Empty(
		t,
		tickAt(t, c, now.Add(6*time.Second)).endpoints,
		"expiry alone must not replace a connected endpoint",
	)
	unrelated := packetAt(t, c, now.Add(7*time.Second), rr(t, "unrelated.local. 120 IN A 192.0.2.77"))
	require.Empty(t, unrelated.endpoints, "unrelated packets must not turn expiry into an update")
	txt := packetAt(
		t,
		c,
		now.Add(8*time.Second),
		flush(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff" "api_encryption=1"`),
	)
	updated := txt.endpoints[0]
	require.Equal(t, initial.Address, updated.Address)
	require.True(t, updated.Encrypted)
	address := packetAt(t, c, now.Add(9*time.Second), rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	require.Empty(t, address.endpoints, "a previously known alternate refresh must not replace an expired address")
	address = packetAt(t, c, now.Add(10*time.Second), rr(t, "room.local. 120 IN AAAA 2001:db8::2"))
	updated = address.endpoints[0]
	require.Equal(t, "2001:db8::2", updated.Address)
	srv := packetAt(
		t,
		c,
		now.Add(11*time.Second),
		flush(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6054 room.local."),
	)
	updated = srv.endpoints[0]
	require.Equal(t, uint16(6054), updated.Port)
}

func TestAlternateLearnedAfterEmissionRemainsKnown(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newBrowserState([]string{"eth0"}, time.Hour, now)
	tickAt(t, c, now)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 5 IN A 192.0.2.10"))
	require.Equal(t, "192.0.2.10", first.endpoints[0].Address)
	alt := rr(t, "room.local. 120 IN AAAA 2001:db8::1")
	evidence := packetAt(t, c, now.Add(time.Second), alt)
	require.Empty(t, evidence.endpoints)
	evidence = packetAt(t, c, now.Add(6*time.Second), alt)
	require.Empty(t, evidence.endpoints)
}

func TestSplitFlushRefreshAndReplacement(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newBrowserState([]string{"eth0"}, time.Hour, now)
	tickAt(t, c, now)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		flush(t, "room.local. 120 IN A 192.0.2.10"),
		flush(t, "room.local. 120 IN A 192.0.2.11"))
	require.Equal(t, "192.0.2.10", first.endpoints[0].Address)
	for _, elapsed := range []time.Duration{10 * time.Second, 20 * time.Second, 30 * time.Second} {
		at := now.Add(elapsed)
		one := packetAt(t, c, at, flush(t, "room.local. 120 IN A 192.0.2.10"))
		require.Empty(t, one.endpoints)
		two := packetAt(t, c, at.Add(300*time.Millisecond), flush(t, "room.local. 120 IN A 192.0.2.11"))
		require.Empty(t, two.endpoints)
		require.Empty(t, tickAt(t, c, at.Add(1100*time.Millisecond)).endpoints)
		require.Contains(t, c.cache.records, recordKey(rr(t, "room.local. 120 IN A 192.0.2.10"), "eth0"))
		require.Contains(t, c.cache.records, recordKey(rr(t, "room.local. 120 IN A 192.0.2.11"), "eth0"))
	}
	at := now.Add(40 * time.Second)
	replacement := packetAt(t, c, at, flush(t, "room.local. 120 IN A 192.0.2.12"))
	require.Empty(t, replacement.endpoints, "the previous address is usable during the grace period")
	require.Equal(t, at.Add(time.Second), replacement.wake)
	late := at.Add(1100 * time.Millisecond)
	require.Equal(t, "192.0.2.12", tickAt(t, c, late).endpoints[0].Address)
}

func TestFlushDoesNotPromoteAnUnrelatedAddressFamily(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newBrowserState([]string{"eth0"}, time.Hour, now)
	tickAt(t, c, now)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	require.Equal(t, "192.0.2.10", first.endpoints[0].Address)
	at := now.Add(2 * time.Second)
	evidence := packetAt(t, c, at, flush(t, "room.local. 120 IN A 0.0.0.0"))
	require.Empty(t, evidence.endpoints)
	require.Empty(t, tickAt(t, c, at.Add(time.Second)).endpoints)
}

func TestNewIPv4EvidenceCanReplaceIPv6(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newBrowserState([]string{"eth0"}, time.Hour, now)
	tickAt(t, c, now)
	first := packetAt(t, c, now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN AAAA 2001:db8::1"))
	require.Equal(t, "2001:db8::1", first.endpoints[0].Address)
	evidence := packetAt(t, c, now.Add(time.Second), rr(t, "room.local. 120 IN A 192.0.2.10"))
	require.Equal(t, "192.0.2.10", evidence.endpoints[0].Address)
}
