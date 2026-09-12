package nats

import (
	"errors"
	"sync"
	"time"

	natsgo "github.com/nats-io/nats.go"
)

// NATSConnectionGeneration identifies one live generation of one NATS
// connection by its cumulative reconnect count. Zero is the initial successful
// connect; every later successful reconnect advances it. A generation is the
// connection identity the fact freshness fence compares, because callback
// timing alone cannot prove that a connection is still the one a queued fact
// was observed under.
//
//nolint:revive // The Device Fact contract fixes this public name, so nats.NATSConnectionGeneration is intended.
type NATSConnectionGeneration struct {
	Reconnects uint64
}

// connectionEpoch is one connection's tracked state: whether the last observed
// lifecycle event left it connected, the generation that event established and
// the Core time the connection entered that generation. A zero epoch time
// means no generation was ever established.
type connectionEpoch struct {
	connected  bool
	generation NATSConnectionGeneration
	epoch      time.Time
}

// DeviceFactEpochSnapshot is the cached freshness the enqueue stage reads. It
// is a pure in-memory copy: reading a snapshot never touches a NATS
// connection, so a [devices.DeviceFactSink] method can consult it without
// making any nats.Conn call.
type DeviceFactEpochSnapshot struct {
	Live              bool
	LiveSince         time.Time
	IngestGeneration  NATSConnectionGeneration
	PublishGeneration NATSConnectionGeneration
	IngestConnected   bool
	PublishConnected  bool
}

// DeviceFactEpochs is the concurrency-safe combined freshness fence shared by
// the Core ingest connection and the dedicated Device Fact publication
// connection. It records each connection's observed reconnect generation and
// the UTC epoch that generation established, so a report stored before the
// current live window can never be published as a live fact after a Core
// restart, an ingest reconnect or a publication reconnect.
//
// LiveSince is the authoritative check. It reads both connections' synchronous
// connectivity and reconnect counters before taking its own mutex, so no NATS
// connection method is ever called while the epoch mutex is held, and each read
// is bounded by the connections' own write deadline.
type DeviceFactEpochs struct {
	mutex   sync.Mutex
	ingest  *natsgo.Conn
	publish *natsgo.Conn
	now     func() time.Time

	ingestEpoch  connectionEpoch
	publishEpoch connectionEpoch
}

// NewDeviceFactEpochs returns the freshness fence for one ingest connection and
// one publication connection. Both connections are required: a fence that
// cannot observe both sides of the fact path would admit backlog.
func NewDeviceFactEpochs(
	ingest *natsgo.Conn,
	publish *natsgo.Conn,
	now func() time.Time,
) (*DeviceFactEpochs, error) {
	if ingest == nil || publish == nil {
		return nil, errors.New("device fact epochs require an ingest and a publication connection")
	}
	if now == nil {
		now = time.Now
	}
	return &DeviceFactEpochs{ingest: ingest, publish: publish, now: now}, nil
}

// Track marks both connections' initial generations and keeps the fence current
// from their disconnect and reconnect callbacks. Each connection's existing
// diagnostics handler is preserved: the fence closes or advances its epoch
// first and then calls the handler the connection already had, because closing
// the live window early is always safe while delaying it is not.
func (epochs *DeviceFactEpochs) Track() {
	epochs.trackConnection(epochs.ingest, epochs.IngestConnected, epochs.IngestDisconnected)
	epochs.trackConnection(epochs.publish, epochs.PublishConnected, epochs.PublishDisconnected)
}

// IngestConnected establishes the ingest connection's current generation at
// the supplied Core time. Initial connects and reconnect handlers both use it.
func (epochs *DeviceFactEpochs) IngestConnected(generation NATSConnectionGeneration, at time.Time) {
	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	epochs.ingestEpoch = connectionEpoch{connected: true, generation: generation, epoch: at.UTC()}
}

// IngestDisconnected closes the ingest side of the live window for diagnostics.
// Correctness does not depend on this callback arriving before a reconnect: the
// worker stage compares current reconnect counts as well.
func (epochs *DeviceFactEpochs) IngestDisconnected() {
	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	epochs.ingestEpoch.connected = false
}

// PublishConnected establishes the publication connection's current generation.
func (epochs *DeviceFactEpochs) PublishConnected(generation NATSConnectionGeneration, at time.Time) {
	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	epochs.publishEpoch = connectionEpoch{connected: true, generation: generation, epoch: at.UTC()}
}

// PublishDisconnected closes the publication side of the live window.
func (epochs *DeviceFactEpochs) PublishDisconnected() {
	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	epochs.publishEpoch.connected = false
}

// Snapshot returns the cached live window without touching either connection.
// It is the enqueue-stage fast filter: the sink reads generations and the
// cached epoch from it, and the worker stage revalidates against the actual
// connections.
func (epochs *DeviceFactEpochs) Snapshot() DeviceFactEpochSnapshot {
	if epochs == nil {
		return DeviceFactEpochSnapshot{}
	}
	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	ingest := epochs.ingestEpoch
	publish := epochs.publishEpoch
	liveSince, live := combinedLiveEpoch(ingest, publish)
	return DeviceFactEpochSnapshot{
		Live:              live,
		LiveSince:         liveSince,
		IngestGeneration:  ingest.generation,
		PublishGeneration: publish.generation,
		IngestConnected:   ingest.connected,
		PublishConnected:  publish.connected,
	}
}

// LiveSince reports the current combined live epoch for work observed under the
// supplied generations. It returns true only when both connections are
// connected right now, each connection's current reconnect count still equals
// the generation the fence established and the supplied generation, and both
// established epochs are nonzero. A reconnect-count mismatch closes the window
// conservatively even when the asynchronous reconnect callback has not run yet,
// and the returned time is the later of the two established epochs.
//
// Both reads reach the connection's own mutex, so both connections must bound
// their socket writes (CoreNATSWriteTimeout): a stalled write on either
// connection would otherwise hold that mutex — and therefore this check, the
// worker that calls it and dispatcher Drain — for up to nats.go's one-minute
// default.
func (epochs *DeviceFactEpochs) LiveSince(
	ingestGeneration NATSConnectionGeneration,
	publishGeneration NATSConnectionGeneration,
) (time.Time, bool) {
	if epochs == nil {
		return time.Time{}, false
	}
	// Read live connection state before taking the epoch mutex: nats.Conn
	// methods take the connection's own lock, and the fence must never call
	// them while holding the epoch mutex.
	ingestConnected, ingestReconnects := connectionLiveness(epochs.ingest)
	publishConnected, publishReconnects := connectionLiveness(epochs.publish)

	epochs.mutex.Lock()
	defer epochs.mutex.Unlock()
	ingest := epochs.ingestEpoch
	publish := epochs.publishEpoch
	if !ingestConnected || !publishConnected || !ingest.connected || !publish.connected {
		return time.Time{}, false
	}
	if ingest.generation != ingestGeneration || publish.generation != publishGeneration {
		return time.Time{}, false
	}
	if ingestReconnects != ingestGeneration.Reconnects ||
		publishReconnects != publishGeneration.Reconnects {
		return time.Time{}, false
	}
	return combinedLiveEpoch(ingest, publish)
}

// trackConnection records one connection's initial generation and keeps the
// fence current from its lifecycle callbacks, preserving any handler the
// connection already had.
func (epochs *DeviceFactEpochs) trackConnection(
	connection *natsgo.Conn,
	connected func(NATSConnectionGeneration, time.Time),
	disconnected func(),
) {
	previousDisconnect := connection.DisconnectErrHandler()
	previousReconnect := connection.ReconnectHandler()
	connection.SetDisconnectErrHandler(func(conn *natsgo.Conn, err error) {
		disconnected()
		if previousDisconnect != nil {
			previousDisconnect(conn, err)
		}
	})
	connection.SetReconnectHandler(func(conn *natsgo.Conn) {
		connected(connectionGeneration(conn), epochs.now().UTC())
		if previousReconnect != nil {
			previousReconnect(conn)
		}
	})
	connected(connectionGeneration(connection), epochs.now().UTC())
}

// combinedLiveEpoch returns the later established epoch when both connections
// are marked connected with nonzero epochs.
func combinedLiveEpoch(ingest connectionEpoch, publish connectionEpoch) (time.Time, bool) {
	if !ingest.connected || !publish.connected || ingest.epoch.IsZero() || publish.epoch.IsZero() {
		return time.Time{}, false
	}
	if publish.epoch.After(ingest.epoch) {
		return publish.epoch, true
	}
	return ingest.epoch, true
}

// connectionGeneration derives a connection's generation from its synchronous
// stats. The transport never waits for a callback to learn it.
func connectionGeneration(connection *natsgo.Conn) NATSConnectionGeneration {
	if connection == nil {
		return NATSConnectionGeneration{}
	}
	return NATSConnectionGeneration{Reconnects: connection.Stats().Reconnects}
}

// connectionLiveness reports whether one connection is connected right now and
// its cumulative reconnect count.
func connectionLiveness(connection *natsgo.Conn) (bool, uint64) {
	if connection == nil {
		return false, 0
	}
	return connection.IsConnected(), connection.Stats().Reconnects
}
