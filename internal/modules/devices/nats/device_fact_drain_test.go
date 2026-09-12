package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// stallingNATSPeer forwards one client connection to a real NATS server until
// the test stalls it. A stalled peer stops draining the client, so the client's
// socket writes block exactly as they do behind an unread peer.
type stallingNATSPeer struct {
	address   string
	stalled   atomic.Bool
	forwarded atomic.Int64
	listener  net.Listener
	mutex     sync.Mutex
	peers     []net.Conn
}

// startStallingNATSPeer listens on a loopback port and forwards to target.
func startStallingNATSPeer(t *testing.T, target string) *stallingNATSPeer {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	peer := &stallingNATSPeer{address: listener.Addr().String(), listener: listener}
	t.Cleanup(func() {
		_ = listener.Close()
		peer.mutex.Lock()
		defer peer.mutex.Unlock()
		for _, connection := range peer.peers {
			_ = connection.Close()
		}
	})
	go peer.serve(target)
	return peer
}

func (peer *stallingNATSPeer) serve(target string) {
	for {
		client, err := peer.listener.Accept()
		if err != nil {
			return
		}
		server, dialErr := net.Dial("tcp", target)
		if dialErr != nil {
			_ = client.Close()
			continue
		}
		// Small socket buffers make the stalled write deterministic and fast
		// instead of depending on one machine's autotuned TCP buffers.
		if tcp, ok := client.(*net.TCPConn); ok {
			_ = tcp.SetReadBuffer(4096)
		}
		peer.mutex.Lock()
		peer.peers = append(peer.peers, client, server)
		peer.mutex.Unlock()
		go peer.forwardClientToServer(client, server)
		go func() { _, _ = io.Copy(client, server) }()
	}
}

// forwardClientToServer relays client bytes until the peer is stalled, then
// stops draining the client so its socket buffer fills.
func (peer *stallingNATSPeer) forwardClientToServer(client, server net.Conn) {
	buffer := make([]byte, 4096)
	for {
		read, err := client.Read(buffer)
		if err != nil {
			return
		}
		for peer.stalled.Load() {
			time.Sleep(time.Millisecond)
		}
		if _, writeErr := server.Write(buffer[:read]); writeErr != nil {
			return
		}
		peer.forwarded.Add(int64(read))
	}
}

func (peer *stallingNATSPeer) stall() { peer.stalled.Store(true) }

// resume lets the peer drain the client again, which releases a write that was
// blocked while the peer was stalled.
func (peer *stallingNATSPeer) resume() { peer.stalled.Store(false) }

func (peer *stallingNATSPeer) forwardedBytes() int64 { return peer.forwarded.Load() }

// smallSendBufferDialer dials real TCP connections with a small send buffer,
// so a stalled peer fills the client's socket promptly.
type smallSendBufferDialer struct{ dialer net.Dialer }

func (dialer *smallSendBufferDialer) Dial(network, address string) (net.Conn, error) {
	connection, err := dialer.dialer.Dial(network, address)
	if err != nil {
		return nil, err
	}
	if tcp, ok := connection.(*net.TCPConn); ok {
		_ = tcp.SetWriteBuffer(4096)
	}
	return connection, nil
}

// Device Fact drain harness constants: the stalled shared connection test
// publishes one payload far larger than the stalled peer's socket buffers, well
// under the broker's default maximum payload.
const (
	deviceFactStallSubject      = "hearth.test.shared_connection_stall"
	deviceFactStallPayloadBytes = 512 * 1024
)

// TestDeviceFactConnectionBoundsSocketWrites protects the connection option
// that makes every close and every synchronous Conn call bounded: the dedicated
// fact connection must bound each socket write well inside the Core shutdown
// budget. The shared Core connection needs the same bound and is asserted where
// it is assembled, by TestCoreNATSConnectionBoundsSocketWrites in
// internal/app/hearthd, together with the reconnect buffering it keeps.
func TestDeviceFactConnectionBoundsSocketWrites(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	factConnection := connectDeviceFactClient(t, server.ClientURL(), DeviceFactConnectionOptions()...)

	if factConnection.Opts.FlusherTimeout != CoreNATSWriteTimeout {
		t.Fatalf(
			"fact connection FlusherTimeout = %s, want %s",
			factConnection.Opts.FlusherTimeout, CoreNATSWriteTimeout,
		)
	}
	if CoreNATSWriteTimeout >= 3*time.Second {
		t.Fatalf(
			"CoreNATSWriteTimeout = %s, want a bound far inside the five-second shutdown budget",
			CoreNATSWriteTimeout,
		)
	}
}

// TestDeviceFactDispatcherAbortsAStalledDrainWithinTheWriteTimeout protects the
// bounded drain abort end to end with a real stalled socket: a publication
// blocked inside a socket write holds the nats.go connection mutex, so the
// abort can close the connection only after the write deadline releases it.
// Without a bounded fact write timeout the default one-minute deadline would
// hold Drain far past its context.
func TestDeviceFactDispatcherAbortsAStalledDrainWithinTheWriteTimeout(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	peer := startStallingNATSPeer(t, server.Addr().String())
	// The ingest side stays directly connected, so the epoch window is live and
	// the queued facts reach the worker instead of being filtered at enqueue.
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(
		t, "nats://"+peer.address,
		append(
			DeviceFactConnectionOptions(),
			natsgo.SetCustomDialer(&smallSendBufferDialer{}),
			// The stalled write reports the expected socket timeout; keep the
			// nats.go default async error handler from writing it to stderr.
			natsgo.ErrorHandler(func(*natsgo.Conn, *natsgo.Subscription, error) {}),
		)...,
	)
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := StartDeviceFactDispatcher(publish, validator, epochs, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 4*CoreNATSWriteTimeout)
		defer cancelDrain()
		_ = dispatcher.Drain(drainContext)
	})
	// Stall before enqueueing, so every admitted fact is written behind a peer
	// that never drains the client again.
	peer.stall()
	now := time.Now().UTC().Add(time.Millisecond)
	dispatcher.now = func() time.Time { return now }
	fact := observationFact(deviceFactTestEntityID, now)
	fact.Value = devices.Value(`"` + strings.Repeat("a", 4096) + `"`)
	for range DeviceFactPendingMessages {
		dispatcher.ObservationAccepted(context.Background(), fact)
	}
	admitted := dispatcher.outstanding()
	if admitted == 0 {
		t.Fatal("no fact was admitted, so the stalled write was never exercised")
	}
	admittedBytes := int64(admitted) * 4096

	// The peer's forwarded byte count plateaus once the client's socket is
	// full, which is the direct evidence that a publication is blocked in write
	// while holding the connection mutex.
	waitForStalledForwarding(t, peer)
	if forwarded := peer.forwardedBytes(); forwarded >= admittedBytes/4 {
		t.Fatalf(
			"the peer drained %d of the roughly %d admitted bytes, so no write stalled",
			forwarded, admittedBytes,
		)
	}

	// The drain context is already expired, so Drain goes straight to its abort
	// path: close the connection, discard the queue and join the worker.
	canceled, cancelDrain := context.WithCancel(context.Background())
	cancelDrain()
	drained := make(chan error, 1)
	startDrain := time.Now()
	go func() { drained <- dispatcher.Drain(canceled) }()
	select {
	case drainErr := <-drained:
		if !errors.Is(drainErr, context.Canceled) {
			t.Fatalf("stalled Drain = %v, want context.Canceled", drainErr)
		}
		t.Logf("stalled drain aborted in %s", time.Since(startDrain))
	case <-time.After(3 * time.Second):
		t.Fatalf(
			"Drain did not return within 3s; a stalled write held the abort far past the %s write timeout",
			CoreNATSWriteTimeout,
		)
	}
	select {
	case <-dispatcher.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("the publication worker did not exit after the stalled abort")
	}
	if dispatcher.Active() {
		t.Fatal("dispatcher stayed active after the stalled abort")
	}
	if !publish.IsClosed() {
		t.Fatal("the abort did not close the dedicated fact connection")
	}
	if got := dispatcher.outstanding(); got != 0 {
		t.Fatalf("outstanding facts after the stalled abort = %d, want 0", got)
	}
}

// waitForStalledForwarding waits until the peer stops forwarding new client
// bytes, which proves the client's socket write is blocked.
func waitForStalledForwarding(t *testing.T, peer *stallingNATSPeer) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	previous := peer.forwardedBytes()
	stableSince := time.Now()
	for time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
		current := peer.forwardedBytes()
		if current != previous {
			previous = current
			stableSince = time.Now()
			continue
		}
		if time.Since(stableSince) >= 200*time.Millisecond {
			return
		}
	}
	t.Fatal("the peer never stopped forwarding client bytes")
}

// waitForHeldConnectionMutex waits until a synchronous Conn call cannot
// complete, which proves that a stalled socket write holds the connection's
// mutex. The probe performs the same nats.Conn read the dispatcher worker
// performs before every publication, so it observes exactly the standoff under
// test instead of inferring it from timing.
func waitForHeldConnectionMutex(t *testing.T, connection *natsgo.Conn) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		probe := make(chan struct{})
		go func() {
			connection.Stats()
			close(probe)
		}()
		select {
		case <-probe:
			// The mutex was free, so the stalled write had not reached its
			// socket yet. Let the writing goroutine make progress and re-probe.
			time.Sleep(5 * time.Millisecond)
		case <-time.After(50 * time.Millisecond):
			return
		}
	}
	t.Fatal("the connection mutex was never held by a stalled write")
}

// TestDeviceFactDispatcherDrainsWithAStalledSharedConnection protects the
// remaining drain standoff. The worker's per-fact freshness check reads
// IsConnected and Stats on the shared ingest connection, and pinned nats.go
// v1.53.1 holds that connection's mutex across a socket write. With nats.go's
// one-minute default write deadline a stalled shared connection holds the mutex
// for a minute, so closing only the dedicated fact connection cannot release a
// worker blocked there and Drain outlasts its context by roughly a minute. Here
// the shared connection stalls behind a real unread peer with the production
// write bound: the fact is enqueued while a stalled write demonstrably holds
// the mutex, and Drain must abort inside a couple of bounded write timeouts.
func TestDeviceFactDispatcherDrainsWithAStalledSharedConnection(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	peer := startStallingNATSPeer(t, server.Addr().String())
	// Resuming the peer releases any write still blocked on it, so no cleanup
	// waits for a socket deadline.
	defer peer.resume()
	// The shared ingest connection runs through the unread peer with the
	// production write bound; the dedicated fact connection stays healthy, so
	// the shared connection's mutex is the only thing that can hold the worker.
	ingest := connectDeviceFactClient(
		t, "nats://"+peer.address,
		natsgo.FlusherTimeout(CoreNATSWriteTimeout),
		natsgo.SetCustomDialer(&smallSendBufferDialer{}),
		// The stalled write reports the expected socket timeout; keep the
		// nats.go default async error handler from writing it to stderr.
		natsgo.ErrorHandler(func(*natsgo.Conn, *natsgo.Subscription, error) {}),
	)
	publish := connectDeviceFactClient(t, server.ClientURL(), DeviceFactConnectionOptions()...)

	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	dispatcher, err := StartDeviceFactDispatcher(publish, validator, epochs, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		drainContext, cancelDrain := context.WithTimeout(context.Background(), 4*CoreNATSWriteTimeout)
		defer cancelDrain()
		_ = dispatcher.Drain(drainContext)
	})

	// One publication larger than the stalled peer's socket buffers blocks
	// inside Conn.publish, which holds the connection mutex while it writes. It
	// runs off the test goroutine because that write occupies the publisher for
	// the connection's whole write deadline.
	peer.stall()
	stalledWrite := make(chan error, 1)
	writeStarted := time.Now()
	go func() {
		stalledWrite <- ingest.Publish(deviceFactStallSubject, bytes.Repeat([]byte("s"), deviceFactStallPayloadBytes))
	}()
	waitForHeldConnectionMutex(t, ingest)

	// The fact is admitted while the shared connection's mutex is held, so the
	// worker's freshness check for it cannot complete until the write deadline.
	now := time.Now().UTC().Add(time.Millisecond)
	dispatcher.now = func() time.Time { return now }
	dispatcher.ObservationAccepted(context.Background(), observationFact(deviceFactTestEntityID, now))
	if dispatcher.outstanding() == 0 {
		t.Fatal("no fact was admitted, so the worker never reached its freshness check")
	}

	// The drain context is already expired, so Drain goes straight to its abort
	// path: close the dedicated connection, discard the queue and join the worker.
	canceled, cancelDrain := context.WithCancel(context.Background())
	cancelDrain()
	drained := make(chan error, 1)
	startDrain := time.Now()
	go func() { drained <- dispatcher.Drain(canceled) }()
	select {
	case drainErr := <-drained:
		if !errors.Is(drainErr, context.Canceled) {
			t.Fatalf("stalled Drain = %v, want context.Canceled", drainErr)
		}
		t.Logf("drain aborted in %s while the shared connection was stalled", time.Since(startDrain))
	case <-time.After(3 * time.Second):
		t.Fatalf(
			"Drain did not return within 3s; the stalled shared connection held the worker past the %s write timeout",
			CoreNATSWriteTimeout,
		)
	}
	select {
	case publishErr := <-stalledWrite:
		if publishErr == nil {
			t.Fatal("the stalled shared connection write completed instead of timing out")
		}
		t.Logf(
			"the bounded shared connection write failed after %s: %v",
			time.Since(writeStarted), publishErr,
		)
	case <-time.After(3 * time.Second):
		t.Fatal("the stalled shared connection write did not fail inside the bounded write timeout")
	}
	select {
	case <-dispatcher.Closed():
	case <-time.After(3 * time.Second):
		t.Fatal("the publication worker did not exit after the stalled abort")
	}
	if dispatcher.Active() {
		t.Fatal("dispatcher stayed active after the stalled abort")
	}
	if !publish.IsClosed() {
		t.Fatal("the abort did not close the dedicated fact connection")
	}
	if got := dispatcher.outstanding(); got != 0 {
		t.Fatalf("outstanding facts after the stalled abort = %d, want 0", got)
	}
}

// TestDeviceFactDispatcherDropsQueuedFactsWhenTheGenerationChanges protects the
// worker-stage generation gate: a fact already admitted under the current
// connection generation must be suppressed, not published, once that generation
// changes before the worker reaches it. Removing or moving the worker-stage
// check fails this test, because the queued fact would then be published into
// the new generation.
func TestDeviceFactDispatcherDropsQueuedFactsWhenTheGenerationChanges(t *testing.T) {
	t.Parallel()
	fixture := newDeviceFactFixture(t)
	now := time.Now().UTC().Add(time.Second)
	fixture.dispatcher.now = func() time.Time { return now }
	entered := make(chan struct{})
	release := make(chan struct{})
	var attempts atomic.Int64
	realPublish := fixture.dispatcher.publish
	fixture.dispatcher.publish = func(subject string, headers natsgo.Header, payload []byte) error {
		if attempts.Add(1) == 1 {
			close(entered)
			<-release
		}
		return realPublish(subject, headers, payload)
	}
	ctx := context.Background()
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("the worker never entered its first publication")
	}
	// This fact is admitted while the fence still reports the current
	// generation, but the worker cannot reach it until the first publication
	// returns.
	fixture.dispatcher.ObservationAccepted(ctx, observationFact(deviceFactTestEntityID, now))
	// The publication connection reconnects while the worker is stalled: the
	// cached generation advances and the connection's own reconnect count no
	// longer matches the queued generation.
	fixture.epochs.PublishConnected(NATSConnectionGeneration{Reconnects: 1}, now.Add(time.Second))
	close(release)

	if err := fixture.dispatcher.Drain(ctx); err != nil {
		t.Fatal(err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("publication attempts = %d, want only the already in-flight fact", got)
	}
	suppressed := logEvents(fixture.logs.records(t), "device_fact.suppressed")
	if len(suppressed) != 1 || suppressed[0]["reason"] != deviceFactReasonGenerationChange {
		t.Fatalf("queued-fact diagnostics = %#v, want one generation_changed suppression", suppressed)
	}
	if suppressed[0]["family"] != string(natswire.DeviceFactFamilyObservation) {
		t.Fatalf("suppression family = %#v, want observation", suppressed[0]["family"])
	}
	// Exactly the already in-flight fact reached the subscriber; the queued
	// fact was suppressed instead of entering the new generation.
	published := fixture.nextFact(t)
	if _, route := decodeFact(t, fixture.validator, published); route.Variant != natswire.ObservationFactApplied {
		t.Fatalf("published fact route = %#v, want an applied Observation", route)
	}
	fixture.assertNoFact(t)
}
