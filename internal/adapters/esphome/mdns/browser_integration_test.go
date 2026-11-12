//go:build integration

package mdns //nolint:testpackage // The loopback socket seam is intentionally package-private.

import (
	"context"
	"fmt"
	"net"
	"testing"
	"time"

	"github.com/miekg/dns"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/adapters/esphome"
)

// This synthetic peer speaks real UDP/DNS on loopback, without joining ambient mDNS.
//
//nolint:gocognit // The scripted peer and its lifecycle are deliberately kept together.
func TestBrowserQueriesResolvesUpdatesAndJoins(t *testing.T) {
	t.Parallel()
	peer, setupErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, setupErr)
	t.Cleanup(func() { _ = peer.Close() })
	client, setupErr := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, setupErr)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan esphome.DiscoveryRecord, 8)
	done := make(chan error, 1)
	go func() {
		done <- runSockets(ctx, out, []browseSocket{{conn: client, group: peer.LocalAddr().(*net.UDPAddr), scope: "loopback"}}, time.Hour)
	}()

	peerErr := make(chan error, 1)
	update := make(chan struct{})
	go func() {
		buf := make([]byte, 4096)
		_ = peer.SetReadDeadline(time.Now().Add(5 * time.Second))
		n, from, readErr := peer.ReadFromUDP(buf)
		if readErr != nil {
			peerErr <- readErr
			return
		}
		var query dns.Msg
		if err := query.Unpack(buf[:n]); err != nil {
			peerErr <- err
			return
		}
		if len(query.Question) != 1 || query.Question[0].Name != serviceName || query.Question[0].Qtype != dns.TypePTR {
			peerErr <- fmt.Errorf("unexpected query: %v", query.Question)
			return
		}
		send := func(lines ...string) error {
			var msg dns.Msg
			msg.Response, msg.Authoritative = true, true
			for _, line := range lines {
				r, err := dns.NewRR(line)
				if err != nil {
					return err
				}
				if r.Header().Rrtype != dns.TypePTR {
					r.Header().Class |= 0x8000
				}
				msg.Answer = append(msg.Answer, r)
			}
			data, err := msg.Pack()
			if err != nil {
				return err
			}
			_, err = peer.WriteToUDP(data, from)
			return err
		}
		if err := send(
			"_esphomelib._tcp.local. 60 IN PTR room._esphomelib._tcp.local.",
			"room._esphomelib._tcp.local. 60 IN SRV 0 0 6053 room.local.",
		); err != nil {
			peerErr <- err
			return
		}
		if err := send(
			`room._esphomelib._tcp.local. 60 IN TXT "mac=aabbccddeeff" "api_encryption=1"`,
			"room.local. 60 IN A 192.0.2.10",
		); err != nil {
			peerErr <- err
			return
		}
		select {
		case <-update:
		case <-ctx.Done():
			peerErr <- ctx.Err()
			return
		}
		// A refresh of the same RRsets must not create a duplicate before the update.
		if refreshErr := send(
			"room._esphomelib._tcp.local. 60 IN SRV 0 0 6053 room.local.",
			"room.local. 60 IN A 192.0.2.10",
		); refreshErr != nil {
			peerErr <- refreshErr
			return
		}
		peerErr <- send("room._esphomelib._tcp.local. 60 IN SRV 0 0 6054 newroom.local.", "newroom.local. 60 IN A 192.0.2.11")
	}()
	mac, setupErr := esphome.ParseMAC("aabbccddeeff")
	require.NoError(t, setupErr)
	want := esphome.DiscoveryRecord{MAC: mac, Address: "192.0.2.10", Port: 6053, Encrypted: true}
	select {
	case got := <-out:
		require.Equal(t, want, got)
	case peerFailure := <-peerErr:
		t.Fatalf("peer: %v", peerFailure)
	case <-time.After(5 * time.Second):
		t.Fatal("initial discovery timed out")
	}
	close(update)
	want.Address, want.Port = "192.0.2.11", 6054
	select {
	case got := <-out:
		require.Equal(t, want, got)
	case <-time.After(5 * time.Second):
		t.Fatal("endpoint update timed out")
	}
	require.NoError(t, <-peerErr)
	cancel()
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not join")
	}
	_, _, setupErr = client.ReadFromUDP(make([]byte, 1))
	require.ErrorIs(t, setupErr, net.ErrClosed)
	select {
	case _, ok := <-out:
		require.True(t, ok, "browser closed caller channel")
	default:
	}
}

func TestBrowseRetransmissionIncludesKnownAnswer(t *testing.T) {
	t.Parallel()
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan esphome.DiscoveryRecord, 2)
	done := make(chan error, 1)
	go func() {
		done <- runSockets(ctx, out, []browseSocket{{conn: client, group: peer.LocalAddr().(*net.UDPAddr), scope: "loopback"}}, 100*time.Millisecond)
	}()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 8192)
	_, from, err := peer.ReadFromUDP(buf)
	require.NoError(t, err)
	var reply dns.Msg
	reply.Response = true
	reply.Answer = []dns.RR{
		rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local."),
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"),
	}
	data, err := reply.Pack()
	require.NoError(t, err)
	_, err = peer.WriteToUDP(data, from)
	require.NoError(t, err)
	select {
	case record := <-out:
		require.Equal(t, "192.0.2.10", record.Address)
	case <-time.After(5 * time.Second):
		t.Fatal("discovery timed out")
	}
	n, _, err := peer.ReadFromUDP(buf)
	require.NoError(t, err)
	var query dns.Msg
	require.NoError(t, query.Unpack(buf[:n]))
	require.Len(t, query.Question, 1)
	require.Equal(t, serviceName, query.Question[0].Name)
	require.Equal(t, dns.TypePTR, query.Question[0].Qtype)
	require.Len(t, query.Answer, 1)
	require.Equal(t, "room._esphomelib._tcp.local.", query.Answer[0].(*dns.PTR).Ptr)
	require.Greater(t, query.Answer[0].Header().Ttl, uint32(60))
	cancel()
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not join")
	}
}

// The peer drops the first resolution questions and answers only their retries.
func TestBrowserRetriesLostResolutionWhileBrowseIsBackedOff(t *testing.T) {
	t.Parallel()
	peer, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	t.Cleanup(func() { _ = peer.Close() })
	client, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	out := make(chan esphome.DiscoveryRecord, 2)
	done := make(chan error, 1)
	go func() {
		done <- runSockets(ctx, out, []browseSocket{{conn: client, group: peer.LocalAddr().(*net.UDPAddr), scope: "loopback"}}, time.Hour)
	}()
	require.NoError(t, peer.SetReadDeadline(time.Now().Add(5*time.Second)))
	buf := make([]byte, 8192)
	n, from, err := peer.ReadFromUDP(buf)
	require.NoError(t, err)
	var first dns.Msg
	require.NoError(t, first.Unpack(buf[:n]))
	require.Len(t, first.Question, 1)
	require.Equal(t, dns.TypePTR, first.Question[0].Qtype)
	response := &dns.Msg{Answer: []dns.RR{rr(t, "_esphomelib._tcp.local. 120 IN PTR room._esphomelib._tcp.local.")}}
	response.Response = true
	data, err := response.Pack()
	require.NoError(t, err)
	_, err = peer.WriteToUDP(data, from)
	require.NoError(t, err)
	counts := map[uint16]int{}
	for len(counts) < 2 {
		n, _, err = peer.ReadFromUDP(buf)
		require.NoError(t, err)
		var question dns.Msg
		require.NoError(t, question.Unpack(buf[:n]))
		require.Len(t, question.Question, 1)
		typ := question.Question[0].Qtype
		require.Contains(t, []uint16{dns.TypeSRV, dns.TypeTXT}, typ)
		counts[typ]++
	}
	// A duplicate PTR advertisement does not itself trigger resolution again.
	_, err = peer.WriteToUDP(data, from)
	require.NoError(t, err)
	for counts[dns.TypeSRV] < 2 || counts[dns.TypeTXT] < 2 {
		n, _, err = peer.ReadFromUDP(buf)
		require.NoError(t, err)
		var question dns.Msg
		require.NoError(t, question.Unpack(buf[:n]))
		require.Len(t, question.Question, 1)
		typ := question.Question[0].Qtype
		require.Contains(t, []uint16{dns.TypeSRV, dns.TypeTXT}, typ)
		counts[typ]++
	}
	response.Answer = []dns.RR{
		rr(t, "room._esphomelib._tcp.local. 120 IN SRV 0 0 6053 room.local."),
		rr(t, `room._esphomelib._tcp.local. 120 IN TXT "mac=aabbccddeeff"`),
		rr(t, "room.local. 120 IN A 192.0.2.10"),
	}
	data, err = response.Pack()
	require.NoError(t, err)
	_, err = peer.WriteToUDP(data, from)
	require.NoError(t, err)
	select {
	case record := <-out:
		require.Equal(t, "192.0.2.10", record.Address)
		require.Equal(t, uint16(6053), record.Port)
	case <-time.After(5 * time.Second):
		t.Fatal("retry response did not resolve the service")
	}
	cancel()
	select {
	case runErr := <-done:
		require.NoError(t, runErr)
	case <-time.After(5 * time.Second):
		t.Fatal("browser did not join")
	}
}
