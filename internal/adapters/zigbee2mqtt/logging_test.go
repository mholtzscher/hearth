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

// captureHandler is a minimal slog handler for asserting structured records.
type captureStore struct {
	mutex   sync.Mutex
	records []capturedRecord
}

type capturedRecord struct {
	level   slog.Level
	message string
	attrs   map[string]any
}

type captureHandler struct {
	store  *captureStore
	prefix []slog.Attr
	level  slog.Level
}

func newCaptureHandler() *captureHandler {
	return &captureHandler{store: &captureStore{}, level: slog.LevelDebug}
}

func (handler *captureHandler) Enabled(_ context.Context, level slog.Level) bool {
	return level >= handler.level
}

func (handler *captureHandler) Handle(_ context.Context, record slog.Record) error {
	attrs := make(map[string]any, len(handler.prefix)+record.NumAttrs())
	for _, attr := range handler.prefix {
		attrs[attr.Key] = attr.Value.Any()
	}
	record.Attrs(func(attr slog.Attr) bool {
		attrs[attr.Key] = attr.Value.Any()
		return true
	})
	handler.store.mutex.Lock()
	handler.store.records = append(handler.store.records, capturedRecord{
		level: record.Level, message: record.Message, attrs: attrs,
	})
	handler.store.mutex.Unlock()
	return nil
}

func (handler *captureHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &captureHandler{store: handler.store, prefix: append(handler.prefix, attrs...), level: handler.level}
}

func (handler *captureHandler) WithGroup(string) slog.Handler { return handler }

func (handler *captureHandler) snapshot() []capturedRecord {
	handler.store.mutex.Lock()
	defer handler.store.mutex.Unlock()
	return append([]capturedRecord(nil), handler.store.records...)
}

func (handler *captureHandler) count(level slog.Level, event string) int {
	total := 0
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			total++
		}
	}
	return total
}

func (handler *captureHandler) first(level slog.Level, event string) (capturedRecord, bool) {
	for _, record := range handler.snapshot() {
		if record.level == level && record.attrs["event"] == event {
			return record, true
		}
	}
	return capturedRecord{}, false
}

func (handler *captureHandler) containsText(text string) bool {
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

func newCaptureAdapter(
	t *testing.T,
	session *fakeSession,
	handler *captureHandler,
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

func waitForCapture(t *testing.T, handler *captureHandler, condition func() bool) {
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
	handler := newCaptureHandler()
	session := newFakeSession(&runtimeRecorder{})
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{})

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

func TestConnectionRetryIsDebugWithFixedCode(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
	session := newFakeSession(&runtimeRecorder{})
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
		return handler.count(slog.LevelDebug, "dependency.retrying") >= 1
	})
	cancel()
	if err := <-done; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}

	if got := handler.count(slog.LevelWarn, "dependency.retrying"); got != 0 {
		t.Fatalf("warn retry records = %d, want 0 (retries stay at Debug)", got)
	}
	for _, record := range handler.snapshot() {
		if record.attrs["event"] != "dependency.retrying" {
			continue
		}
		if _, exists := record.attrs["error_code"]; !exists {
			t.Fatalf("retry record misses error_code: %#v", record.attrs)
		}
		if _, exists := record.attrs["error"]; exists {
			t.Fatalf("retry record carries arbitrary error text: %#v", record.attrs)
		}
	}
	if handler.containsText("no fake MQTT connection") {
		t.Fatal("logs leak raw connection error text")
	}
	if handler.containsText("tcp://127.0.0.1:1883") {
		t.Fatal("logs leak configured MQTT URL")
	}
}

func TestReconcileCompletedReportsActivatedCounts(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
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
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1
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
}

func TestReconcileCompletedReportsZeroSupportedDevices(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection.onSubscribe = func(connection *fakeConnection) {
		emitBridgeReady(t, connection, now, []byte(`[]`))
	}
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
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
}

func TestReconcileCompletedCountsSensorOnlyEntities(t *testing.T) {
	t.Parallel()
	handler := newCaptureHandler()
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
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1
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
	handler := newCaptureHandler()
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
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
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
	handler := newCaptureHandler()
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
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{connections: []*fakeConnection{connection}})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitForCapture(t, handler, func() bool {
		return handler.count(slog.LevelInfo, "adapter.reconcile_completed") == 1
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
	waitForCapture(t, handler, func() bool {
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

func TestCommandRefreshFailureOmitsErrorText(t *testing.T) {
	t.Parallel()
	const refreshSentinel = "sentinel-refresh-8b4e"
	handler := newCaptureHandler()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newCaptureAdapter(t, session, handler, &fakeDialer{})
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
	waitForCapture(t, handler, func() bool {
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
