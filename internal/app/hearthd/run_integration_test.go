package hearthd

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	platformnats "github.com/mholtzscher/hearth/internal/platform/nats"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

func TestCoreNATSTransportRegistersAndProjectsDurableObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	service, err := devices.New(ctx, database, logger)
	if err != nil {
		t.Fatal(err)
	}

	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
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
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	stopModule := runIntegrationDeviceService(t, service, devicesnats.NewCommandDelivery(platformnats.NewCommandClient(coreConnection, validator)))
	registrations, err := platformnats.StartRegistrationServer(coreConnection, validator, devicesnats.RegistrationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	observations, err := platformnats.StartObservationConsumer(ctx, durable, validator, devicesnats.ObservationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)

	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: "simulator", NATSURL: server.ClientURL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "office-light",
		Device:     adapter.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities: []adapter.EntityDescriptor{{
			Key: "power", ExternalID: "sim.light", Name: "Power", Type: "hearth.power/v1",
			Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := devices.ParseEntityID(binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	adapterReceivedAt := time.Now().UTC()
	wireObservation := adapter.Observation{
		EntityID: binding.Entities[0].EntityID, Value: json.RawMessage(`true`),
		AdapterReceivedAt: adapterReceivedAt.Format(time.RFC3339Nano),
	}
	observationID, err := session.PublishObservation(ctx, wireObservation)
	if err != nil {
		t.Fatal(err)
	}

	var projected devices.State
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		view, getErr := service.GetEntity(ctx, entityID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		if view.State != nil {
			projected = *view.State
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if projected.ObservationID == "" {
		t.Fatal("observation was not projected")
	}
	if projected.ObservationID != devices.ObservationID(observationID) || string(projected.Value) != "true" {
		t.Fatalf("projected state = %#v", projected)
	}
	if err := NewRuntimeReadiness(database, coreConnection, js, observations).Check(ctx); err != nil {
		t.Fatal(err)
	}
	for time.Now().Before(deadline) {
		info, infoErr := durable.Info(ctx)
		if infoErr != nil {
			t.Fatal(infoErr)
		}
		if info.NumAckPending == 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	info, err := durable.Info(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if info.NumAckPending != 0 {
		t.Fatalf("observation remained unacknowledged: %#v", info)
	}

	observations.Stop()
	select {
	case <-observations.Closed():
	case <-time.After(time.Second):
		t.Fatal("observation consumer did not stop")
	}
	stopModule()
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	database, err = platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	recoveredService, err := devices.New(ctx, database, logger)
	if err != nil {
		t.Fatal(err)
	}
	runIntegrationDeviceService(t, recoveredService, devicesnats.NewCommandDelivery(platformnats.NewCommandClient(coreConnection, validator)))
	recovered, err := recoveredService.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State == nil || recovered.State.ObservationID != projected.ObservationID ||
		recovered.State.ReceiveOrder != projected.ReceiveOrder || string(recovered.State.Value) != "true" {
		t.Fatalf("recovered state = %#v, want %#v", recovered.State, projected)
	}
	result, err := recoveredService.ReceiveObservation(ctx, devices.ReceivedObservation{
		AdapterID: "simulator",
		Observation: devices.Observation{
			ID: devices.ObservationID(observationID), EntityID: entityID, Value: devices.Value(`true`),
			AdapterReceivedAt: adapterReceivedAt,
		},
		ObservedAt: projected.ObservedAt,
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionDuplicate {
		t.Fatalf("recovered redelivery = %#v", result)
	}
	var receipts int
	if err := database.QueryRowContext(ctx, "SELECT count(*) FROM observation_receipts").Scan(&receipts); err != nil {
		t.Fatal(err)
	}
	if receipts != 1 {
		t.Fatalf("receipt count after recovered redelivery = %d", receipts)
	}
}

func TestCoreCommandRoundTripRequiresLinkedSimulatorObservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	service, err := devices.New(ctx, database, logger)
	if err != nil {
		t.Fatal(err)
	}

	server, err := natsserver.NewServer(&natsserver.Options{
		Host: "127.0.0.1", Port: -1, JetStream: true, StoreDir: t.TempDir(), NoSigs: true,
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
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := platformnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	runIntegrationDeviceService(t, service, devicesnats.NewCommandDelivery(platformnats.NewCommandClient(coreConnection, validator)))
	registrations, err := platformnats.StartRegistrationServer(coreConnection, validator, devicesnats.RegistrationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	observations, err := platformnats.StartObservationConsumer(ctx, durable, validator, devicesnats.ObservationHandler(service), logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)

	session, err := adapter.Connect(ctx, adapter.Config{AdapterID: "simulator", NATSURL: server.ClientURL()})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = session.Close() })
	support := contractpowerv1.Support{
		State: contractpowerv1.StateSupport{},
		Operations: contractpowerv1.OperationSupport{
			Set: contractpowerv1.SetSupport{},
		},
	}
	descriptor, err := sdkpowerv1.NewEntityDescriptor(adapter.EntityMetadata{
		Key: "power", ExternalID: "sim.light", Name: "Power",
	}, support)
	if err != nil {
		t.Fatal(err)
	}
	binding, err := session.Register(ctx, adapter.Registration{
		BindingKey: "office-light",
		Device:     adapter.DeviceDescriptor{Name: "Office light", Kind: "light"},
		Entities:   []adapter.EntityDescriptor{descriptor},
	})
	if err != nil {
		t.Fatal(err)
	}
	entityID, err := devices.ParseEntityID(binding.Entities[0].EntityID)
	if err != nil {
		t.Fatal(err)
	}
	handler, err := sdkpowerv1.NewCommandHandler(string(entityID), support, sdkpowerv1.Handlers{
		Set: func(commandContext context.Context, command sdkpowerv1.SetCommand, responder adapter.Responder) error {
			if err := responder.Accept(); err != nil {
				return err
			}
			commandID := command.ID
			observation, err := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
				EntityID: string(entityID), Support: support, State: contractpowerv1.State(command.Parameters.Value),
				AdapterReceivedAt: time.Now().UTC(), RefreshForCommand: &commandID,
			})
			if err != nil {
				return err
			}
			_, err = session.PublishObservation(commandContext, observation)
			return err
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	serveContext, stopServing := context.WithCancel(ctx)
	defer stopServing()
	baselineSubscriptions := server.NumSubscriptions()
	serveErrors := make(chan error, 1)
	go func() { serveErrors <- session.ServeCommands(serveContext, handler) }()
	deadline := time.Now().Add(time.Second)
	for server.NumSubscriptions() <= baselineSubscriptions && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if server.NumSubscriptions() <= baselineSubscriptions {
		t.Fatal("simulator command subscription did not become active")
	}

	result, err := service.ExecuteCommand(ctx, entityID, devices.OperationNameSet, devices.CommandParameters(`{"value":true}`))
	if err != nil {
		t.Fatal(err)
	}
	if string(result.Value) != "true" {
		t.Fatalf("command result = %#v", result)
	}
	var status string
	var acceptedAt, outcomeObservationID sql.NullString
	if err := database.QueryRowContext(ctx,
		"SELECT status, accepted_at, outcome_observation_id FROM commands WHERE id = ?", result.CommandID,
	).Scan(&status, &acceptedAt, &outcomeObservationID); err != nil {
		t.Fatal(err)
	}
	if status != "satisfied" || !acceptedAt.Valid || !outcomeObservationID.Valid ||
		outcomeObservationID.String != string(result.ObservationID) {
		t.Fatalf("stored Command = %q %#v %#v", status, acceptedAt, outcomeObservationID)
	}
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != result.ObservationID || string(view.State.Value) != "true" {
		t.Fatalf("entity view = %#v", view)
	}
	stopServing()
	select {
	case err := <-serveErrors:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("serve commands: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("simulator command server did not stop")
	}
}

func runIntegrationDeviceService(t *testing.T, service *devices.Service, delivery devices.CommandDelivery) func() {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	errorsChannel := make(chan error, 1)
	go func() { errorsChannel <- service.Run(ctx, delivery) }()
	var once sync.Once
	stop := func() {
		once.Do(func() {
			cancel()
			if err := <-errorsChannel; err != nil {
				t.Errorf("run Device / Entity module: %v", err)
			}
		})
	}
	t.Cleanup(stop)
	return stop
}
