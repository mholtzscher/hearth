package mdns //nolint:testpackage // Cache tests exercise packet correlation and expiry directly.

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

func rr(t *testing.T, text string) dns.RR {
	t.Helper()
	r, err := dns.NewRR(text)
	require.NoError(t, err)
	return r
}

func add(t *testing.T, c *recordCache, scope string, now time.Time, records ...dns.RR) {
	t.Helper()
	msg := &dns.Msg{Answer: records}
	msg.Response = true
	require.NoError(t, c.ingest(msg, scope, now))
}

func TestCacheCorrelatesSplitRecordsAndExpires(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	ptr := rr(t, "_esphomelib._tcp.local. 30 IN PTR device._esphomelib._tcp.local.")
	srv := rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 6053 host.local.")
	txt := rr(t, `device._esphomelib._tcp.local. 30 IN TXT "mac=aabbccddeeff" "api_encryption="`)
	a := rr(t, "host.local. 5 IN A 192.0.2.4")
	aaaa := rr(t, "host.local. 30 IN AAAA fe80::42")
	add(t, c, "eth0", now, a, aaaa)
	add(t, c, "eth0", now, srv, txt)
	require.Empty(t, c.resolved(now))
	add(t, c, "other", now, ptr)
	require.Empty(t, c.resolved(now))
	add(t, c, "eth0", now, ptr)
	mac, err := esphome.ParseMAC("aa:bb:cc:dd:ee:ff")
	require.NoError(t, err)
	want := esphome.DiscoveryRecord{MAC: mac, Address: "192.0.2.4", Port: 6053, Encrypted: true}
	require.Equal(t, map[string]esphome.DiscoveryRecord{"eth0|device._esphomelib._tcp.local.": want}, c.resolved(now))
	want.Address = "fe80::42%eth0"
	require.Equal(
		t,
		map[string]esphome.DiscoveryRecord{"eth0|device._esphomelib._tcp.local.": want},
		c.resolved(now.Add(6*time.Second)),
	)
	require.Empty(t, c.resolved(now.Add(31*time.Second)))
}

func TestTXTEncryptionAndMAC(t *testing.T) {
	t.Parallel()
	tests := []struct {
		txt              []string
		valid, encrypted bool
	}{
		{[]string{"mac=AABBCCDDEEFF"}, true, false},
		{[]string{"mac=aa:bb:cc:dd:ee:ff", "api_encryption_supported=1"}, true, false},
		{[]string{"mac=aabbccddeeff", "api_encryption"}, true, true},
		{[]string{"mac=aabbccddeeff", "api_encryption=unknown"}, true, true},
		{[]string{"mac=bad", "api_encryption=1"}, false, false},
		{[]string{"mac=aabbccddeeff", "mac=112233445566"}, false, false},
		{[]string{"api_encryption=1"}, false, true},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.txt), func(t *testing.T) {
			t.Parallel()
			_, encrypted, valid := parseTXT(tc.txt)
			require.Equal(t, tc.valid, valid)
			require.Equal(t, tc.encrypted, encrypted)
		})
	}
}

func TestCacheRejectsUnrelatedAndUnusableRecords(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(t, c, "eth0", now,
		rr(t, "_esphomelib._tcp.local. 30 IN PTR device._esphomelib._tcp.local.evil."),
		rr(t, "_other._tcp.local. 30 IN PTR device._esphomelib._tcp.local."),
		rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 0 host.local."),
		rr(t, `device._esphomelib._tcp.local. 30 IN TXT "mac=aabbccddeeff"`),
		rr(t, "host.local. 30 IN A 0.0.0.0"),
		rr(t, "host.local. 30 IN A 224.0.0.1"))
	require.Empty(t, c.resolved(now))
	validSRV := rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 6053 host.local.")
	validSRV.Header().Class |= 0x8000
	add(
		t,
		c,
		"eth0",
		now,
		rr(t, "_esphomelib._tcp.local. 30 IN PTR device._esphomelib._tcp.local."),
		validSRV,
	)
	require.Empty(t, c.resolved(now))
	add(t, c, "eth0", now, rr(t, "host.local. 30 IN A 192.0.2.5"))
	require.Len(t, c.resolved(now), 1)
	invalidSRV := rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 0 host.local.")
	invalidSRV.Header().Class |= 0x8000
	add(t, c, "eth0", now, invalidSRV)
	require.Empty(t, c.resolved(now), "invalid update must replace the previous endpoint")
}

func TestCacheFlushAndGoodbye(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(
		t,
		c,
		"eth0",
		now,
		rr(t, "_esphomelib._tcp.local. 30 IN PTR device._esphomelib._tcp.local."),
		rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 6053 host.local."),
		rr(t, `device._esphomelib._tcp.local. 30 IN TXT "mac=aabbccddeeff"`),
		rr(t, "host.local. 30 IN A 192.0.2.1"),
	)
	a := rr(t, "host.local. 30 IN A 192.0.2.10")
	b := rr(t, "host.local. 30 IN A 192.0.2.11")
	a.Header().Class |= 0x8000
	b.Header().Class |= 0x8000
	add(t, c, "eth0", now.Add(2*time.Second), a, b)
	for _, r := range c.resolved(now.Add(4 * time.Second)) {
		require.Equal(t, "192.0.2.10", r.Address)
	}
	goodbye := rr(t, "host.local. 0 IN A 192.0.2.10")
	add(t, c, "eth0", now.Add(4*time.Second), goodbye)
	for _, r := range c.resolved(now.Add(4 * time.Second)) {
		require.Equal(t, "192.0.2.11", r.Address)
	}
}

func TestCacheOverflowIsVisible(t *testing.T) {
	t.Parallel()
	c := newRecordCache()
	now := time.Unix(1000, 0)
	records := make([]dns.RR, 0, maxCacheRecords+1)
	for i := range maxCacheRecords + 1 {
		records = append(records, rr(t, fmt.Sprintf("host%d.local. 30 IN A 192.0.2.1", i)))
	}
	msg := &dns.Msg{Answer: records}
	msg.Response = true
	err := c.ingest(msg, "eth0", now)
	require.ErrorContains(t, err, "cache limit")
}

func TestQuestionsFollowKnownRecordsOnTheirInterface(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(t, c, "eth0", now,
		rr(t, "_esphomelib._tcp.local. 30 IN PTR device._esphomelib._tcp.local."),
		rr(t, "device._esphomelib._tcp.local. 30 IN SRV 0 0 6053 host.local."))
	require.ElementsMatch(t, []dns.Question{
		{Name: serviceName, Qtype: dns.TypePTR, Qclass: dns.ClassINET},
		{Name: "device._esphomelib._tcp.local.", Qtype: dns.TypeSRV, Qclass: dns.ClassINET},
		{Name: "device._esphomelib._tcp.local.", Qtype: dns.TypeTXT, Qclass: dns.ClassINET},
		{Name: "host.local.", Qtype: dns.TypeA, Qclass: dns.ClassINET},
		{Name: "host.local.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET},
	}, c.questions("eth0", now))
	require.Equal(
		t,
		[]dns.Question{{Name: serviceName, Qtype: dns.TypePTR, Qclass: dns.ClassINET}},
		c.questions("eth1", now),
	)
}
