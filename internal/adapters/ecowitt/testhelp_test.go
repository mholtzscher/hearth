package ecowitt //nolint:testpackage // Runtime tests use the package-private MQTT seam and coordinator.

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// The sanitized fixture PASSKEY is the only credential any checked-in fixture
// contains. It is not a real secret.
const (
	sanitizedPasskeyHex = "0123456789abcdef0123456789abcdef"
	fixtureTopic        = "ecowitt/943cc64457a7"
)

// fixtureReceivedAt is the local receipt time used with the sanitized fixture,
// whose dateutc is 2026-09-12 14:30:00 UTC. It is a fixed test fixture value
// rather than mutable package state.
//
//nolint:gochecknoglobals // Fixed fixture receipt time shared by every test.
var fixtureReceivedAt = time.Date(2026, time.September, 12, 14, 30, 4, 0, time.UTC)

// fixtureUploadInterval matches the example operator configuration.
const fixtureUploadInterval = 16 * time.Second

// sanitizedPasskey decodes the sanitized fixture PASSKEY.
func sanitizedPasskey(t *testing.T) [16]byte {
	t.Helper()
	decoded, err := hex.DecodeString(sanitizedPasskeyHex)
	if err != nil {
		t.Fatalf("decode sanitized PASSKEY: %v", err)
	}
	var passkey [16]byte
	copy(passkey[:], decoded)
	return passkey
}

// loadFixture reads one checked-in Ecowitt payload.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	return fixtureBytes(t, name)
}

// fixtureBytes reads one checked-in Ecowitt payload for any test or fuzz
// target.
func fixtureBytes(tb testing.TB, name string) []byte {
	tb.Helper()
	payload, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		tb.Fatalf("read fixture %s: %v", name, err)
	}
	return bytes.TrimSpace(payload)
}

// mustDecodePasskey decodes the sanitized fixture PASSKEY for any test or fuzz
// target.
func mustDecodePasskey(tb testing.TB) [16]byte {
	tb.Helper()
	decoded, err := hex.DecodeString(sanitizedPasskeyHex)
	if err != nil {
		tb.Fatalf("decode sanitized PASSKEY: %v", err)
	}
	var passkey [16]byte
	copy(passkey[:], decoded)
	return passkey
}

// callLog records an ordered trace of observable effects across the fake
// Session and fake transport, so a test can prove cross-call ordering.
type callLog struct {
	mutex   sync.Mutex
	entries []string
}

// append records one trace entry.
func (log *callLog) append(entry string) {
	if log == nil {
		return
	}
	log.mutex.Lock()
	defer log.mutex.Unlock()
	log.entries = append(log.entries, entry)
}

// snapshot returns every recorded entry in order.
func (log *callLog) snapshot() []string {
	if log == nil {
		return nil
	}
	log.mutex.Lock()
	defer log.mutex.Unlock()
	return append([]string(nil), log.entries...)
}

// testConfig is the Adapter configuration every test shares.
func testConfig(t *testing.T) Config {
	t.Helper()
	return Config{
		MQTTURL:          "tcp://127.0.0.1:1883",
		MQTTTopic:        fixtureTopic,
		MQTTClientID:     "hearth-eco-0123456789ab",
		GatewayName:      "Weather Station Gateway",
		OutdoorArrayName: "Outdoor Weather Array",
		ExpectedPasskey:  sanitizedPasskey(t),
		UploadInterval:   fixtureUploadInterval,
	}
}

// fakeClock is the deterministic time seam for runtime tests.
type fakeClock struct {
	mutex  sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

// newFakeClock starts a fake clock at one instant.
func newFakeClock(now time.Time) *fakeClock { return &fakeClock{now: now} }

// Now implements clock.
func (clock *fakeClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	return clock.now
}

// AfterFunc implements clock.
func (clock *fakeClock) AfterFunc(after time.Duration, callback func()) timer {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	scheduled := &fakeTimer{clock: clock, at: clock.now.Add(after), callback: callback}
	clock.timers = append(clock.timers, scheduled)
	return scheduled
}

// Advance moves the clock forward, firing every due timer in deadline order.
func (clock *fakeClock) Advance(after time.Duration) {
	target := clock.Now().Add(after)
	for {
		scheduled := clock.dueTimer(target)
		if scheduled == nil {
			clock.mutex.Lock()
			clock.now = target
			clock.mutex.Unlock()
			return
		}
		clock.mutex.Lock()
		clock.now = scheduled.at
		clock.mutex.Unlock()
		scheduled.callback()
	}
}

// dueTimer claims the earliest unclaimed timer at or before one instant.
func (clock *fakeClock) dueTimer(target time.Time) *fakeTimer {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	var due []*fakeTimer
	for _, scheduled := range clock.timers {
		if scheduled.claimed || scheduled.stopped || scheduled.at.After(target) {
			continue
		}
		due = append(due, scheduled)
	}
	if len(due) == 0 {
		return nil
	}
	sort.SliceStable(due, func(left, right int) bool {
		first, second := due[left], due[right]
		return first.at.Before(second.at)
	})
	due[0].claimed = true
	return due[0]
}

// fakeTimer is one schedulable deadline.
type fakeTimer struct {
	clock    *fakeClock
	at       time.Time
	callback func()
	claimed  bool
	stopped  bool
}

// Stop implements timer.
func (scheduled *fakeTimer) Stop() {
	scheduled.clock.mutex.Lock()
	defer scheduled.clock.mutex.Unlock()
	scheduled.stopped = true
}

// inlineEffects runs effects synchronously so effect completion is
// deterministic in coordinator tests.
type inlineEffects struct{}

// Go runs one effect inline.
func (inlineEffects) Go(effect func()) { effect() }

// Wait joins no goroutines.
func (inlineEffects) Wait() {}

// recordingSession is a fake Hearth Adapter SDK Session.
type recordingSession struct {
	mutex sync.Mutex

	registrations       []adapter.Registration
	bindings            []adapter.Binding
	healthReports       []adapter.HealthReport
	availabilityReports [][]adapter.EntityAvailabilityReport
	observations        []adapter.Observation
	observationIDs      []adapter.ObservationID

	registerErr     error
	healthErr       error
	availabilityErr error
	publishErr      error

	publishGate    chan struct{}
	publishStarted chan struct{}
	publishOnce    sync.Once

	// healthGate blocks the healthy SetHealth call, and healthStarted reports
	// the first attempt, so a test can prove health submissions are serialized
	// rather than concurrent. healthMaxInFlight records the highest number of
	// SetHealth calls running at once.
	healthGate        chan struct{}
	healthStarted     chan struct{}
	healthOnce        sync.Once
	healthInFlight    atomic.Int32
	healthMaxInFlight atomic.Int32
	trace             *callLog
}

// observedPublish reports the first attempt to publish an Observation, so a
// test can prove the coordinator kept processing while one was pending.
func (session *recordingSession) observedPublish() <-chan struct{} {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return session.publishStarted
}

// record appends one entry to the shared trace.
func (session *recordingSession) record(entry string) { session.trace.append(entry) }

// derivedBinding builds deterministic canonical Entity IDs from a registration
// so two independent runs of the same configuration produce identical IDs.
func derivedBinding(registration adapter.Registration) adapter.Binding {
	entities := make([]adapter.EntityBinding, 0, len(registration.Entities))
	for _, descriptor := range registration.Entities {
		entities = append(entities, adapter.EntityBinding{
			Key:      descriptor.Key,
			EntityID: "ent-" + registration.BindingKey + "-" + descriptor.Key,
			Enabled:  true,
		})
	}
	return adapter.Binding{
		BindingKey: registration.BindingKey,
		DeviceID:   "dev-" + registration.BindingKey,
		Entities:   entities,
	}
}

// Register implements Session.
func (session *recordingSession) Register(
	_ context.Context,
	registration adapter.Registration,
) (adapter.Binding, error) {
	session.record("register:" + registration.BindingKey)
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.registrations = append(session.registrations, registration)
	if session.registerErr != nil {
		return adapter.Binding{}, session.registerErr
	}
	if len(session.bindings) > 0 {
		index := len(session.registrations) - 1
		if index < len(session.bindings) {
			return session.bindings[index], nil
		}
	}
	return derivedBinding(registration), nil
}

// SetHealth implements Session. It tracks how many calls run at once so a test
// can prove the coordinator serializes health submissions instead of issuing
// healthy and unhealthy calls concurrently.
func (session *recordingSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	current := session.healthInFlight.Add(1)
	defer session.healthInFlight.Add(-1)
	for {
		maximum := session.healthMaxInFlight.Load()
		if current <= maximum || session.healthMaxInFlight.CompareAndSwap(maximum, current) {
			break
		}
	}
	if report.Status == adapter.HealthHealthy && session.healthStarted != nil {
		session.healthOnce.Do(func() { close(session.healthStarted) })
		if session.healthGate != nil {
			<-session.healthGate
		}
	}
	session.record("health:" + string(report.Status) + ":" + report.ReasonCode)
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.healthErr != nil {
		return session.healthErr
	}
	session.healthReports = append(session.healthReports, report)
	return nil
}

// ReportEntityAvailability implements Session.
func (session *recordingSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.record(fmt.Sprintf("availability:%d:%s", len(reports), availabilityStatusOf(reports)))
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.availabilityErr != nil {
		return session.availabilityErr
	}
	cloned := append([]adapter.EntityAvailabilityReport(nil), reports...)
	session.availabilityReports = append(session.availabilityReports, cloned)
	return nil
}

// PublishObservation implements Session.
func (session *recordingSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	if session.publishStarted != nil {
		session.publishOnce.Do(func() { close(session.publishStarted) })
	}
	if session.publishGate != nil {
		<-session.publishGate
	}
	session.record("observation:" + observation.EntityID)
	session.mutex.Lock()
	defer session.mutex.Unlock()
	if session.publishErr != nil {
		return "", session.publishErr
	}
	identifier := adapter.ObservationID("obs-" + observation.EntityID)
	session.observations = append(session.observations, observation)
	session.observationIDs = append(session.observationIDs, identifier)
	return identifier, nil
}

// registrationsSnapshot returns every recorded registration in order.
func (session *recordingSession) registrationsSnapshot() []adapter.Registration {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.Registration(nil), session.registrations...)
}

// health returns the recorded health transitions.
func (session *recordingSession) health() []adapter.HealthReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.HealthReport(nil), session.healthReports...)
}

// availability flattens every recorded availability report in call order.
func (session *recordingSession) availability() []adapter.EntityAvailabilityReport {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	flattened := make([]adapter.EntityAvailabilityReport, 0)
	for _, batch := range session.availabilityReports {
		flattened = append(flattened, batch...)
	}
	return flattened
}

// published returns every published Observation in publication order.
func (session *recordingSession) published() []adapter.Observation {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	return append([]adapter.Observation(nil), session.observations...)
}

// fakeConnection is one fake MQTT connection fronted by the production callback
// relay, exactly as the Paho transport is: Dial wraps the delivery callback,
// enqueue is nonblocking and bounded, and Close seals the relay and waits for
// every admitted delivery to drain. Tests that need a delivery to outlive its
// connection can therefore reproduce a real Paho loss faithfully.
type fakeConnection struct {
	mutex        sync.Mutex
	topic        string
	qos          byte
	subscribeErr error
	lost         chan error
	closed       bool
	relay        *mqttRelay
}

// Subscribe implements mqttConnection.
func (connection *fakeConnection) Subscribe(_ context.Context, topic string, qos byte) error {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	connection.topic = topic
	connection.qos = qos
	return connection.subscribeErr
}

// Lost implements mqttConnection.
func (connection *fakeConnection) Lost() <-chan error { return connection.lost }

// Close implements mqttConnection. It mirrors the production connection:
// sealing stops new admissions and the wait joins the drain, so every admitted
// delivery reaches the delivery callback before Close returns.
func (connection *fakeConnection) Close() {
	connection.mutex.Lock()
	connection.closed = true
	relay := connection.relay
	connection.mutex.Unlock()
	if relay != nil {
		relay.seal()
		relay.wait()
	}
}

// relaySnapshot returns the connection's current relay.
func (connection *fakeConnection) relaySnapshot() *mqttRelay {
	connection.mutex.Lock()
	defer connection.mutex.Unlock()
	return connection.relay
}

// fakeDialer captures the delivery callback so tests can push broker
// deliveries exactly as the Paho relay would.
type fakeDialer struct {
	trace      *callLog
	mutex      sync.Mutex
	configs    []mqttConfig
	connection *fakeConnection
	dialErr    error
}

// newFakeDialer builds a fake transport with an unconnected generation.
func newFakeDialer() *fakeDialer {
	return &fakeDialer{connection: &fakeConnection{lost: make(chan error, 1)}}
}

// Dial implements mqttDialer. It wraps the delivery callback in a real relay
// and watches that relay's overflow, so the fake transport holds the same
// bounded nonblocking admission and drain contract as the production Paho
// transport.
func (dialer *fakeDialer) Dial(
	ctx context.Context,
	config mqttConfig,
	deliver func(mqttMessage),
) (mqttConnection, error) {
	dialer.trace.append("dial:" + config.ClientID)
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	dialer.configs = append(dialer.configs, config)
	if dialer.dialErr != nil {
		return nil, dialer.dialErr
	}
	relay := newMQTTRelay(deliver)
	go relay.watchOverflow(ctx, dialer.connection.lost)
	dialer.connection.mutex.Lock()
	dialer.connection.relay = relay
	dialer.connection.mutex.Unlock()
	return dialer.connection, nil
}

// push delivers one broker message through the connection's relay, exactly as
// one Paho callback would. A push after the relay sealed is ignored.
func (dialer *fakeDialer) push(message mqttMessage) {
	if relay := dialer.connection.relaySnapshot(); relay != nil {
		relay.enqueue(message)
	}
}

// configsSeen returns the dial configuration of every attempt.
func (dialer *fakeDialer) configsSeen() []mqttConfig {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	return append([]mqttConfig(nil), dialer.configs...)
}

// runtimeHarness drives the serial coordinator deterministically.
type runtimeHarness struct {
	clock       *fakeClock
	trace       *callLog
	session     *recordingSession
	dialer      *fakeDialer
	adapter     *Adapter
	coordinator *runtimeCoordinator
	terminal    error
}

// newRuntimeHarness builds an Adapter, its route snapshot, and a coordinator
// with a fake clock and inline effects.
func newRuntimeHarness(t *testing.T, logger *slog.Logger) *runtimeHarness {
	t.Helper()
	return newRuntimeHarnessWithEffects(t, logger, inlineEffects{})
}

// newRuntimeHarnessWithEffects builds a harness with one effect group.
func newRuntimeHarnessWithEffects(
	t *testing.T,
	logger *slog.Logger,
	effects effectGroup,
) *runtimeHarness {
	t.Helper()
	clock := newFakeClock(fixtureReceivedAt)
	trace := &callLog{}
	session := &recordingSession{trace: trace, publishStarted: make(chan struct{})}
	dialer := newFakeDialer()
	dialer.trace = trace
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	ecowitt, err := newAdapter(session, testConfig(t), logger, dialer)
	if err != nil {
		t.Fatalf("newAdapter: %v", err)
	}
	ecowitt.clock = clock
	ctx, cancel := context.WithCancel(t.Context())
	routes, err := ecowitt.registerDevices(ctx)
	if err != nil {
		t.Fatalf("registerDevices: %v", err)
	}
	coordinator := newRuntimeCoordinator(ctx, cancel, ecowitt, routes, clock, effects)
	t.Cleanup(cancel)
	return &runtimeHarness{
		clock:       clock,
		trace:       trace,
		session:     session,
		dialer:      dialer,
		adapter:     ecowitt,
		coordinator: coordinator,
	}
}

// pump drains and applies every queued coordinator event until it quiesces.
func (harness *runtimeHarness) pump(t *testing.T) {
	t.Helper()
	for range 10000 {
		select {
		case event := <-harness.coordinator.events:
			if err := harness.coordinator.handle(event); err != nil {
				harness.terminal = err
				return
			}
		default:
			return
		}
	}
	t.Fatal("runtime coordinator did not quiesce")
}

// submit enqueues one event as the connection supervisor would.
func (harness *runtimeHarness) submit(t *testing.T, event runtimeEvent) {
	t.Helper()
	if err := harness.coordinator.submit(harness.coordinator.ctx, event); err != nil {
		t.Fatalf("submit event: %v", err)
	}
	harness.pump(t)
}

// establish starts the first MQTT generation up to its SUBACK.
func (harness *runtimeHarness) establish(t *testing.T) {
	t.Helper()
	result := make(chan error, 1)
	harness.submit(t, generationStarting{generation: 1, result: result})
	select {
	case err := <-result:
		if err != nil {
			t.Fatalf("generation starting: %v", err)
		}
	default:
		t.Fatal("generation starting was not acknowledged")
	}
	harness.submit(t, generationEstablished{
		generation:   1,
		disconnect:   func(error) {},
		subscribedAt: harness.clock.Now(),
	})
}

// send enqueues one event without draining the queue, so a test can run the
// coordinator loop concurrently.
func (harness *runtimeHarness) send(t *testing.T, event runtimeEvent) {
	t.Helper()
	if err := harness.coordinator.submit(harness.coordinator.ctx, event); err != nil {
		t.Fatalf("send event: %v", err)
	}
}

// deliver pushes one broker message into the coordinator.
func (harness *runtimeHarness) deliver(t *testing.T, topic string, payload []byte, retained bool) {
	t.Helper()
	harness.deliverAt(t, harness.coordinator.generation, topic, payload, retained, harness.clock.Now())
}

// deliverAt pushes one broker message for one generation at one receipt time.
func (harness *runtimeHarness) deliverAt(
	t *testing.T,
	generation uint64,
	topic string,
	payload []byte,
	retained bool,
	receivedAt time.Time,
) {
	t.Helper()
	harness.submit(t, reportReceived{
		generation: generation,
		message: mqttMessage{
			Topic: topic, Payload: payload, Retained: retained, ReceivedAt: receivedAt,
		},
	})
}

// advance moves the fake clock and applies every resulting deadline event.
func (harness *runtimeHarness) advance(t *testing.T, after time.Duration) {
	t.Helper()
	harness.clock.Advance(after)
	harness.pump(t)
}

// sanitizedCaptureFixture is the checked-in sanitized real GW2000+WS90 report.
const sanitizedCaptureFixture = "gw2000-ws90-report.txt"

// fixtureMessage is the sanitized real fixture as one broker delivery.
func fixtureMessage(t *testing.T) mqttMessage {
	t.Helper()
	return mqttMessage{
		Topic:      fixtureTopic,
		Payload:    loadFixture(t, sanitizedCaptureFixture),
		ReceivedAt: fixtureReceivedAt,
	}
}

// availabilityStatusOf summarizes one availability batch for the trace.
func availabilityStatusOf(reports []adapter.EntityAvailabilityReport) string {
	if len(reports) == 0 {
		return "empty"
	}
	return string(reports[0].Status)
}

// publishedEntityIDs returns the canonical Entity IDs of published
// Observations in publication order.
func publishedEntityIDs(observations []adapter.Observation) []string {
	identifiers := make([]string, 0, len(observations))
	for _, observation := range observations {
		identifiers = append(identifiers, observation.EntityID)
	}
	return identifiers
}

// bufferHandler captures structured log records for diagnostics assertions.
type bufferHandler struct {
	mutex   sync.Mutex
	records []map[string]any
	buffer  bytes.Buffer
}

// Enabled implements [slog.Handler].
func (handler *bufferHandler) Enabled(context.Context, slog.Level) bool { return true }

// Handle implements [slog.Handler].
func (handler *bufferHandler) Handle(_ context.Context, record slog.Record) error {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	fields := map[string]any{"msg": record.Message, "level": record.Level.String()}
	record.Attrs(func(attribute slog.Attr) bool {
		fields[attribute.Key] = attribute.Value.Any()
		return true
	})
	handler.records = append(handler.records, fields)
	if err := json.NewEncoder(&handler.buffer).Encode(fields); err != nil {
		return err
	}
	return nil
}

// WithAttrs implements [slog.Handler].
func (handler *bufferHandler) WithAttrs(_ []slog.Attr) slog.Handler {
	return handler
}

// WithGroup implements [slog.Handler].
func (handler *bufferHandler) WithGroup(string) slog.Handler { return handler }

// output returns every captured record as text.
func (handler *bufferHandler) output() string {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	return handler.buffer.String()
}
