package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

type runtimeRecorder struct {
	mutex  sync.Mutex
	events []string
}

func (recorder *runtimeRecorder) add(event string) {
	recorder.mutex.Lock()
	recorder.events = append(recorder.events, event)
	recorder.mutex.Unlock()
}

func (recorder *runtimeRecorder) snapshot() []string {
	recorder.mutex.Lock()
	defer recorder.mutex.Unlock()
	return append([]string(nil), recorder.events...)
}

type fakeSession struct {
	mutex        sync.Mutex
	recorder     *runtimeRecorder
	pages        map[string]adapter.OwnedMappingPage
	pageRequests []adapter.OwnedMappingPageRequest
	bindings     map[string]adapter.Binding
	registerHook func(context.Context, adapter.Registration)
	health       []adapter.HealthReport
	availability []adapter.EntityAvailabilityReport
	observations []adapter.Observation
}

func newFakeSession(recorder *runtimeRecorder) *fakeSession {
	return &fakeSession{
		recorder: recorder,
		pages:    map[string]adapter.OwnedMappingPage{"": {}},
		bindings: make(map[string]adapter.Binding),
	}
}

func (session *fakeSession) ListOwnedMappings(
	_ context.Context,
	request adapter.OwnedMappingPageRequest,
) (adapter.OwnedMappingPage, error) {
	session.mutex.Lock()
	defer session.mutex.Unlock()
	session.pageRequests = append(session.pageRequests, request)
	session.recorder.add("list:" + request.Cursor)
	return session.pages[request.Cursor], nil
}

func (session *fakeSession) Register(ctx context.Context, registration adapter.Registration) (adapter.Binding, error) {
	session.recorder.add("register:" + registration.BindingKey)
	if session.registerHook != nil {
		session.registerHook(ctx, registration)
	}
	if err := ctx.Err(); err != nil {
		return adapter.Binding{}, err
	}
	if binding, exists := session.bindings[registration.BindingKey]; exists {
		return binding, nil
	}
	binding := adapter.Binding{BindingKey: registration.BindingKey, DeviceID: "dev-" + registration.BindingKey}
	for _, descriptor := range registration.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{
				Key:      descriptor.Key,
				EntityID: registration.BindingKey + "-" + descriptor.Key,
				Enabled:  true,
			},
		)
	}
	return binding, nil
}

func (session *fakeSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	session.mutex.Lock()
	session.health = append(session.health, report)
	session.mutex.Unlock()
	session.recorder.add("health:" + string(report.Status) + ":" + report.ReasonCode)
	return nil
}

func (session *fakeSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.mutex.Lock()
	session.availability = append(session.availability, reports...)
	session.mutex.Unlock()
	session.recorder.add("availability")
	return nil
}

func (session *fakeSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.mutex.Lock()
	session.observations = append(session.observations, observation)
	session.mutex.Unlock()
	if observation.RefreshForCommand == nil {
		session.recorder.add("observation")
	} else {
		session.recorder.add("linked-observation")
	}
	return "obs-test", nil
}

type fakeDialer struct {
	mutex       sync.Mutex
	connections []*fakeConnection
	dials       int
}

func (dialer *fakeDialer) Dial(_ context.Context, _ mqttConfig, receive func(mqttMessage)) (mqttConnection, error) {
	dialer.mutex.Lock()
	defer dialer.mutex.Unlock()
	if dialer.dials >= len(dialer.connections) {
		return nil, errors.New("no fake MQTT connection")
	}
	connection := dialer.connections[dialer.dials]
	dialer.dials++
	connection.receive = receive
	return connection, nil
}

type fakeConnection struct {
	mutex         sync.Mutex
	recorder      *runtimeRecorder
	receive       func(mqttMessage)
	onSubscribe   func(*fakeConnection)
	onPublish     func(context.Context, *fakeConnection, string, []byte) error
	published     []mqttPublication
	lost          chan error
	closed        bool
	subscriptions []string
}

type mqttPublication struct {
	topic    string
	payload  string
	qos      byte
	retained bool
}

func newFakeConnection(recorder *runtimeRecorder) *fakeConnection {
	return &fakeConnection{recorder: recorder, lost: make(chan error, 1)}
}

func (connection *fakeConnection) Subscribe(_ context.Context, topic string, _ byte) error {
	connection.mutex.Lock()
	connection.subscriptions = append(connection.subscriptions, topic)
	connection.mutex.Unlock()
	connection.recorder.add("subscribe:" + topic)
	if connection.onSubscribe != nil {
		connection.onSubscribe(connection)
	}
	return nil
}

func (connection *fakeConnection) Publish(
	ctx context.Context,
	topic string,
	qos byte,
	retained bool,
	payload []byte,
) error {
	connection.mutex.Lock()
	connection.published = append(
		connection.published,
		mqttPublication{topic: topic, payload: string(payload), qos: qos, retained: retained},
	)
	connection.mutex.Unlock()
	connection.recorder.add("mqtt:" + topic)
	if connection.onPublish != nil {
		return connection.onPublish(ctx, connection, topic, payload)
	}
	return nil
}

func (connection *fakeConnection) Lost() <-chan error { return connection.lost }
func (connection *fakeConnection) Close() {
	connection.mutex.Lock()
	connection.closed = true
	connection.mutex.Unlock()
}

func (connection *fakeConnection) emit(topic string, payload []byte, retained bool, receivedAt time.Time) {
	connection.receive(mqttMessage{Topic: topic, Payload: payload, Retained: retained, ReceivedAt: receivedAt})
}

// This test protects paginated ownership reconciliation and the fixed healthy -> availability -> State -> /get startup order.
// It fails if missing, disabled, and removed capabilities collapse to one reason or retained evidence is emitted too early.
func TestRunReconcilesOwnedMappingsAndOrdersStartupEvidence(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	currentBinding := "z2m-00124b0024abcdef"
	session.pages = map[string]adapter.OwnedMappingPage{
		"": {
			Items: []adapter.OwnedMapping{
				{BindingKey: currentBinding, EntityKey: "power", EntityID: currentBinding + "-power"},
				{BindingKey: "z2m-0000000000000003", EntityKey: "power", EntityID: "missing-power"},
			},
			NextCursor: "page-2",
		},
		"page-2": {Items: []adapter.OwnedMapping{
			{BindingKey: "z2m-0000000000000004", EntityKey: "power", EntityID: "disabled-power"},
			{BindingKey: "z2m-0000000000000005", EntityKey: "power", EntityID: "removed-power"},
		}},
	}
	current := eligibleDevice()
	session.bindings[currentBinding] = adapter.Binding{
		BindingKey: currentBinding,
		DeviceID:   "dev-current",
		Entities: []adapter.EntityBinding{
			{Key: "power", EntityID: currentBinding + "-power", Enabled: true},
			{Key: "brightness", EntityID: currentBinding + "-brightness", Enabled: true},
			{Key: "removed-diagnostic", EntityID: "returned-removed", Enabled: true},
		},
	}
	disabled := eligibleDevice()
	disabled.IEEEAddress, disabled.FriendlyName, disabled.Disabled = "0x0000000000000004", "disabled-light", true
	removed := eligibleDevice()
	removed.IEEEAddress, removed.FriendlyName = "0x0000000000000005", "removed-light"
	removed.Definition.Exposes = []upstreamExpose{{Type: "switch"}}
	inventory, err := json.Marshal([]upstreamDevice{current, disabled, removed})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connection := newFakeConnection(recorder)
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
		connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON","brightness":63.75}`), true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		observations := len(session.observations)
		session.mutex.Unlock()
		connection.mutex.Lock()
		publications := len(connection.published)
		connection.mutex.Unlock()
		return observations == 2 && publications == 2
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}

	if got := session.pageRequests; !reflect.DeepEqual(
		got,
		[]adapter.OwnedMappingPageRequest{{Limit: 200}, {Limit: 200, Cursor: "page-2"}},
	) {
		t.Fatalf("mapping page requests = %#v", got)
	}
	reasons := make(map[string]string)
	for _, report := range session.availability {
		reasons[report.EntityID] = report.ReasonCode
	}
	if reasons["missing-power"] != deviceMissingReason || reasons["disabled-power"] != deviceDisabledReason ||
		reasons["removed-power"] != capabilityMissingReason || reasons["returned-removed"] != capabilityMissingReason ||
		reasons[currentBinding+"-power"] != "" {
		t.Fatalf("availability reasons = %#v", reasons)
	}
	events := recorder.snapshot()
	assertOrdered(
		t,
		events,
		"list:",
		"list:page-2",
		"subscribe:zigbee2mqtt/#",
		"health:healthy:",
		"availability",
		"observation",
		"mqtt:zigbee2mqtt/test-light/get",
	)
	for _, publication := range connection.published {
		if publication.qos != 1 || publication.retained || publication.topic != "zigbee2mqtt/test-light/get" {
			t.Fatalf("startup publication = %#v", publication)
		}
	}
}

// This test protects recoverable exact health reasons and latest-per-topic pending State. It fails if malformed or
// incompatible info permits registration, stale State history floods Core, or availability precedes healthy acknowledgement.
func TestRunRecoversFromIncompatibleBridgeConfiguration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	inventory, _ := json.Marshal([]upstreamDevice{eligibleDevice()})
	badInfo := []byte(
		`{"version":"2.13.0","config":{"mqtt":{"version":4},"availability":{"enabled":false},"device_options":{"optimistic":false}}}`,
	)
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", []byte(`{}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", badInfo, true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON"}`), false, now.Add(time.Second))
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"OFF"}`), false, now.Add(2*time.Second))
		connection.emit("zigbee2mqtt/test-light", []byte(`{"state":"ON"}`), false, now.Add(3*time.Second))
		connection.emit(
			"zigbee2mqtt/bridge/info",
			readFixture(t, "bridge-info-2.13.0.json"),
			false,
			now.Add(time.Second),
		)
		connection.emit(
			"zigbee2mqtt/test-light/availability",
			[]byte(`{"state":"online"}`),
			false,
			now.Add(time.Second),
		)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 3 && len(session.availability) >= 2 && len(session.observations) == 1
	})
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if string(session.observations[0].Value) != "true" ||
		session.observations[0].AdapterReceivedAt != now.Add(3*time.Second).Format(time.RFC3339Nano) {
		t.Fatalf("replayed pending Observations = %#v, want only latest State", session.observations)
	}
	if session.health[0].Status != adapter.HealthUnhealthy || session.health[0].ReasonCode != invalidInventoryReason ||
		session.health[1].Status != adapter.HealthUnhealthy ||
		session.health[1].ReasonCode != incompatibleConfigurationReason ||
		session.health[len(session.health)-1].Status != adapter.HealthHealthy {
		t.Fatalf("health reports = %#v", session.health)
	}
	assertOrdered(
		t,
		recorder.snapshot(),
		"health:unhealthy:"+invalidInventoryReason,
		"health:unhealthy:"+incompatibleConfigurationReason,
		"health:healthy:",
		"availability",
	)
}

// This test protects fresh availability after an unhealthy transition and fails if pre-offline evidence is replayed.
func TestRunRecoveryDoesNotReplayStaleAvailability(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	connection := newFakeConnection(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
		connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 1 && len(session.availability) == 2
	})

	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"offline"}`), false, now.Add(time.Second))
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 2
	})
	connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), false, now.Add(2*time.Second))
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 3
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if len(session.availability) != 2 {
		t.Fatalf("recovery replayed stale availability: %#v", session.availability)
	}
}

// This test protects availability identity and fails if a reused friendly name transfers evidence between IEEE Devices.
func TestReconciledAvailabilityUsesIEEEIdentity(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	device := runtimeDevice{
		bindingKey:  "z2m-00124b0024abcdee",
		ieeeAddress: "0x00124b0024abcdee",
		friendly:    "reused-name",
		entities: []runtimeEntity{{entityID: "new-power", discovered: discoveredEntity{
			Descriptor: adapter.EntityDescriptor{Key: "power"},
		}}},
	}
	z2m.rememberMapping(adapter.OwnedMapping{
		BindingKey: device.bindingKey, EntityKey: "power", EntityID: "new-power",
	})
	inventory := inventoryDiscovery{Devices: []discoveredDevice{{
		IEEEAddress:  device.ieeeAddress,
		Registration: adapter.Registration{BindingKey: device.bindingKey},
	}}}
	err := z2m.reportReconciledAvailability(
		context.Background(),
		inventory,
		routeSnapshot{devices: map[string]runtimeDevice{device.friendly: device}},
		map[string]availabilityEvidence{
			"0x00124b0024abcdef": {available: true, receivedAt: time.Now().UTC()},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(session.availability) != 0 {
		t.Fatalf("foreign IEEE availability was reported: %#v", session.availability)
	}
}

// This test protects connection generations and bridge recovery. It fails if loss leaves routes healthy, retained
// synchronization is skipped on reconnect, or recovered availability precedes the second healthy acknowledgement.
func TestRunDisconnectsUnhealthyAndResynchronizesNewGeneration(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	connections := []*fakeConnection{newFakeConnection(recorder), newFakeConnection(recorder)}
	for index, connection := range connections {
		generationTime := now.Add(time.Duration(index) * time.Minute)
		connection.onSubscribe = func(connection *fakeConnection) {
			connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, generationTime)
			connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, generationTime)
			connection.emit("zigbee2mqtt/bridge/devices", inventory, true, generationTime)
			connection.emit("zigbee2mqtt/test-light/availability", []byte(`{"state":"online"}`), true, generationTime)
		}
	}
	dialer := &fakeDialer{connections: connections}
	z2m := newRuntimeAdapter(t, session, dialer)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) == 1
	})
	connections[0].lost <- errors.New("broker stopped")
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 3 && session.health[len(session.health)-1].Status == adapter.HealthHealthy
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if dialer.dials != 2 || z2m.generation != 2 {
		t.Fatalf("dials=%d generation=%d", dialer.dials, z2m.generation)
	}
	if session.health[1].Status != adapter.HealthUnhealthy ||
		session.health[1].ReasonCode != externalSystemUnavailableReason {
		t.Fatalf("health reports = %#v", session.health)
	}
	events := recorder.snapshot()
	firstHealthy := indexOf(events, "health:healthy:", 0)
	unhealthy := indexOf(events, "health:unhealthy:"+externalSystemUnavailableReason, firstHealthy+1)
	secondHealthy := indexOf(events, "health:healthy:", unhealthy+1)
	secondAvailability := indexOf(events, "availability", secondHealthy+1)
	if firstHealthy < 0 || unhealthy < 0 || secondHealthy < 0 || secondAvailability < 0 {
		t.Fatalf("recovery ordering events = %v", events)
	}
}

// This test protects loss during reconciliation and fails if a dead MQTT generation reports healthy or available.
func TestRunConnectionLossCancelsReconciliationBeforeHealthy(t *testing.T) {
	t.Parallel()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	registerStarted := make(chan struct{})
	session.registerHook = func(ctx context.Context, _ adapter.Registration) {
		close(registerStarted)
		<-ctx.Done()
	}
	connection := newFakeConnection(recorder)
	inventory, err := json.Marshal([]upstreamDevice{eligibleDevice()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	connection.onSubscribe = func(connection *fakeConnection) {
		connection.emit("zigbee2mqtt/bridge/state", []byte(`{"state":"online"}`), true, now)
		connection.emit("zigbee2mqtt/bridge/info", readFixture(t, "bridge-info-2.13.0.json"), true, now)
		connection.emit("zigbee2mqtt/bridge/devices", inventory, true, now)
	}
	z2m := newRuntimeAdapter(t, session, &fakeDialer{connections: []*fakeConnection{connection}})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- z2m.Run(ctx) }()
	<-registerStarted
	connection.lost <- errors.New("broker stopped during registration")
	waitFor(t, func() bool {
		session.mutex.Lock()
		defer session.mutex.Unlock()
		return len(session.health) >= 1
	})
	cancel()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	for _, report := range session.health {
		if report.Status == adapter.HealthHealthy {
			t.Fatalf("dead generation reported healthy: %#v", session.health)
		}
	}
	if len(session.availability) != 0 {
		t.Fatalf("dead generation reported availability: %#v", session.availability)
	}
}

// This test protects exact topic classification and fails if /set, /get, bridge request/response, nested, group, or
// unknown topics are accidentally interpreted as Device evidence.
func TestTopicClassificationIsExact(t *testing.T) {
	t.Parallel()
	device := runtimeDevice{friendly: "test-light"}
	devices := map[string]runtimeDevice{"test-light": device}
	if classifyBridgeTopic("zigbee2mqtt", "zigbee2mqtt/bridge/devices") != bridgeTopicDevices ||
		classifyBridgeTopic("zigbee2mqtt", "zigbee2mqtt/bridge/request/device/remove") != bridgeTopicUnknown {
		t.Fatal("bridge topic classification was not exact")
	}
	for topic, want := range map[string]deviceTopic{
		"zigbee2mqtt/test-light":              deviceTopicState,
		"zigbee2mqtt/test-light/availability": deviceTopicAvailability,
		"zigbee2mqtt/test-light/set":          deviceTopicUnknown,
		"zigbee2mqtt/test-light/get":          deviceTopicUnknown,
		"zigbee2mqtt/test-light/extra/path":   deviceTopicUnknown,
		"zigbee2mqtt/group":                   deviceTopicUnknown,
		"other/test-light":                    deviceTopicUnknown,
	} {
		if _, got := classifyDeviceTopic("zigbee2mqtt", topic, devices); got != want {
			t.Errorf("classifyDeviceTopic(%q) = %v, want %v", topic, got, want)
		}
	}
	inventory := &inventoryDiscovery{Devices: []discoveredDevice{{FriendlyName: "test-light"}}}
	for topic, want := range map[string]bool{
		"zigbee2mqtt/test-light":                     true,
		"zigbee2mqtt/test-light/availability":        true,
		"zigbee2mqtt/test-light/set":                 false,
		"zigbee2mqtt/bridge/request/device/remove":   false,
		"zigbee2mqtt/unknown-light":                  false,
		"zigbee2mqtt/test-light/additional/segments": false,
	} {
		if got := queueableDeviceTopic("zigbee2mqtt", topic, inventory); got != want {
			t.Errorf("queueableDeviceTopic(%q) = %t, want %t", topic, got, want)
		}
	}
}

func newRuntimeAdapter(t *testing.T, session *fakeSession, dialer mqttDialer) *Adapter {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	z2m, err := newAdapter(
		session,
		Config{MQTTURL: "tcp://127.0.0.1:1883", BaseTopic: "zigbee2mqtt", ClientID: "test-client"},
		logger,
		dialer,
	)
	if err != nil {
		t.Fatal(err)
	}
	z2m.retryDelay = func(time.Duration) time.Duration { return 0 }
	return z2m
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for condition")
		}
		time.Sleep(time.Millisecond)
	}
}

func indexOf(events []string, want string, after int) int {
	for index := max(after, 0); index < len(events); index++ {
		if events[index] == want {
			return index
		}
	}
	return -1
}

func assertOrdered(t *testing.T, events []string, want ...string) {
	t.Helper()
	index := 0
	for _, event := range events {
		if index < len(want) && event == want[index] {
			index++
		}
	}
	if index != len(want) {
		t.Fatalf("events = %v, missing ordered suffix %v", events, want[index:])
	}
}
