package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"net"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
)

const (
	// deviceFactTestEntityID is a canonical Entity identity used by the fact
	// transport fixtures.
	deviceFactTestEntityID = "ent_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	// deviceFactTestObservationID is a canonical Observation identity.
	deviceFactTestObservationID = "obs_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	// deviceFactTestEventID is a canonical Entity Event identity.
	deviceFactTestEventID = "evt_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	// deviceFactTestCommandID is a canonical Command identity.
	deviceFactTestCommandID = "cmd_01890f47-7a6b-7c4d-8e9f-0123456789ab"
)

// startDeviceFactServer starts one plain NATS server on the requested port; -1
// selects a free port. A caller can restart a stopped server on the same
// explicit port to drive real reconnects.
func startDeviceFactServer(t *testing.T, port int) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: port, NoSigs: true, NoLog: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func deviceFactServerPort(t *testing.T, server *natsserver.Server) int {
	t.Helper()
	address, ok := server.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("NATS server address = %#v", server.Addr())
	}
	return address.Port
}

// connectDeviceFactClient connects one test peer with an unlimited reconnect
// policy and a short wait so a restart is observed promptly.
func connectDeviceFactClient(t *testing.T, url string, options ...natsgo.Option) *natsgo.Conn {
	t.Helper()
	base := []natsgo.Option{
		natsgo.MaxReconnects(-1),
		natsgo.ReconnectWait(10 * time.Millisecond),
	}
	connection, err := natsgo.Connect(url, append(base, options...)...)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	return connection
}

// waitForDeviceFactCondition polls one bounded condition. It is a liveness
// wait for asynchronous NATS lifecycle callbacks, never an ordering oracle.
func waitForDeviceFactCondition(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	if condition() {
		return
	}
	t.Fatalf("timed out waiting for %s", what)
}

// TestDeviceFactEpochsCombinedWindow covers the fence's pure state machine: both
// sides must be live, the returned epoch is the later established one, and
// either side disconnecting closes the window.
func TestDeviceFactEpochsCombinedWindow(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(t, server.ClientURL())
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()

	initial, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{})
	if !live || initial.IsZero() {
		t.Fatalf("initial LiveSince = %v, %v, want a live window", initial, live)
	}

	firstEpoch := time.Now().UTC().Add(-time.Minute)
	secondEpoch := firstEpoch.Add(time.Second)
	epochs.IngestConnected(NATSConnectionGeneration{}, firstEpoch)
	epochs.PublishConnected(NATSConnectionGeneration{}, secondEpoch)
	combined, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{})
	if !live || !combined.Equal(secondEpoch) {
		t.Fatalf("combined LiveSince = %v, %v, want %v", combined, live, secondEpoch)
	}

	epochs.PublishDisconnected()
	if _, stillLive := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); stillLive {
		t.Fatal("LiveSince stayed live after the publisher disconnected")
	}
	if snapshot := epochs.Snapshot(); snapshot.Live {
		t.Fatalf("Snapshot stayed live after the publisher disconnected: %#v", snapshot)
	}

	thirdEpoch := secondEpoch.Add(time.Second)
	epochs.PublishConnected(NATSConnectionGeneration{}, thirdEpoch)
	combined, live = epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{})
	if !live || !combined.Equal(thirdEpoch) {
		t.Fatalf("reconnected LiveSince = %v, %v, want %v", combined, live, thirdEpoch)
	}

	epochs.IngestDisconnected()
	if _, stillLive := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); stillLive {
		t.Fatal("LiveSince stayed live after the ingest connection disconnected")
	}
}

// TestDeviceFactEpochsSnapshotIsPureMemory proves the enqueue-stage snapshot
// never consults a connection: a snapshot taken while both connections are
// closed still reports the cached window, while LiveSince does not.
func TestDeviceFactEpochsSnapshotIsPureMemory(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(t, server.ClientURL())
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Mark the window without attaching lifecycle callbacks, so closing the
	// connections cannot change the cached state.
	initialEpoch := time.Now().UTC()
	epochs.IngestConnected(NATSConnectionGeneration{}, initialEpoch)
	epochs.PublishConnected(NATSConnectionGeneration{}, initialEpoch)
	if !epochs.Snapshot().Live {
		t.Fatal("snapshot was not live after the initial connect")
	}
	ingest.Close()
	publish.Close()
	if !epochs.Snapshot().Live {
		t.Fatal("snapshot consulted the connections instead of the cached state")
	}
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); live {
		t.Fatal("LiveSince reported a live window with both connections closed")
	}
}

// TestDeviceFactEpochsRejectsMissingConnections fails construction when either
// side of the fact path is absent.
func TestDeviceFactEpochsRejectsMissingConnections(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	connection := connectDeviceFactClient(t, server.ClientURL())
	if _, err := NewDeviceFactEpochs(nil, connection, nil); err == nil {
		t.Fatal("NewDeviceFactEpochs accepted a nil ingest connection")
	}
	if _, err := NewDeviceFactEpochs(connection, nil, nil); err == nil {
		t.Fatal("NewDeviceFactEpochs accepted a nil publication connection")
	}
}

// TestDeviceFactEpochsReconnectEstablishesANewGeneration protects the
// generation fence: after a real reconnect both handlers establish the new
// generation and epoch, the old generation is refused, and the new boundary is
// the reconnect time.
func TestDeviceFactEpochsReconnectEstablishesANewGeneration(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(t, server.ClientURL())
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); !live {
		t.Fatal("initial window was not live")
	}
	port := deviceFactServerPort(t, server)
	server.Shutdown()
	server.WaitForShutdown()
	waitForDeviceFactCondition(t, "both connections to disconnect", func() bool {
		return !epochs.Snapshot().Live
	})

	restartAt := time.Now().UTC().Add(-time.Second)
	startDeviceFactServer(t, port)
	waitForDeviceFactCondition(t, "both connections to reconnect", func() bool {
		return ingest.IsConnected() && publish.IsConnected() &&
			ingest.Stats().Reconnects >= 1 && publish.Stats().Reconnects >= 1
	})
	// The reconnect handlers are asynchronous; the window stays closed until
	// they have established the new generation.
	waitForDeviceFactCondition(t, "the reconnect handlers to establish a live window", func() bool {
		_, live := epochs.LiveSince(
			NATSConnectionGeneration{Reconnects: 1}, NATSConnectionGeneration{Reconnects: 1},
		)
		return live
	})
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); live {
		t.Fatal("the pre-reconnect generation was still live after a real reconnect")
	}
	liveSince, live := epochs.LiveSince(
		NATSConnectionGeneration{Reconnects: 1}, NATSConnectionGeneration{Reconnects: 1},
	)
	if !live || liveSince.Before(restartAt) {
		t.Fatalf("reconnect LiveSince = %v, %v, want a boundary at or after the restart", liveSince, live)
	}
}

// TestDeviceFactEpochsGenerationMismatchSuppressesWithDelayedCallbacks protects
// the conservative mismatch rule: when a reconnect happened but a lifecycle
// callback has not established the new generation, the old generation is
// refused and the unestablished new one is refused too.
func TestDeviceFactEpochsGenerationMismatchSuppressesWithDelayedCallbacks(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	ingest := connectDeviceFactClient(t, server.ClientURL())
	publish := connectDeviceFactClient(t, server.ClientURL())
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Deliberately avoid Track: the fence is marked once and no reconnect
	// callback advances it, modelling a reconnect whose callbacks are late.
	initialEpoch := time.Now().UTC()
	epochs.IngestConnected(NATSConnectionGeneration{}, initialEpoch)
	epochs.PublishConnected(NATSConnectionGeneration{}, initialEpoch)
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); !live {
		t.Fatal("initial window was not live")
	}

	port := deviceFactServerPort(t, server)
	server.Shutdown()
	server.WaitForShutdown()
	startDeviceFactServer(t, port)
	waitForDeviceFactCondition(t, "both connections to reconnect", func() bool {
		return ingest.IsConnected() && publish.IsConnected() &&
			ingest.Stats().Reconnects >= 1 && publish.Stats().Reconnects >= 1
	})
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); live {
		t.Fatal("the stale generation stayed live while the reconnect callbacks were delayed")
	}
	if _, live := epochs.LiveSince(
		NATSConnectionGeneration{Reconnects: 1}, NATSConnectionGeneration{Reconnects: 1},
	); live {
		t.Fatal("an unestablished generation was accepted")
	}
}

// TestDeviceFactEpochsTrackPreservesExistingHandlers proves the fence chains
// connection diagnostics instead of replacing them.
func TestDeviceFactEpochsTrackPreservesExistingHandlers(t *testing.T) {
	t.Parallel()
	server := startDeviceFactServer(t, -1)
	disconnects := make(chan struct{}, 1)
	reconnects := make(chan struct{}, 1)
	ingest := connectDeviceFactClient(t, server.ClientURL(), natsgo.DisconnectErrHandler(
		func(*natsgo.Conn, error) { disconnects <- struct{}{} },
	))
	publish := connectDeviceFactClient(t, server.ClientURL(), natsgo.ReconnectHandler(
		func(*natsgo.Conn) { reconnects <- struct{}{} },
	))
	epochs, err := NewDeviceFactEpochs(ingest, publish, nil)
	if err != nil {
		t.Fatal(err)
	}
	epochs.Track()
	port := deviceFactServerPort(t, server)
	server.Shutdown()
	server.WaitForShutdown()
	select {
	case <-disconnects:
	case <-time.After(5 * time.Second):
		t.Fatal("Track replaced the existing disconnect handler")
	}
	startDeviceFactServer(t, port)
	select {
	case <-reconnects:
	case <-time.After(5 * time.Second):
		t.Fatal("Track replaced the existing reconnect handler")
	}
}

func TestDeviceFactEpochsLiveSinceOnNilReceiver(t *testing.T) {
	t.Parallel()
	var epochs *DeviceFactEpochs
	if _, live := epochs.LiveSince(NATSConnectionGeneration{}, NATSConnectionGeneration{}); live {
		t.Fatal("nil epoch fence reported a live window")
	}
	if snapshot := epochs.Snapshot(); snapshot.Live {
		t.Fatal("nil epoch fence reported a live snapshot")
	}
}
