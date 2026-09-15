package nats //nolint:testpackage // Tests exercise package-private NATS wire behavior and fixtures.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

type registrarFunc func(
	context.Context,
	string,
	devices.RuntimeID,
	devices.Registration,
) (devices.Binding, error)

func (registrar registrarFunc) Register(
	ctx context.Context,
	adapterID string,
	runtimeID devices.RuntimeID,
	registration devices.Registration,
) (devices.Binding, error) {
	return registrar(ctx, adapterID, runtimeID, registration)
}

func TestRegistrationServerMapsDomainRegistrationAndReturnsCorrelatedResponse(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	externalID := "device.office"
	initiallyEnabled := false
	handled := make(chan devices.Registration, 1)
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		_ context.Context,
		adapterID string,
		runtimeID devices.RuntimeID,
		registration devices.Registration,
	) (devices.Binding, error) {
		if adapterID != "simulator" || runtimeID != devices.RuntimeID(testRuntimeID) {
			t.Errorf("Adapter runtime = %q/%q", adapterID, runtimeID)
		}
		handled <- registration
		return devices.Binding{
			BindingKey: registration.BindingKey,
			DeviceID:   devices.DeviceID("dev_01890f47-7a6b-7c4d-8e9f-0123456789ab"),
			Entities: []devices.EntityBinding{
				{Key: "power", EntityID: devices.EntityID(testEntityID), Enabled: true},
				{
					Key:      "brightness",
					EntityID: devices.EntityID("ent_01890f47-7a6b-7c4d-8e9f-1123456789ab"),
					Enabled:  true,
				},
			},
		}, nil
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	requestID := "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab"
	request := natswire.Envelope[registration]{
		ID: requestID, Schema: contractsv1.RegistrationRequestSchemaID,
		EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: testCorrelationID,
		Data: registration{
			BindingKey: "office-light",
			Device:     deviceDescriptor{ExternalID: &externalID, Name: "Office light", Kind: "light"},
			Entities: []entityDescriptor{
				{
					Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
					Support: []byte(`{"state":{},"operations":{"set":{}}}`), InitiallyEnabled: &initiallyEnabled,
				},
				{
					Key:        "brightness",
					ExternalID: "light.office.brightness",
					Name:       "Brightness",
					Type:       "hearth.brightness/v1",
					Support:    []byte(`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`),
				},
			},
		},
	}
	response := requestRegistration(t, connection, validator, request)
	if response.CausationID == nil || *response.CausationID != requestID ||
		response.CorrelationID != testCorrelationID || response.Data.Binding == nil ||
		len(response.Data.Binding.Entities) != 2 || response.Data.Binding.Entities[0].EntityID != testEntityID ||
		!response.Data.Binding.Entities[0].Enabled || response.Data.Binding.Entities[1].Key != "brightness" {
		t.Fatalf("response = %#v", response)
	}
	mapped := <-handled
	if mapped.Device.Kind != devices.DeviceKindLight || mapped.Device.ExternalID == nil ||
		*mapped.Device.ExternalID != externalID || len(mapped.Entities) != 2 ||
		mapped.Entities[0].TypeID != devices.EntityTypeID("hearth.power/v1") ||
		mapped.Entities[1].TypeID != devices.EntityTypeID("hearth.brightness/v1") ||
		string(mapped.Entities[0].Support) != `{"state":{},"operations":{"set":{}}}` ||
		mapped.Entities[0].InitiallyEnabled == nil || *mapped.Entities[0].InitiallyEnabled {
		t.Fatalf("mapped registration = %#v", mapped)
	}
	externalID = "mutated"
	initiallyEnabled = true
	request.Data.Entities[0].Support[0] = 'x'
	if *mapped.Device.ExternalID != "device.office" ||
		string(mapped.Entities[0].Support) != `{"state":{},"operations":{"set":{}}}` ||
		*mapped.Entities[0].InitiallyEnabled {
		t.Fatalf("registrar inputs were not copied: %#v", mapped)
	}
}

func TestRegistrationServerMapsDomainRejection(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Registration,
	) (devices.Binding, error) {
		return devices.Binding{}, &devices.RegistrationRejectedError{
			Code: devices.RegistrationIdentityConflict, Message: "binding is already owned",
		}
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	response := requestRegistration(t, connection, validator, validRegistrationEnvelope())
	if response.Data.Status != "rejected" || response.Data.Error == nil ||
		response.Data.Error.Code != "identity_conflict" || response.Data.Error.Message != "binding is already owned" {
		t.Fatalf("response = %#v", response)
	}
}

func TestRegistrationServerMapsRuntimeFencingToTypedRejection(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Registration,
	) (devices.Binding, error) {
		return devices.Binding{}, devices.ErrRuntimeFenced
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	response := requestRegistration(t, connection, validator, validRegistrationEnvelope())
	if response.Data.Status != "rejected" || response.Data.Error == nil ||
		response.Data.Error.Code != "runtime_fenced" {
		t.Fatalf("response = %#v", response)
	}
}

func TestStaleRuntimeInvalidRegistrationFencesSDKSession(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	server, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if migrateErr := platformdb.Migrate(ctx, database); migrateErr != nil {
		t.Fatal(migrateErr)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	service := devices.NewService(devicessqlite.DeviceStores(repository), nil, catalog, devices.Dependencies{})
	sessions, err := StartSessionServer(connection, validator, service, service, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := StartRegistrationServer(connection, validator, service, slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })

	staleSession, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = staleSession.Close() })
	var runtimeValue string
	if scanErr := database.QueryRowContext(
		ctx,
		"SELECT active_runtime_id FROM adapter_instances WHERE adapter_id = 'simulator'",
	).Scan(&runtimeValue); scanErr != nil {
		t.Fatal(scanErr)
	}
	staleRuntime, err := devices.ParseRuntimeID(runtimeValue)
	if err != nil {
		t.Fatal(err)
	}
	if releaseErr := service.ReleaseAdapterRuntime(ctx, "simulator", staleRuntime); releaseErr != nil {
		t.Fatal(releaseErr)
	}
	replacement, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = replacement.Close() })

	_, err = staleSession.Register(ctx, adapter.Registration{
		BindingKey: "invalid-light",
		Device:     adapter.DeviceDescriptor{Name: "Invalid light", Kind: "light"},
		Entities: []adapter.EntityDescriptor{{
			Key: "power", ExternalID: "invalid.light", Name: "Power",
			Type: "unknown.entity/v1", Support: json.RawMessage(`{"state":{},"operations":{}}`),
		}},
	})
	if !errors.Is(err, adapter.ErrRuntimeFenced) {
		t.Fatalf("stale invalid Registration error = %v", err)
	}
	if _, followupErr := staleSession.SetEntityEnabled(
		ctx, testEntityID, false,
	); !errors.Is(followupErr, adapter.ErrRuntimeFenced) {
		t.Fatalf("fenced session follow-up error = %v", followupErr)
	}
}

func TestRegistrationInfrastructureFailureDoesNotReply(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Registration,
	) (devices.Binding, error) {
		return devices.Binding{}, errors.New("SQLite unavailable")
	}), slog.New(slog.DiscardHandler))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	request := validRegistrationEnvelope()
	payload, err := natswire.Encode(validator, contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.RegistrationSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err = connection.RequestMsgWithContext(
		ctx,
		&natsgo.Msg{Subject: subject, Header: make(natsgo.Header), Data: payload},
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("request error = %v", err)
	}
}

func validRegistrationEnvelope() natswire.Envelope[registration] {
	return natswire.Envelope[registration]{
		ID:            "reg_01890f47-7a6b-7c4d-8e9f-0123456789ab",
		Schema:        contractsv1.RegistrationRequestSchemaID,
		EmittedAt:     "2026-08-20T12:34:56Z",
		CorrelationID: testCorrelationID,
		Data: registration{
			BindingKey: "office-light",
			Device:     deviceDescriptor{Name: "Office light", Kind: "light"},
			Entities: []entityDescriptor{{
				Key: "power", ExternalID: "light.office", Name: "Power", Type: "hearth.power/v1",
				Support: []byte(`{"state":{},"operations":{"set":{}}}`),
			}},
		},
	}
}

func requestRegistration(
	t *testing.T,
	connection *natsgo.Conn,
	validator *contractsv1.Validator,
	request natswire.Envelope[registration],
) natswire.Envelope[registrationResponse] {
	t.Helper()
	payload, err := natswire.Encode(validator, contractsv1.RegistrationRequestSchemaID, request)
	if err != nil {
		t.Fatal(err)
	}
	subject, err := natswire.RegistrationSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	reply, err := connection.RequestMsgWithContext(context.Background(), &natsgo.Msg{
		Subject: subject, Header: make(natsgo.Header), Data: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	response, err := natswire.Decode[registrationResponse](
		validator,
		contractsv1.RegistrationResponseSchemaID,
		reply.Data,
	)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

// This test protects shared request/reply discard diagnostics and fails if a
// malicious payload is answered, logged with its secrets, or reported without
// a fixed error code and kind.
func TestRegistrationServerDiscardsMaliciousPayloadWithoutLoggingIt(t *testing.T) {
	t.Parallel()
	_, connection, _ := startJetStream(t)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	logs, observer, logger := newContextObservingSink(observedSpanContext)
	handled := make(chan struct{}, 1)
	server, err := StartRegistrationServer(connection, validator, registrarFunc(func(
		context.Context,
		string,
		devices.RuntimeID,
		devices.Registration,
	) (devices.Binding, error) {
		handled <- struct{}{}
		return devices.Binding{}, nil
	}), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = server.Drain() })

	subject, err := natswire.RegistrationSubject("simulator", testRuntimeID)
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithTimeout(t.Context(), 300*time.Millisecond)
	defer cancel()
	discardHeaders := make(natsgo.Header)
	natswire.InjectTrace(testTraceContext(t), discardHeaders)
	_, requestErr := connection.RequestMsgWithContext(requestContext, &natsgo.Msg{
		Subject: subject, Header: discardHeaders,
		Data: []byte(`{"token":"s3cr3t-registration-token","binding_key":"office-light"}`),
	})
	if requestErr == nil {
		t.Fatal("malicious registration payload unexpectedly received a reply")
	}
	select {
	case <-handled:
		t.Fatal("malicious registration payload reached the registrar")
	default:
	}

	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if len(logEvents(logs.records(t), "transport.request_discarded")) == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	discarded := logEvents(logs.records(t), "transport.request_discarded")
	if len(discarded) != 1 {
		t.Fatalf("transport.request_discarded events = %d, want 1:\n%s", len(discarded), logs.output())
	}
	if discarded[0]["level"] != "WARN" {
		t.Fatalf("discarded level = %#v, want WARN", discarded[0]["level"])
	}
	if discarded[0]["error_code"] != "request_decode_failed" || discarded[0]["kind"] != "registration" {
		t.Fatalf("discarded record = %#v", discarded[0])
	}
	for _, key := range []string{"subject", "error"} {
		if _, ok := discarded[0][key]; ok {
			t.Fatalf("discarded record logs unsafe %q (record = %#v)", key, discarded[0])
		}
	}
	if output := logs.output(); len(output) == 0 || strings.Contains(output, "s3cr3t-registration-token") {
		t.Fatalf("discard logs missing or contain sensitive payload:\n%s", output)
	}
	if !observer.allObserved() {
		t.Fatal("discard log emission lost the extracted operation context")
	}
}
