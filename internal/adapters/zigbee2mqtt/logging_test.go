package zigbee2mqtt //nolint:testpackage // Tests assert package-private logging behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// logRecordSnapshot is one captured slog record with its resolved attributes.
type logRecordSnapshot struct {
	level   slog.Level
	message string
	attrs   map[string]any
}

// recordingStore holds captured records behind a mutex so connection and
// runtime goroutines can log while the test polls without data races.
type recordingStore struct {
	mutex   sync.Mutex
	records []logRecordSnapshot
}

type recordingHandler struct {
	level  slog.Level
	prefix []slog.Attr
	store  *recordingStore
}

func newRecordingHandler() *recordingHandler {
	return &recordingHandler{level: slog.LevelDebug, store: &recordingStore{}}
}

func (handler *recordingHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level
}

func (handler *recordingHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(handler.prefix)+record.NumAttrs())
	for _, attr := range handler.prefix {
		attrs[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	handler.store.mutex.Lock()
	handler.store.records = append(handler.store.records, logRecordSnapshot{
		level: record.Level, message: record.Message, attrs: attrs,
	})
	handler.store.mutex.Unlock()
	return nil
}

func (handler *recordingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &recordingHandler{
		level: handler.level, prefix: append(handler.prefix, attrs...), store: handler.store,
	}
}

func (handler *recordingHandler) WithGroup(string) slog.Handler { return handler }

func (handler *recordingHandler) snapshot() []logRecordSnapshot {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]logRecordSnapshot(nil), handler.store.records...)
}

func (handler *recordingHandler) count(level slog.Level, event string) int {
	total := 0
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			total++
		}
	}
	return total
}

func (handler *recordingHandler) first(level slog.Level, event string) (logRecordSnapshot, bool) {
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			return record, true
		}
	}
	return logRecordSnapshot{}, false
}

func (handler *recordingHandler) containsText(text string) bool {
	for _, record := range handler.snapshot() {
		if strings.Contains(record.message, text) {
			return true
		}
		for _, value := range record.attrs {
			if strings.Contains(fmt.Sprintf("%v", value), text) {
				return true
			}
		}
	}
	return false
}

func newRecordingAdapter(
	t *testing.T,
	session *fakeSession,
	handler *recordingHandler,
	dialer mqttDialer,
) *Adapter {
	t.Helper()
	z2m, err := newAdapter(
		session,
		Config{MQTTURL: "tcp://127.0.0.1:1883", BaseTopic: "zigbee2mqtt", ClientID: "test-client"},
		slog.New(handler),
		dialer,
	)
	if err != nil {
		t.Fatal(err)
	}
	z2m.retryDelay = func(time.Duration) time.Duration { return 0 }
	return z2m
}

func emitBridgeReady(
	t *testing.T,
	connection *fakeConnection,
	now time.Time,
	inventory []byte,
) {
	t.Helper()
	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
	connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
	connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
}

func waitForLogCondition(t *testing.T, handler *recordingHandler, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for log condition; records = %#v", handler.snapshot())
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func TestAdapterAttachesBoundedComponentOnce(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	session := newFakeSession(&runtimeRecorder{})
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{})

	z2m.logIsolatedDevice(context.Background(), rejectionDisabled)

	records := handler.snapshot()
	if len(records) != 1 {
		t.Fatalf("records = %d, want 1", len(records))
	}
	record := records[0]
	if record.attrs["component"] != adapterComponent {
		t.Fatalf("component = %v, want %q", record.attrs["component"], adapterComponent)
	}
	if record.attrs["event"] != "adapter.device_isolated" {
		t.Fatalf("event = %v", record.attrs["event"])
	}
	for _, key := range []string{"app", "pid", "ieee", "friendly_name", "model"} {
		if _, exists := record.attrs[key]; exists {
			t.Fatalf("record carries forbidden key %q: %#v", key, record.attrs)
		}
	}
}

func TestReconcileCompletedReportsActivatedCounts(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1 &&
			handler.count(slog.LevelInfo, "adapter.upstream_ready") == 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	record, found := handler.first(slog.LevelInfo, "adapter.reconcile_completed")
	if !found {
		t.Fatal("missing adapter.reconcile_completed record")
	}
	if record.attrs["device_count"] != int64(1) {
		t.Fatalf("device_count = %v (%T), want 1", record.attrs["device_count"], record.attrs["device_count"])
	}
	if entities, ok := record.attrs["entity_count"].(int64); !ok || entities < 1 {
		t.Fatalf("entity_count = %v, want at least 1", record.attrs["entity_count"])
	}
	if record.attrs["isolated_device_count"] != int64(0) {
		t.Fatalf("isolated_device_count = %v, want 0", record.attrs["isolated_device_count"])
	}
	if handler.count(slog.LevelInfo, "dependency.recovered") != 0 {
		t.Fatalf("first startup must not emit recovery: %#v", handler.snapshot())
	}
}

func TestReconcileCompletedReportsZeroSupportedDevices(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, []byte(`[]`))
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	record, found := handler.first(slog.LevelInfo, "adapter.reconcile_completed")
	if !found {
		t.Fatal("missing adapter.reconcile_completed record")
	}
	for _, key := range []string{"device_count", "entity_count", "isolated_device_count"} {
		value, exists := record.attrs[key]
		if !exists {
			t.Fatalf("summary misses %q: %#v", key, record.attrs)
		}
		if value != int64(0) {
			t.Fatalf("%s = %v, want explicit 0", key, value)
		}
	}
	if handler.count(slog.LevelInfo, "adapter.upstream_ready") != 1 {
		t.Fatalf("records = %#v", handler.snapshot())
	}
}

func TestReconcileCompletedCountsSensorOnlyEntities(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleSensorDevice("temperature", 1)})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1 &&
			handler.count(slog.LevelInfo, "adapter.upstream_ready") == 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	record, found := handler.first(slog.LevelInfo, "adapter.reconcile_completed")
	if !found {
		t.Fatal("missing adapter.reconcile_completed record")
	}
	if record.attrs["device_count"] != int64(1) {
		t.Fatalf("device_count = %v, want 1", record.attrs["device_count"])
	}
	if record.attrs["entity_count"] != int64(1) {
		t.Fatalf("entity_count = %v, want 1 sensor Entity", record.attrs["entity_count"])
	}
	if record.attrs["isolated_device_count"] != int64(0) {
		t.Fatalf("isolated_device_count = %v, want 0", record.attrs["isolated_device_count"])
	}
}

func TestIsolatedDeviceOmitsVendorIdentity(t *testing.T) {
	t.Parallel()
	const (
		ieeeSentinel     = "0x00124b00deadbeef"
		friendlySentinel = "sentinel-friendly-9z8q"
		modelSentinel    = "sentinel-model-7x6w"
	)
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	device := eligibleDevice()
	device.IEEEAddress, device.FriendlyName, device.Disabled = ieeeSentinel, friendlySentinel, true
	device.Definition.Model = modelSentinel
	inventory, err := json.Marshal([]upstreamDevice{device})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1 &&
			handler.count(slog.LevelWarn, "adapter.device_isolated") == 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	record, _ := handler.first(slog.LevelWarn, "adapter.device_isolated")
	if record.attrs["reason_code"] != rejectionDisabled {
		t.Fatalf("reason_code = %v, want %q", record.attrs["reason_code"], rejectionDisabled)
	}
	for _, sentinel := range []string{ieeeSentinel, friendlySentinel, modelSentinel} {
		if handler.containsText(sentinel) {
			t.Fatalf("logs leak vendor identity %q", sentinel)
		}
	}
	summary, _ := handler.first(slog.LevelInfo, "adapter.reconcile_completed")
	if summary.attrs["isolated_device_count"] != int64(1) {
		t.Fatalf("isolated_device_count = %v, want 1", summary.attrs["isolated_device_count"])
	}
}

func TestMalformedUpstreamPayloadsStayOutOfLogs(t *testing.T) {
	t.Parallel()
	const (
		stateSentinel        = "SENTINEL-BAD-STATE-1a2b"
		availabilitySentinel = "SENTINEL-BAD-AVAIL-3c4d"
		bridgeEventSentinel  = "SENTINEL-BRIDGE-EVT-5e6f"
	)
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.upstream_ready") == 1
	})
	connection.emit(
		"zigbee2mqtt/test-light",
		[]byte(`{"state":"`+stateSentinel+`"}`),
		false,
		now.Add(time.Second),
	)
	connection.emit(
		"zigbee2mqtt/test-light/availability",
		[]byte(`{"state":"`+availabilitySentinel+`"}`),
		false,
		now.Add(time.Second),
	)
	connection.emit("zigbee2mqtt/bridge/event", []byte(bridgeEventSentinel), false, now.Add(time.Second))
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelWarn, "adapter.device_state_ignored")+
			handler.count(slog.LevelWarn, "adapter.state_property_ignored") >= 1 &&
			handler.count(slog.LevelWarn, "adapter.availability_ignored") == 1 &&
			handler.count(slog.LevelWarn, "adapter.bridge_event_ignored") == 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	for _, sentinel := range []string{stateSentinel, availabilitySentinel, bridgeEventSentinel} {
		if handler.containsText(sentinel) {
			t.Fatalf("logs leak upstream payload %q", sentinel)
		}
	}
	for _, record := range handler.snapshot() {
		for _, key := range []string{"topic", "friendly_name", "payload", "properties", "error"} {
			if _, exists := record.attrs[key]; exists {
				t.Fatalf("record carries forbidden key %q: %#v", key, record.attrs)
			}
		}
	}
}

func TestSameConnectionBridgeFlapReemitsUpstreamReadyOnce(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1 &&
			handler.count(slog.LevelInfo, "adapter.upstream_ready") == 1
	})

	connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now.Add(time.Second))
	time.Sleep(200 * time.Millisecond)
	if got := handler.count(slog.LevelInfo, "adapter.upstream_ready"); got != 1 {
		cancel()
		<-done
		t.Fatalf("upstream_ready = %d after inventory refresh without unhealthy, want 1", got)
	}

	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"offline"}`), false, now.Add(2*time.Second))
	waitForLogCondition(t, handler, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		for _, report := range session.health {
			if report.Status == adapter.HealthUnhealthy && report.ReasonCode == bridgeOfflineReason {
				return true
			}
		}
		return false
	})
	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), false, now.Add(3*time.Second))
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.upstream_ready") == 2
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelInfo, "adapter.reconcile_completed"); got != 3 {
		t.Fatalf("reconcile_completed = %d, want 3 (initial plus inventory refresh plus same-connection recovery)", got)
	}
	if got := handler.count(slog.LevelInfo, "adapter.upstream_ready"); got != 2 {
		t.Fatalf("upstream_ready = %d, want exactly 2 (once per recovery)", got)
	}
	if got := handler.count(slog.LevelInfo, "dependency.recovered"); got != 0 {
		t.Fatalf("dependency.recovered = %d, want 0 without a dial retry episode", got)
	}
}

// flakyDialer fails a fixed number of dials before delegating, so retry and
// recovery evidence can be asserted without timing-sensitive fault injection.
type flakyDialer struct {
	mutex sync.Mutex
	fails int
	err   error
	next  mqttDialer
}

func (dialer *flakyDialer) Dial(
	ctx context.Context,
	config mqttConfig,
	receive func(mqttMessage),
) (mqttConnection, error) {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	if dialer.fails > 0 {
		dialer.fails--
		return nil, dialer.err
	}
	return dialer.next.Dial(ctx, config, receive)
}

func TestConnectionFailureWarnsOnceThenRecovers(t *testing.T) {
	t.Parallel()
	const dialSentinel = "sentinel-broker-2f9d.invalid"
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, inventory)
	}
	dialer := &flakyDialer{
		fails: 1,
		err:   errors.New("dial " + dialSentinel + ": connection refused"),
		next:  &fakeDialer{connections: []*fakeConnection{connection}},
	}
	z2m := newRecordingAdapter(t, session, handler, dialer)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.upstream_ready") == 1 &&
			handler.count(slog.LevelInfo, "dependency.recovered") == 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 1 {
		t.Fatalf("warn retry records = %d, want exactly 1", got)
	}
	if got := handler.count(slog.LevelDebug, "dependency.retrying"); got != 0 {
		t.Fatalf("debug retry records = %d, want 0 for a single failure", got)
	}
	recovered, found := handler.first(slog.LevelInfo, "dependency.recovered")
	if !found {
		t.Fatal("missing dependency.recovered record")
	}
	if recovered.attrs["attempts"] != int64(1) {
		t.Fatalf("attempts = %v, want 1", recovered.attrs["attempts"])
	}
	if _, exists := recovered.attrs["duration_ms"]; !exists {
		t.Fatalf("recovered record misses duration_ms: %#v", recovered.attrs)
	}
	if handler.containsText(dialSentinel) {
		t.Fatalf("logs leak dial failure text %q", dialSentinel)
	}
	if handler.containsText("tcp://127.0.0.1:1883") {
		t.Fatal("logs leak configured MQTT URL")
	}
}

func TestIdenticalConnectionFailuresStayQuietAfterFirstWarn(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelDebug, "dependency.retrying") >= 1
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 1 {
		t.Fatalf("warn retry records = %d, want exactly 1", got)
	}
	if handler.containsText("no fake MQTT connection") {
		t.Fatal("logs leak raw connection error text")
	}
}

func TestCancelledRetryBackoffEmitsNoAdditionalWarning(t *testing.T) {
	t.Parallel()
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{})
	z2m.retryDelay = func(time.Duration) time.Duration { return 50 * time.Millisecond }

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelWarn, "dependency.retrying") == 1
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 1 {
		t.Fatalf("warn retry records = %d, want exactly 1 with no cancellation warning", got)
	}
	for _, record := range handler.snapshot() {
		if record.level == slog.LevelError {
			t.Fatalf("cancellation produced an Error record: %#v", record)
		}
		if event, ok := record.attrs["event"].(string); ok {
			switch event {
			case "dependency.connected", "dependency.disconnected", "dependency.reconnected", "dependency.closed":
				t.Fatalf("unexpected SDK-owned event %q: %#v", event, record)
			}
		}
	}
}

func TestCommandRefreshFailureOmitsErrorText(t *testing.T) {
	t.Parallel()
	const refreshSentinel = "sentinel-refresh-8b4e"
	handler := newRecordingHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRecordingAdapter(t, session, handler, &fakeDialer{})
	startCoordinator(t, z2m)
	connection := newFakeConnection(recorder)
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	binding := adapter.Binding{BindingKey: device.Registration.BindingKey, DeviceID: "dev-test"}
	for _, entity := range device.Entities {
		binding.Entities = append(binding.Entities, adapter.EntityBinding{
			Key:      entity.Descriptor.Key,
			EntityID: "entity-" + entity.Descriptor.Key,
			Enabled:  true,
		})
	}
	runtime, err := runtimeDeviceFromBinding(device, binding)
	if err != nil {
		t.Fatal(err)
	}
	routes := make(map[string]commandRoute)
	for _, entity := range runtime.entities {
		routes[entity.entityID] = commandRoute{
			entityID: entity.entityID, ieeeAddress: runtime.ieeeAddress, friendlyName: runtime.friendly,
			entity: entity, connectionGeneration: 1,
		}
	}
	activation := make(chan routeActivationResult, 1)
	z2m.runtimeEvents <- routesActivated{
		generation: 1,
		connection: connection,
		disconnect: func(error) {},
		snapshot:   routeSnapshot{routes: routes, devices: map[string]runtimeDevice{runtime.friendly: runtime}},
		result:     activation,
	}
	if result := <-activation; result.err != nil {
		t.Fatal(result.err)
	}
	connection.onPublish = func(_ context.Context, _ *fakeConnection, topic string, _ []byte) error {
		if strings.HasSuffix(topic, "/get") {
			return errors.New("publish zigbee2mqtt/" + runtime.friendly + "/get: " + refreshSentinel)
		}
		return nil
	}
	if err = z2m.HandleCommand(
		context.Background(),
		testCommand(runtime.entities[0].entityID, `{"value":true}`),
		newFakeResponder(recorder, session),
	); err != nil {
		t.Fatal(err)
	}
	waitForLogCondition(t, handler, func() bool {
		return handler.count(slog.LevelWarn, "adapter.command_refresh_failed") == 1
	})

	if handler.containsText(refreshSentinel) {
		t.Fatalf("logs leak refresh error text %q", refreshSentinel)
	}
	record, _ := handler.first(slog.LevelWarn, "adapter.command_refresh_failed")
	if record.attrs["entity_id"] != runtime.entities[0].entityID {
		t.Fatalf("entity_id = %v, want canonical command Entity", record.attrs["entity_id"])
	}
}
