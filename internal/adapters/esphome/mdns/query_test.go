package mdns //nolint:testpackage // Query planning is internal to the browser.

import (
	"fmt"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

func TestBrowseBackoffAndKnownAnswerTTL(t *testing.T) {
	t.Parallel()
	interval := time.Second
	for _, want := range []time.Duration{2 * time.Second, 4 * time.Second, 8 * time.Second, 16 * time.Second} {
		interval = nextBrowseInterval(interval)
		require.Equal(t, want, interval)
	}
	require.Equal(t, time.Hour, nextBrowseInterval(time.Hour))
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(t, c, "eth0", now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 300 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 300 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 300 IN A 192.0.2.10"))
	msgs, _, err := c.queryPlan("eth0", now.Add(10*time.Second), true)
	require.NoError(t, err)
	require.NotEmpty(t, msgs)
	require.Equal(t, dns.TypePTR, msgs[0].Question[0].Qtype)
	require.Len(t, msgs[0].Answer, 1)
	require.Equal(t, uint32(110), msgs[0].Answer[0].Header().Ttl)
	msgs, _, err = c.queryPlan("eth0", now.Add(61*time.Second), true)
	require.NoError(t, err)
	require.Empty(t, msgs[0].Answer, "an answer below half its original TTL cannot suppress a reply")
	msgs, _, err = c.queryPlan("eth0", now.Add(95*time.Second), false)
	require.NoError(t, err)
	require.Empty(t, msgs)
	msgs, _, err = c.queryPlan("eth0", now.Add(96*time.Second), false)
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	require.Equal(t, dns.TypePTR, msgs[0].Question[0].Qtype)
	require.Empty(t, msgs[0].Answer)
	require.Equal(t, now.Add(96*time.Second), c.nextWake(now.Add(95*time.Second), now.Add(time.Hour)))
	_, selected, err := c.queryPlan("eth0", now.Add(96*time.Second), false)
	require.NoError(t, err)
	require.NoError(t, c.advanceRefresh(map[string]map[string]bool{"eth0": selected}, now.Add(96*time.Second)))
	require.Equal(t, now.Add(102*time.Second), c.nextWake(now.Add(96*time.Second), now.Add(time.Hour)))
	for _, elapsed := range []time.Duration{102 * time.Second, 108 * time.Second, 114 * time.Second} {
		at := now.Add(elapsed)
		_, selected, err = c.queryPlan("eth0", at, false)
		require.NoError(t, err)
		require.NotEmpty(t, selected)
		require.NoError(t, c.advanceRefresh(map[string]map[string]bool{"eth0": selected}, at))
	}
	require.Equal(t, now.Add(120*time.Second), c.nextWake(now.Add(114*time.Second), now.Add(time.Hour)))
}

func TestKnownAnswersSplitAcrossPackets(t *testing.T) {
	t.Parallel()
	answers := make([]dns.RR, 0, 300)
	for i := range 300 {
		answers = append(
			answers,
			rr(t, "_esphomelib._tcp.local. 120 IN PTR "+longInstance(i)+"._esphomelib._tcp.local."),
		)
	}
	parts, err := packQuestions(dns.Question{Name: serviceName, Qtype: dns.TypePTR, Qclass: dns.ClassINET}, answers)
	require.NoError(t, err)
	require.Greater(t, len(parts), 1)
	count := 0
	for i, part := range parts {
		data, packErr := part.Pack()
		require.NoError(t, packErr)
		require.LessOrEqual(t, len(data), maxQueryBytes)
		require.Equal(t, i < len(parts)-1, part.Truncated)
		if i == 0 {
			require.Len(t, part.Question, 1)
		} else {
			require.Empty(t, part.Question)
		}
		count += len(part.Answer)
	}
	require.Equal(t, len(answers), count)
}

func longInstance(i int) string { return fmt.Sprintf("device-%04d-long-known-answer-example", i) }

func TestNewServiceRequestsMissingPartsWithoutResettingBrowse(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	ptr := rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local.")
	msg := &dns.Msg{Answer: []dns.RR{ptr}}
	msg.Response = true
	instances := c.newInstances(msg, "eth0")
	require.NoError(t, c.ingest(msg, "eth0", now))
	require.ElementsMatch(t, []dns.Question{
		{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeSRV, Qclass: dns.ClassINET},
		{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeTXT, Qclass: dns.ClassINET},
	}, c.missingQuestions("eth0", instances))
	require.Empty(t, c.newInstances(msg, "eth0"), "a refresh must not request missing records again")
	txtKey := questionKey(dns.Question{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeTXT})
	require.NoError(t, c.markMissing("eth0", txtKey, now))
	_, selected, err := c.queryPlan("eth0", now.Add(500*time.Millisecond), true)
	require.NoError(t, err)
	require.NotContains(t, selected, txtKey, "a nearby browse must not duplicate a fresh resolution question")
	_, selected, err = c.queryPlan("eth0", now.Add(time.Second), true)
	require.NoError(t, err)
	require.Contains(t, selected, txtKey)
	srv := rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local.")
	msg.Answer = []dns.RR{srv}
	instances = c.newInstances(msg, "eth0")
	require.NoError(t, c.ingest(msg, "eth0", now))
	require.ElementsMatch(t, []dns.Question{
		{Name: "room.local.", Qtype: dns.TypeA, Qclass: dns.ClassINET},
		{Name: "room.local.", Qtype: dns.TypeAAAA, Qclass: dns.ClassINET},
	}, c.missingQuestions("eth0", instances))
}

func TestMissingComponentsRetryIndependentlyOfBrowse(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	ptr := rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local.")
	add(t, c, "eth0", now, ptr)
	srvKey := questionKey(dns.Question{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeSRV})
	txtKey := questionKey(dns.Question{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeTXT})
	require.NoError(t, c.markMissing("eth0", srvKey, now))
	require.NoError(t, c.markMissing("eth0", txtKey, now))
	browse := now.Add(time.Hour)
	require.Equal(t, now.Add(time.Second), c.nextWake(now, browse))
	_, selected, err := c.queryPlan("eth0", now.Add(time.Second), false)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{srvKey: true, txtKey: true}, selected)
	require.NoError(t, c.advanceRefresh(map[string]map[string]bool{"eth0": selected}, now.Add(time.Second)))
	add(t, c, "eth0", now.Add(2*time.Second), ptr)
	require.Equal(
		t,
		now.Add(3*time.Second),
		c.nextWake(now.Add(2*time.Second), browse),
		"PTR refresh must not reset retry backoff",
	)
	_, selected, err = c.queryPlan("eth0", now.Add(3*time.Second), false)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{srvKey: true, txtKey: true}, selected)
	require.NoError(t, c.advanceRefresh(map[string]map[string]bool{"eth0": selected}, now.Add(3*time.Second)))
	require.Equal(t, now.Add(7*time.Second), c.nextWake(now.Add(3*time.Second), browse))
	add(t, c, "eth0", now.Add(4*time.Second),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"))
	c.nextWake(now.Add(4*time.Second), browse)
	require.Empty(t, c.missingLast, "answered questions must stop retrying")
}

func TestMissingAddressRetriesStopWhenAddressOrAssociationArrives(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(t, c, "eth0", now,
		rr(t, "_esphomelib._tcp.local. 10 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`))
	a := questionKey(dns.Question{Name: "room.local.", Qtype: dns.TypeA})
	aaaa := questionKey(dns.Question{Name: "room.local.", Qtype: dns.TypeAAAA})
	require.NoError(t, c.markMissing("eth0", a, now))
	require.NoError(t, c.markMissing("eth0", aaaa, now))
	require.Equal(t, now.Add(time.Second), c.nextWake(now, now.Add(time.Hour)))
	_, selected, err := c.queryPlan("eth0", now.Add(time.Second), false)
	require.NoError(t, err)
	require.Equal(t, map[string]bool{a: true, aaaa: true}, selected)
	add(t, c, "eth0", now.Add(2*time.Second), rr(t, "room.local. 120 IN A 192.0.2.10"))
	c.nextWake(now.Add(2*time.Second), now.Add(time.Hour))
	require.Empty(t, c.missingLast, "either address family completes resolution")
	require.NoError(
		t,
		c.markMissing(
			"eth0",
			questionKey(dns.Question{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeTXT}),
			now.Add(2*time.Second),
		),
	)
	c.nextWake(now.Add(11*time.Second), now.Add(time.Hour))
	require.Empty(t, c.missingLast, "expired associations must not keep retries alive")
}

func TestPreviouslyCompleteAssociationStartsRetryWhenTXTExpires(t *testing.T) {
	t.Parallel()
	now := time.Unix(1000, 0)
	c := newRecordCache()
	add(t, c, "eth0", now,
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 5 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"))
	at := now.Add(6 * time.Second)
	require.Equal(t, at, c.nextWake(at, now.Add(time.Hour)))
	_, selected, err := c.queryPlan("eth0", at, false)
	require.NoError(t, err)
	txtKey := questionKey(dns.Question{Name: "room._esphomelib._tcp.local.", Qtype: dns.TypeTXT})
	require.Equal(t, map[string]bool{txtKey: true}, selected)
	require.NoError(t, c.advanceRefresh(map[string]map[string]bool{"eth0": selected}, at))
	require.Equal(t, at.Add(time.Second), c.nextWake(at, now.Add(time.Hour)))
}
