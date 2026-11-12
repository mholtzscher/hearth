package esphome //nolint:testpackage // The harness drives unexported dialer and timing seams.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/apipb"
	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi/nativeapitest"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

// call is one recorded Session or responder interaction, in order.
type call struct {
	kind         string
	availability []adapter.EntityAvailabilityReport
	observation  adapter.Observation
	linkedTo     string
	registration adapter.Registration
	health       adapter.HealthReport
}

// fakeSession records every Session call. Register binds each Entity key to "ent_<key>".
type fakeSession struct {
	mu    sync.Mutex
	owned []adapter.OwnedMapping
	calls []call
}

func (session *fakeSession) record(entry call) {
	session.mu.Lock()
	defer session.mu.Unlock()
	session.calls = append(session.calls, entry)
}

func (session *fakeSession) snapshot() []call {
	session.mu.Lock()
	defer session.mu.Unlock()
	return append([]call(nil), session.calls...)
}

func (session *fakeSession) ListOwnedMappings(
	_ context.Context,
	_ adapter.OwnedMappingPageRequest,
) (adapter.OwnedMappingPage, error) {
	return adapter.OwnedMappingPage{Items: session.owned}, nil
}

func (session *fakeSession) Register(_ context.Context, registration adapter.Registration) (adapter.Binding, error) {
	session.record(call{kind: "register", registration: registration})
	binding := adapter.Binding{BindingKey: registration.BindingKey, DeviceID: "dev_" + registration.BindingKey}
	for _, entity := range registration.Entities {
		binding.Entities = append(
			binding.Entities,
			adapter.EntityBinding{Key: entity.Key, EntityID: "ent_" + entity.Key, Enabled: true},
		)
	}
	return binding, nil
}

func (session *fakeSession) SetHealth(_ context.Context, report adapter.HealthReport) error {
	session.record(call{kind: "health", health: report})
	return nil
}

func (session *fakeSession) ReportEntityAvailability(
	_ context.Context,
	reports []adapter.EntityAvailabilityReport,
) error {
	session.record(
		call{kind: "availability", availability: append([]adapter.EntityAvailabilityReport(nil), reports...)},
	)
	return nil
}

func (session *fakeSession) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	session.record(call{kind: "observation", observation: observation})
	return "obs_1", nil
}

// fakeResponder records the Command's response; Accept returns evidence that records linked Observations.
type fakeResponder struct {
	session  *fakeSession
	command  string
	mu       sync.Mutex
	response string
	message  string
}

func (responder *fakeResponder) Accept() (adapter.CommandEvidence, error) {
	responder.set("accepted", "")
	return &fakeEvidence{session: responder.session, command: responder.command}, nil
}

func (responder *fakeResponder) Reject(message string) error {
	responder.set("rejected", message)
	return nil
}

func (responder *fakeResponder) RejectUnavailable(message string) error {
	responder.set("unavailable", message)
	return nil
}

func (responder *fakeResponder) set(response, message string) {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	if responder.response != "" {
		panic("responded twice")
	}
	responder.response, responder.message = response, message
}

func (responder *fakeResponder) result() string {
	responder.mu.Lock()
	defer responder.mu.Unlock()
	return responder.response
}

type fakeEvidence struct {
	session *fakeSession
	command string
}

func (evidence *fakeEvidence) PublishObservation(
	_ context.Context,
	observation adapter.Observation,
) (adapter.ObservationID, error) {
	evidence.session.record(call{kind: "linked", observation: observation, linkedTo: evidence.command})
	return "obs_linked", nil
}

// pipeDialer serves scripted devices over [net.Pipe], keyed by address, and exposes each served session.
type pipeDialer struct {
	mu       sync.Mutex
	devices  map[string]nativeapitest.Device
	sessions map[string][]*nativeapitest.Session
	fail     map[string]error
}

func newPipeDialer() *pipeDialer {
	return &pipeDialer{
		devices:  map[string]nativeapitest.Device{},
		sessions: map[string][]*nativeapitest.Session{},
		fail:     map[string]error{},
	}
}

func (dialer *pipeDialer) Dial(ctx context.Context, config nativeapi.DialConfig) (nativeapi.Conn, error) {
	dialer.mu.Lock()
	device, known := dialer.devices[config.Address]
	failure := dialer.fail[config.Address]
	dialer.mu.Unlock()
	if failure != nil {
		return nil, failure
	}
	if !known {
		return nil, errors.New("connection refused")
	}
	client, server := net.Pipe()
	session := device.Serve(server)
	dialer.mu.Lock()
	dialer.sessions[config.Address] = append(dialer.sessions[config.Address], session)
	dialer.mu.Unlock()
	conn, err := nativeapi.Open(ctx, client, config)
	if err != nil {
		_ = client.Close()
		return nil, err
	}
	return conn, nil
}

func (dialer *pipeDialer) latest(t *testing.T, address string) *nativeapitest.Session {
	t.Helper()
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	sessions := dialer.sessions[address]
	if len(sessions) == 0 {
		t.Fatalf("no session for %s", address)
	}
	return sessions[len(sessions)-1]
}

func (dialer *pipeDialer) setFailure(address string, err error) {
	dialer.mu.Lock()
	defer dialer.mu.Unlock()
	dialer.fail[address] = err
}

func harnessPSK() nativeapi.PSK { return nativeapi.PSK{1, 2, 3} }

// testDevice is a small device: a temperature sensor, a switch, a dimmable RGB light, and a binary sensor.
func testDevice(macText string) nativeapitest.Device {
	return nativeapitest.Device{
		PSK:  harnessPSK(),
		Name: "node",
		Info: &apipb.DeviceInfoResponse{MacAddress: macText, Name: "node", FriendlyName: "Test Node"},
		Entities: []proto.Message{
			&apipb.ListEntitiesSensorResponse{
				Key:               1,
				Name:              "Temperature",
				DeviceClass:       "temperature",
				UnitOfMeasurement: "°C",
			},
			&apipb.ListEntitiesSwitchResponse{Key: 2, Name: "Relay"},
			&apipb.ListEntitiesLightResponse{
				Key: 3, Name: "Lamp", SupportedColorModes: []apipb.ColorMode{apipb.ColorMode_COLOR_MODE_RGB},
			},
			&apipb.ListEntitiesBinarySensorResponse{Key: 4, Name: "Motion", DeviceClass: "motion"},
		},
		States: []proto.Message{
			&apipb.SensorStateResponse{Key: 1, State: 21.5},
			&apipb.SwitchStateResponse{Key: 2, State: false},
			&apipb.LightStateResponse{Key: 3, State: false, Brightness: 1},
			&apipb.BinarySensorStateResponse{Key: 4, State: false},
		},
	}
}

func harnessDevice(t *testing.T, macText, address string) Device {
	t.Helper()
	mac, err := ParseMAC(macText)
	if err != nil {
		t.Fatal(err)
	}
	return Device{MAC: mac, Address: address, Port: 6053, Transport: EncryptedTransport(harnessPSK())}
}

func harnessTiming() timing {
	value := defaultTiming()
	value.jitter = func(delay time.Duration) time.Duration { return delay }
	return value
}

// runAdapter starts Run in the bubble and returns a stop function that cancels and joins it.
func runAdapter(t *testing.T, session *fakeSession, dialer *pipeDialer, devices ...Device) (*Adapter, func() error) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	instance, err := newAdapter(session, Config{Static: devices}, logger, dialer, harnessTiming(), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	result := make(chan error, 1)
	go func() { result <- instance.Run(ctx) }()
	stop := sync.OnceValue(func() error {
		cancel()
		return <-result
	})
	t.Cleanup(func() { _ = stop() })
	return instance, stop
}

// entityIDFor finds the Entity ID registered for an external ID.
func entityIDFor(t *testing.T, session *fakeSession, externalID string) string {
	t.Helper()
	for _, entry := range session.snapshot() {
		if entry.kind != "register" {
			continue
		}
		for _, entity := range entry.registration.Entities {
			if entity.ExternalID == externalID {
				return "ent_" + entity.Key
			}
		}
	}
	t.Fatalf("no registered Entity %q", externalID)
	return ""
}

// command sends one Command through HandleCommand on its own goroutine.
func command(
	t *testing.T,
	instance *Adapter,
	session *fakeSession,
	entityID, id string,
	parameters string,
) (*fakeResponder, <-chan error) {
	t.Helper()
	responder := &fakeResponder{session: session, command: id}
	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		done <- instance.HandleCommand(ctx, adapter.Command{
			ID: id, EntityID: entityID, OperationName: "set", Parameters: json.RawMessage(parameters),
		}, responder)
	}()
	return responder, done
}

func callsOfKind(calls []call, kind string) []call {
	var matched []call
	for _, entry := range calls {
		if entry.kind == kind {
			matched = append(matched, entry)
		}
	}
	return matched
}

// lastAvailability returns each Entity's last reported availability.
func lastAvailability(calls []call) map[string]adapter.EntityAvailabilityReport {
	latest := map[string]adapter.EntityAvailabilityReport{}
	for _, entry := range callsOfKind(calls, "availability") {
		for _, report := range entry.availability {
			latest[report.EntityID] = report
		}
	}
	return latest
}
