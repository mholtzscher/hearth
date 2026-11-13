package mdns //nolint:testpackage // Characterizes the browser's internal event boundary.

import (
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"
)

// TestBrowserEventTranscript pins logical DNS queries, endpoint changes and wake
// deadlines from the original socket loop. Query order within one step is irrelevant.
func TestBrowserEventTranscript(t *testing.T) {
	t.Parallel()
	base := time.Unix(1000, 0)
	state := newBrowserState([]string{"eth0"}, time.Hour, base)
	var transcript []string
	step := func(label string, elapsed time.Duration, lines ...string) {
		t.Helper()
		now := base.Add(elapsed)
		var effects browserEffects
		var err error
		if lines == nil {
			effects, err = state.tick(now)
		} else {
			msg := &dns.Msg{}
			msg.Response = true
			for _, line := range lines {
				msg.Answer = append(msg.Answer, rr(t, line))
			}
			effects, err = state.handlePacket(msg, "eth0", now)
		}
		require.NoError(t, err)
		var queries []string
		for _, action := range effects.queries {
			queries = append(queries, describeQuery(action.message))
		}
		sort.Strings(queries)
		var endpoints []string
		for _, r := range effects.endpoints {
			endpoints = append(endpoints, fmt.Sprintf("%s:%d encrypted=%t", r.Address, r.Port, r.Encrypted))
		}
		transcript = append(
			transcript,
			fmt.Sprintf("%s queries=%v endpoints=%v wake=%s", label, queries, endpoints, effects.wake.Sub(base)),
		)
	}
	step("start", 0)
	step("ptr", 200*time.Millisecond, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local.")
	step("retry", 1200*time.Millisecond)
	step("complete", 1300*time.Millisecond,
		"room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local.",
		`room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`,
		"room.local. 5 IN A 192.0.2.10", "room.local. 120 IN AAAA 2001:db8::1")
	step("duplicate", 2*time.Second, "room.local. 5 IN A 192.0.2.10")
	step("expiry", 7*time.Second)
	step("txt", 8*time.Second, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff" "api_encryption=1"`)
	step("known alternate", 9*time.Second, "room.local. 120 IN AAAA 2001:db8::1")
	step("new alternate", 10*time.Second, "room.local. 120 IN AAAA 2001:db8::2")
	step("port", 11*time.Second, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6054 room.local.")
	t.Log("\n" + strings.Join(transcript, "\n"))
	require.Equal(t, []string{
		"start queries=[_esphomelib._tcp.local./PTR] endpoints=[] wake=1h0m0s",
		"ptr queries=[room._esphomelib._tcp.local./SRV room._esphomelib._tcp.local./TXT] endpoints=[] wake=1.2s",
		"retry queries=[room._esphomelib._tcp.local./SRV room._esphomelib._tcp.local./TXT] endpoints=[] wake=3.2s",
		"complete queries=[] endpoints=[192.0.2.10:6053 encrypted=false] wake=5.3s",
		"duplicate queries=[] endpoints=[] wake=6s",
		"expiry queries=[] endpoints=[] wake=1m36.2s",
		"txt queries=[] endpoints=[192.0.2.10:6053 encrypted=true] wake=1m36.2s",
		"known alternate queries=[] endpoints=[] wake=1m36.2s",
		"new alternate queries=[] endpoints=[2001:db8::2:6053 encrypted=true] wake=1m36.2s",
		"port queries=[] endpoints=[2001:db8::2:6054 encrypted=true] wake=1m36.2s",
	}, transcript)
}

func describeQuery(msg dns.Msg) string {
	var parts []string
	for _, q := range msg.Question {
		parts = append(parts, fmt.Sprintf("%s/%s", q.Name, dns.TypeToString[q.Qtype]))
	}
	for _, answer := range msg.Answer {
		parts = append(parts, fmt.Sprintf("answer=%s TTL=%d", answer.Header().Name, answer.Header().Ttl))
	}
	return strings.Join(parts, "+")
}
