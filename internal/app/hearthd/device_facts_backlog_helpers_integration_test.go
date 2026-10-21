//go:build integration

package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/contracts/v1/natswire"
	"github.com/mholtzscher/hearth/internal/modules/adapters"
	adaptersnats "github.com/mholtzscher/hearth/internal/modules/adapters/nats"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
)

// assertDeviceFactStreamSet pins Core's inbound evidence, Device Fact, and
// independently delivered Adapter health stream identities.
func assertDeviceFactStreamSet(ctx context.Context, t *testing.T, js jetstream.JetStream) {
	t.Helper()
	names := js.StreamNames(ctx)
	var streamNames []string
	for name := range names.Name() {
		streamNames = append(streamNames, name)
	}
	if nameErr := names.Err(); nameErr != nil {
		t.Fatal(nameErr)
	}
	expected := map[string]bool{
		adaptersnats.HealthStreamName:     true,
		devicesnats.ObservationStreamName: true,
		devicesnats.EntityEventStreamName: true,
		devicesnats.DeviceFactStreamName:  true,
	}
	if len(streamNames) != len(expected) {
		t.Fatalf("streams = %v, want exactly %v", streamNames, expected)
	}
	for _, name := range streamNames {
		if !expected[name] {
			t.Fatalf("unexpected stream %q", name)
		}
	}
}

// seedDeviceFactsRegistration registers one power Entity and one event-source
// Entity, returning the claimed Runtime and both canonical Entity IDs.
func seedDeviceFactsRegistration(
	ctx context.Context,
	t *testing.T,
	databasePath string,
) (adapters.RuntimeID, devices.EntityID, devices.EntityID) {
	t.Helper()
	database := dbtest.OpenMigrated(t, databasePath)
	defer func() { _ = database.Close() }()
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		testDeviceStores(devicessqlite.NewDeviceRepository(database, catalog), database), nil, catalog,
		devices.Dependencies{},
	)
	runtimeID := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	if claimErr := adapterServiceForTest(database).ClaimAdapterRuntime(ctx, adapters.ClaimAdapterRuntimeParams{
		AdapterID: "simulator", RuntimeID: runtimeID,
		SoftwareName: "hearth-facts-test", SoftwareVersion: "0.1.0",
	}); claimErr != nil {
		t.Fatal(claimErr)
	}
	binding, err := service.Register(ctx, "simulator", runtimeID, devices.Registration{
		BindingKey: "facts-light",
		Device:     devices.DeviceDescriptor{Name: "Facts light", Kind: devices.DeviceKindLight},
		Entities: []devices.EntityDescriptor{
			{
				Key: "power", ExternalID: "facts.power", Name: "Power",
				TypeID:  devices.EntityTypePowerV1,
				Support: devices.EntitySupport(`{"state":{},"operations":{"set":{}}}`),
			},
			{
				Key: "events", ExternalID: "facts.events", Name: "Events",
				TypeID: devices.EntityTypeEnumeventV1,
				Support: devices.EntitySupport(
					`{"state":{},"operations":{},"events":{"names":["single_press"]}}`,
				),
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	powerEntityID, eventEntityID := binding.Entities[0].EntityID, binding.Entities[1].EntityID
	return runtimeID, powerEntityID, eventEntityID
}

// publishBacklogObservation stores one schema-valid Observation envelope carrying
// the fixed trace context in the inbound observation stream before or after Core
// starts, exactly as a traced SDK would.
func publishBacklogObservation(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID adapters.RuntimeID,
	entityID devices.EntityID,
	observationID string,
	value string,
) string {
	t.Helper()
	subject, err := natswire.ObservationSubject("simulator", string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	type wireObservation struct {
		EntityID          string          `json:"entity_id"`
		Value             json.RawMessage `json:"value"`
		AdapterReceivedAt string          `json:"adapter_received_at"`
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	payload, err := natswire.Encode(validator, contractsv1.ObservationSchemaID,
		natswire.Envelope[wireObservation]{
			ID: observationID, Schema: contractsv1.ObservationSchemaID,
			EmittedAt: now.Format(time.RFC3339Nano), CorrelationID: string(correlationID),
			Data: wireObservation{
				EntityID: string(entityID), Value: json.RawMessage(value),
				AdapterReceivedAt: now.Format(time.RFC3339Nano),
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{observationID}}
	headers.Set("traceparent", factsTraceparent)
	headers.Set("tracestate", factsTracestate)
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  headers,
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return observationID
}

// publishBacklogEntityEvent stores one schema-valid Entity Event envelope
// carrying the fixed trace context in the inbound entity event stream.
func publishBacklogEntityEvent(
	ctx context.Context,
	t *testing.T,
	js jetstream.JetStream,
	validator *contractsv1.Validator,
	runtimeID adapters.RuntimeID,
	entityID devices.EntityID,
	eventID string,
	name string,
) string {
	t.Helper()
	subject, err := natswire.EntityEventSubject("simulator", string(runtimeID), string(entityID))
	if err != nil {
		t.Fatal(err)
	}
	correlationID, err := devices.NewCorrelationID()
	if err != nil {
		t.Fatal(err)
	}
	type wireEntityEvent struct {
		EntityID string `json:"entity_id"`
		Name     string `json:"name"`
	}
	payload, err := natswire.Encode(validator, contractsv1.EntityEventSchemaID,
		natswire.Envelope[wireEntityEvent]{
			ID: eventID, Schema: contractsv1.EntityEventSchemaID,
			EmittedAt: time.Now().UTC().Format(time.RFC3339Nano), CorrelationID: string(correlationID),
			Data: wireEntityEvent{EntityID: string(entityID), Name: name},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	headers := natsgo.Header{natsgo.MsgIdHdr: []string{eventID}}
	headers.Set("traceparent", factsTraceparent)
	headers.Set("tracestate", factsTracestate)
	if _, publishErr := js.PublishMsg(ctx, &natsgo.Msg{
		Subject: subject,
		Header:  headers,
		Data:    payload,
	}); publishErr != nil {
		t.Fatal(publishErr)
	}
	return eventID
}
