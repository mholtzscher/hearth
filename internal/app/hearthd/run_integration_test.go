package hearthd //nolint:testpackage // Tests exercise package-private assembly and lifecycle behavior.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	contractpowerv1 "github.com/mholtzscher/hearth/entitytypes/powerv1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/sdk/adapter"
	sdkpowerv1 "github.com/mholtzscher/hearth/sdk/adapter/powerv1"
)

//nolint:gocognit,gocyclo,cyclop // The durable transport lifecycle is clearer as one integration test.
func TestCoreNATSTransportRegistersAndProjectsDurableObservation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
	databasePath := filepath.Join(t.TempDir(), "hearth.db")
	database, err := platformdb.Open(ctx, databasePath)
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
	repository := devices.NewSQLiteRepository(database, catalog)
	service := devices.NewService(devices.SQLiteStores(repository), nil, catalog, devices.Dependencies{})

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
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	deviceEventConsumer, err := devicesnats.ProvisionDeviceEventResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	sessions, err := devicesnats.StartSessionServer(coreConnection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := devicesnats.StartRegistrationServer(coreConnection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	observations, err := devicesnats.StartObservationConsumer(ctx, durable, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)
	deviceEvents, err := devicesnats.StartDeviceEventConsumer(ctx, deviceEventConsumer, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(deviceEvents.Stop)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
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
	var runtimeID devices.RuntimeID
	if scanErr := database.QueryRowContext(
		ctx,
		"SELECT runtime_id FROM observations WHERE observation_id = ?",
		observationID,
	).Scan(&runtimeID); scanErr != nil {
		t.Fatal(scanErr)
	}
	readiness := NewRuntimeReadiness(database, coreConnection, js, observations, deviceEvents)
	if readinessErr := readiness.Check(ctx); readinessErr != nil {
		t.Fatal(readinessErr)
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

	// Release while the session server's repository is still open.
	if closeErr := session.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	observations.Stop()
	select {
	case <-observations.Closed():
	case <-time.After(time.Second):
		t.Fatal("observation consumer did not stop")
	}
	if closeErr := database.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	database, err = platformdb.Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	recoveredService := devices.NewService(
		devices.SQLiteStores(devices.NewSQLiteRepository(database, catalog)),
		nil,
		catalog,
		devices.Dependencies{},
	)
	recovered, err := recoveredService.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if recovered.State == nil || recovered.State.ObservationID != projected.ObservationID ||
		recovered.State.ReceiveOrder != projected.ReceiveOrder || string(recovered.State.Value) != "true" {
		t.Fatalf("recovered state = %#v, want %#v", recovered.State, projected)
	}
	result, err := recoveredService.ProjectObservation(ctx, "simulator", runtimeID, devices.Observation{
		ID: devices.ObservationID(observationID), EntityID: entityID, Value: devices.Value(`true`),
		AdapterReceivedAt: adapterReceivedAt,
	}, projected.ObservedAt)
	if err != nil {
		t.Fatal(err)
	}
	if result.Disposition != devices.DispositionDuplicate || result.State != nil {
		t.Fatalf("recovered redelivery = %#v", result)
	}
	var observationRowCount int
	if queryErr := database.QueryRowContext(
		ctx,
		"SELECT count(*) FROM observations",
	).Scan(&observationRowCount); queryErr != nil {
		t.Fatal(queryErr)
	}
	if observationRowCount != 1 {
		t.Fatalf("observation count after recovered redelivery = %d", observationRowCount)
	}
}

//nolint:gocognit,gocyclo,cyclop // The command lifecycle is clearer as one integration test.
func TestCoreCommandRoundTripRequiresLinkedSimulatorObservation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger := slog.New(slog.DiscardHandler)
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
	repository := devices.NewSQLiteRepository(database, catalog)

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
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devices.SQLiteStores(repository),
		devicesnats.NewCommandSender(coreConnection, validator),
		catalog,
		devices.Dependencies{},
	)
	sessions, err := devicesnats.StartSessionServer(coreConnection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := devicesnats.StartRegistrationServer(coreConnection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	observations, err := devicesnats.StartObservationConsumer(ctx, durable, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)

	session, err := adapter.Connect(ctx, adapter.Config{
		AdapterID: "simulator", SoftwareName: "hearth-simulator",
		SoftwareVersion: "0.1.0", NATSURL: server.ClientURL(),
	})
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
			evidence, acceptErr := responder.Accept()
			if acceptErr != nil {
				return acceptErr
			}
			observation, observationErr := sdkpowerv1.NewObservation(sdkpowerv1.ObservationInput{
				EntityID: string(entityID), Support: support, State: contractpowerv1.State(command.Parameters.Value),
				AdapterReceivedAt: time.Now().UTC(),
			})
			if observationErr != nil {
				return observationErr
			}
			_, publishErr := evidence.PublishObservation(commandContext, observation)
			return publishErr
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

	result, err := service.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      entityID,
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":true}`),
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Outcome != devices.OutcomeObserved || result.ObservationID == nil || result.Value == nil ||
		string(*result.Value) != "true" {
		t.Fatalf("command result = %#v", result)
	}
	stored, err := repository.GetCommand(ctx, result.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Status != devices.CommandStatusSatisfied || stored.AcceptedAt == nil ||
		stored.OutcomeObservationID == nil || *stored.OutcomeObservationID != *result.ObservationID {
		t.Fatalf("stored command = %#v", stored)
	}
	view, err := service.GetEntity(ctx, entityID)
	if err != nil {
		t.Fatal(err)
	}
	if view.State == nil || view.State.ObservationID != *result.ObservationID || string(view.State.Value) != "true" {
		t.Fatalf("entity view = %#v", view)
	}
	stopServing()
	select {
	case serveErr := <-serveErrors:
		if !errors.Is(serveErr, context.Canceled) {
			t.Fatalf("serve commands: %v", serveErr)
		}
	case <-time.After(time.Second):
		t.Fatal("simulator command server did not stop")
	}
}
