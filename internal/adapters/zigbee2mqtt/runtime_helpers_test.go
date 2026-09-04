package zigbee2mqtt //nolint:testpackage // Runtime tests use the package-private MQTT seam.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
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
	publishHook  func(context.Context, adapter.Observation, bool) error
	health       []adapter.HealthReport
	availability []adapter.EntityAvailabilityReport
	observations []adapter.Observation
	linked       []adapter.Observation
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
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	if session.publishHook != nil {
		if err := session.publishHook(ctx, observation, false); err != nil {
			return "obs-test", err
		}
	}
	session.mutex.Lock()
	session.observations = append(session.observations, observation)
	session.mutex.Unlock()
	session.recorder.add("observation")
	return "obs-test", nil
}

func (session *fakeSession) publishLinked(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	if session.publishHook != nil {
		if err := session.publishHook(ctx, observation, true); err != nil {
			return "obs-linked", err
		}
	}
	session.mutex.Lock()
	session.linked = append(session.linked, observation)
	session.mutex.Unlock()
	session.recorder.add("linked-observation")
	return "obs-linked", nil
}

type fakeEvidence struct{ session *fakeSession }

func (evidence fakeEvidence) PublishObservation(
	ctx context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	return evidence.session.publishLinked(ctx, observation)
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

type fakeResponder struct {
	mutex       sync.Mutex
	recorder    *runtimeRecorder
	evidence    adapter.CommandEvidence
	acceptErr   error
	accepted    int
	rejected    int
	unavailable int
}

func newFakeResponder(recorder *runtimeRecorder, session *fakeSession) *fakeResponder {
	return &fakeResponder{recorder: recorder, evidence: fakeEvidence{session: session}}
}

func (responder *fakeResponder) Accept() (adapter.CommandEvidence, error) {
	responder.mutex.Lock()
	responder.accepted++
	err := responder.acceptErr
	evidence := responder.evidence
	responder.mutex.Unlock()
	responder.recorder.add("accept")
	if err != nil {
		return nil, err
	}
	return evidence, nil
}

func (responder *fakeResponder) Reject(string) error {
	responder.mutex.Lock()
	responder.rejected++
	responder.mutex.Unlock()
	responder.recorder.add("reject")
	return nil
}

func (responder *fakeResponder) RejectUnavailable(string) error {
	responder.mutex.Lock()
	responder.unavailable++
	responder.mutex.Unlock()
	responder.recorder.add("unavailable")
	return nil
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

func startCoordinator(t *testing.T, z2m *Adapter) *runtimeCoordinator {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	coordinator := newRuntimeCoordinator(ctx, z2m)
	go func() { _ = coordinator.run() }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-z2m.runtimeDone:
		case <-time.After(3 * time.Second):
			t.Fatal("runtime coordinator did not stop")
		}
	})
	return coordinator
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

func commandReadyAdapter(
	t *testing.T,
	recorder *runtimeRecorder,
	session *fakeSession,
) (*Adapter, *runtimeCoordinator, *fakeConnection, runtimeDevice) {
	t.Helper()
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	coordinator := startCoordinator(t, z2m)
	connection := newFakeConnection(recorder)
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rcb01057z.json")
	binding := adapter.Binding{BindingKey: device.Registration.BindingKey, DeviceID: "dev-test"}
	for _, entity := range device.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{
				Key:      entity.Descriptor.Key,
				EntityID: "entity-" + entity.Descriptor.Key,
				Enabled:  true,
			},
		)
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
	result := <-activation
	if result.err != nil {
		t.Fatal(result.err)
	}
	return z2m, coordinator, connection, runtime
}

func publishState(
	ctx context.Context,
	z2m *Adapter,
	device runtimeDevice,
	payload string,
	receivedAt time.Time,
) error {
	return z2m.publishDeviceState(ctx, 1, 1, device, mqttMessage{
		Topic:      "zigbee2mqtt/" + device.friendly,
		Payload:    []byte(payload),
		ReceivedAt: receivedAt,
	})
}

func testCommand(entityID, parameters string) adapter.Command {
	return adapter.Command{
		ID: "cmd-test", CorrelationID: "cor-test", EntityID: entityID, OperationName: "set",
		Parameters: json.RawMessage(parameters), Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
}
