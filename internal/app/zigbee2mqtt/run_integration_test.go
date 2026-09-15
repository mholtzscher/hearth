package zigbee2mqtt //nolint:testpackage // Tests exercise package-private lifecycle supervision.

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	platformdb "github.com/mholtzscher/hearth/internal/platform/db"
	"github.com/mholtzscher/hearth/internal/testbroker"
)

// TestRunConnectsNATSAndMosquitto protects application assembly across an
// embedded native-NATS server and a real Mosquitto MQTT broker and fails on SDK
// metadata, logger wiring, registration, command serving, linked observation,
// sibling cancellation, or graceful release defects.
//
//nolint:cyclop,gocognit,gocyclo // One process-level lifecycle is clearest in one test.
func TestRunConnectsNATSAndMosquitto(t *testing.T) {
	t.Parallel()
	logRecords := make(chan slog.Record, 16)
	logger := slog.New(recordHandler{records: logRecords})

	server := startTestNATSServer(t)
	mqttURL := testbroker.StartMosquitto(t).URL()
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devicessqlite.DeviceStores(repository),
		devicesnats.NewCommandSender(coreConnection, validator),
		catalog,
		devices.Dependencies{},
	)
	startCoreTransports(ctx, t, coreConnection, durable, validator, service, logger)

	setRequests := make(chan []byte, 1)
	upstream := connectMQTTClient(t, mqttURL, "zigbee2mqtt-process-test")
	subscribeMQTT(t, upstream, "zigbee2mqtt/test-light/set", func(_ paho.Client, message paho.Message) {
		payload := append([]byte(nil), message.Payload()...)
		select {
		case setRequests <- payload:
		default:
		}
	})
	publishRetainedSnapshots(t, upstream)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			AdapterID: "zigbee2mqtt",
			NATSURL:   server.ClientURL(),
			MQTT: MQTTConfig{
				URL: mqttURL, BaseTopic: "zigbee2mqtt",
			},
		}, logger)
	}()

	entity := waitForReadyPowerEntity(ctx, t, service, runErrors)
	publishMQTT(t, upstream, "zigbee2mqtt/bridge/event", false, []byte(`not-json`))
	waitForLog(t, logRecords, "ignored malformed Zigbee2MQTT bridge event")

	instance, err := service.GetAdapter(ctx, "zigbee2mqtt")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthHealthy || instance.Health.Runtime == nil ||
		instance.Health.Runtime.SoftwareName != "hearth-adapter-zigbee2mqtt" ||
		instance.Health.Runtime.SoftwareVersion != "0.1.0" {
		t.Fatalf("Adapter instance = %#v", instance)
	}

	type commandResult struct {
		result devices.CommandResult
		err    error
	}
	commandDone := make(chan commandResult, 1)
	go func() {
		result, executeErr := service.ExecuteCommand(ctx, devices.CommandInput{
			EntityID:      entity.Entity.ID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":true}`),
		})
		commandDone <- commandResult{result: result, err: executeErr}
	}()
	select {
	case payload := <-setRequests:
		var command map[string]json.RawMessage
		if err = json.Unmarshal(payload, &command); err != nil {
			t.Fatal(err)
		}
		if string(command["state"]) != `"ON"` || len(command) != 1 {
			t.Fatalf("Zigbee2MQTT set payload = %s", payload)
		}
	case <-ctx.Done():
		t.Fatalf("waiting for Zigbee2MQTT set: %v", ctx.Err())
	}
	for {
		history, historyErr := service.ListEntityCommands(ctx, devices.ListEntityCommandsParams{
			EntityID: entity.Entity.ID,
			Limit:    1,
		})
		if historyErr != nil {
			t.Fatal(historyErr)
		}
		if len(history.Items) == 1 && history.Items[0].Status == devices.CommandStatusAccepted {
			break
		}
		select {
		case completed := <-commandDone:
			t.Fatalf("Command completed before delayed State: result=%#v error=%v", completed.result, completed.err)
		case <-time.After(time.Millisecond):
		}
	}
	publishMQTT(t, upstream, "zigbee2mqtt/test-light", false, []byte(`{"state":"ON"}`))
	completed := <-commandDone
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	if completed.result.Outcome != devices.OutcomeObserved || completed.result.Value == nil ||
		string(*completed.result.Value) != "true" {
		t.Fatalf("Command result = %#v", completed.result)
	}
	var observationCount int
	if err = database.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM observations WHERE entity_id = ?",
		string(entity.Entity.ID),
	).Scan(&observationCount); err != nil {
		t.Fatal(err)
	}
	if observationCount != 2 {
		t.Fatalf("Observations = %d, want startup State plus one linked outcome", observationCount)
	}

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after parent cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after parent cancellation: %v", ctx.Err())
	}
	instance, err = service.GetAdapter(ctx, "zigbee2mqtt")
	if err != nil {
		t.Fatal(err)
	}
	if instance.Health.Status != devices.AdapterHealthUnhealthy || instance.Health.Runtime == nil ||
		instance.Health.Runtime.Status != "offline" {
		t.Fatalf("released Adapter instance = %#v", instance)
	}
}

// TestSuperviseCancelsSiblingAndReturnsTerminalError protects the lifecycle
// failure path and fails if the first component's error is discarded.
func TestSuperviseCancelsSiblingAndReturnsTerminalError(t *testing.T) {
	t.Parallel()
	started := make(chan struct{})
	terminalErr := errors.New("terminal adapter failure")

	err := supervise(
		context.Background(),
		func(context.Context) error {
			<-started
			return terminalErr
		},
		func(ctx context.Context) error {
			close(started)
			<-ctx.Done()
			return ctx.Err()
		},
	)
	if !errors.Is(err, terminalErr) {
		t.Fatalf("supervise error = %v, want terminal failure", err)
	}
}

type recordHandler struct {
	records chan<- slog.Record
}

func (handler recordHandler) Enabled(context.Context, slog.Level) bool { return true }

func (handler recordHandler) Handle(_ context.Context, record slog.Record) error {
	handler.records <- record.Clone()
	return nil
}

func (handler recordHandler) WithAttrs([]slog.Attr) slog.Handler { return handler }

func (handler recordHandler) WithGroup(string) slog.Handler { return handler }

func waitForLog(t *testing.T, records <-chan slog.Record, message string) {
	t.Helper()
	timer := time.NewTimer(time.Second)
	defer timer.Stop()
	for {
		select {
		case record := <-records:
			if record.Message == message {
				return
			}
		case <-timer.C:
			t.Fatalf("waiting for log message %q", message)
		}
	}
}

func startTestNATSServer(t *testing.T) *natsserver.Server {
	t.Helper()
	server, err := natsserver.NewServer(&natsserver.Options{
		ServerName: "hearth-zigbee2mqtt-process-test",
		Host:       "127.0.0.1",
		Port:       -1,
		NoSigs:     true,
		NoLog:      true,
		JetStream:  true,
		StoreDir:   t.TempDir(),
	})
	if err != nil {
		t.Fatal(err)
	}
	go server.Start()
	if !server.ReadyForConnections(10 * time.Second) {
		server.Shutdown()
		t.Fatal("NATS server did not become ready")
	}
	t.Cleanup(func() {
		server.Shutdown()
		server.WaitForShutdown()
	})
	return server
}

func startCoreTransports(
	ctx context.Context,
	t *testing.T,
	connection *natsgo.Conn,
	durable jetstream.Consumer,
	validator *contractsv1.Validator,
	service *devices.Service,
	logger *slog.Logger,
) {
	t.Helper()
	sessions, err := devicesnats.StartSessionServer(connection, validator, service, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sessions.Drain() })
	registrations, err := devicesnats.StartRegistrationServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = registrations.Drain() })
	mappings, err := devicesnats.StartOwnedMappingsServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = mappings.Drain() })
	availability, err := devicesnats.StartEntityAvailabilityServer(connection, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = availability.Drain() })
	observations, err := devicesnats.StartObservationConsumer(ctx, durable, validator, service, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(observations.Stop)
}

func connectMQTTClient(t *testing.T, brokerURL, clientID string) paho.Client {
	t.Helper()
	client := paho.NewClient(paho.NewClientOptions().
		AddBroker(brokerURL).
		SetClientID(clientID).
		SetProtocolVersion(4).
		SetCleanSession(true))
	if token := client.Connect(); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatalf("connect MQTT: %v", token.Error())
	}
	t.Cleanup(func() { client.Disconnect(0) })
	return client
}

func subscribeMQTT(t *testing.T, client paho.Client, topic string, callback paho.MessageHandler) {
	t.Helper()
	if token := client.Subscribe(topic, 1, callback); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Fatalf("subscribe MQTT topic %q: %v", topic, token.Error())
	}
}

func publishMQTT(t *testing.T, client paho.Client, topic string, retained bool, payload []byte) {
	t.Helper()
	if token := client.Publish(topic, 1, retained, payload); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		t.Errorf("publish MQTT topic %q: %v", topic, token.Error())
	}
}

func publishRetainedSnapshots(t *testing.T, client paho.Client) {
	t.Helper()
	messages := []struct {
		topic   string
		payload string
	}{
		{
			topic: "zigbee2mqtt/bridge/info",
			payload: `{"version":"2.13.0","config":{"mqtt":{"version":4},` +
				`"availability":{"enabled":true},"device_options":{"optimistic":false}}}`,
		},
		{topic: "zigbee2mqtt/bridge/state", payload: `{"state":"online"}`},
		{
			topic: "zigbee2mqtt/bridge/devices",
			payload: `[{"ieee_address":"0x00124b0000000001","type":"Router","supported":true,` +
				`"disabled":false,"friendly_name":"test-light","description":"Test light",` +
				`"interview_state":"SUCCESSFUL","endpoints":{},"definition":{"model":"TEST",` +
				`"vendor":"Fixture","description":"Fixture","exposes":[{"type":"light","features":[` +
				`{"type":"binary","name":"state","property":"state","access":7,` +
				`"value_on":"ON","value_off":"OFF"}]}]}}]`,
		},
		{topic: "zigbee2mqtt/test-light/availability", payload: `{"state":"online"}`},
		{topic: "zigbee2mqtt/test-light", payload: `{"state":"OFF"}`},
	}
	for _, message := range messages {
		publishMQTT(t, client, message.topic, true, []byte(message.payload))
	}
}

// TestRunProjectsRelayAndTemperatureDevices protects the proof-flow Core
// contract and fails if the relay plug or temperature sensor does not
// register, if temperature support/State/availability does not project, or if
// a temperature Operation reaches adapter dispatch instead of being rejected
// by Core validation.
//
// The process test reads the same checked-in Zigbee2MQTT 2.13.0 proof
// captures used by the Adapter characterization tests, preventing a reduced
// duplicate inventory from weakening the end-to-end evidence.
func assertProofFlowLinkquality(
	t *testing.T,
	name string,
	linkquality devices.EntityWithState,
	wantState string,
) {
	t.Helper()
	if linkquality.Entity.TypeID != "hearth.numericsensor/v1" {
		t.Fatalf("%s linkquality type = %s", name, linkquality.Entity.TypeID)
	}
	if string(linkquality.Entity.Support) != `{"state":{"maximum":255,"minimum":0,"unit":"lqi"},"operations":{}}` {
		t.Fatalf("%s linkquality support = %s", name, linkquality.Entity.Support)
	}
	if string(linkquality.State.Value) != wantState {
		t.Fatalf("%s linkquality State = %s, want %s", name, linkquality.State.Value, wantState)
	}
}

//nolint:gocognit // One process-level proof flow keeps registration, projection, and rejection causally connected.
func TestRunProjectsRelayAndTemperatureDevices(t *testing.T) {
	t.Parallel()
	logger := slog.New(slog.DiscardHandler)

	server := startTestNATSServer(t)
	mqttURL := testbroker.StartMosquitto(t).URL()
	coreConnection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(coreConnection.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	database, err := platformdb.Open(ctx, filepath.Join(t.TempDir(), "hearth.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err = platformdb.Migrate(ctx, database); err != nil {
		t.Fatal(err)
	}
	catalog, err := devices.NewBuiltinTypeCatalog()
	if err != nil {
		t.Fatal(err)
	}
	repository := devicessqlite.NewDeviceRepository(database, catalog)
	validator, err := contractsv1.Compile()
	if err != nil {
		t.Fatal(err)
	}
	js, err := jetstream.New(coreConnection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	service := devices.NewService(
		devicessqlite.DeviceStores(repository),
		devicesnats.NewCommandSender(coreConnection, validator),
		catalog,
		devices.Dependencies{},
	)
	startCoreTransports(ctx, t, coreConnection, durable, validator, service, logger)

	upstream := connectMQTTClient(t, mqttURL, "zigbee2mqtt-proof-flow-test")
	temperatureSets := make(chan []byte, 4)
	subscribeMQTT(t, upstream, "zigbee2mqtt/fixture-temperature/set", func(_ paho.Client, message paho.Message) {
		payload := append([]byte(nil), message.Payload()...)
		select {
		case temperatureSets <- payload:
		default:
		}
	})
	publishProofFlowSnapshots(t, upstream)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- Run(runContext, Config{
			AdapterID: "zigbee2mqtt",
			NATSURL:   server.ClientURL(),
			MQTT: MQTTConfig{
				URL: mqttURL, BaseTopic: "zigbee2mqtt",
			},
		}, logger)
	}()

	proof := waitForProofFlowEntities(ctx, t, service, runErrors)
	if string(proof.temperature.Entity.Support) != `{"state":{"unit":"mCel"},"operations":{}}` {
		t.Fatalf("temperature support = %s", proof.temperature.Entity.Support)
	}
	assertProofFlowLinkquality(t, "relay", proof.relayLinkquality, "120")
	assertProofFlowLinkquality(t, "temperature", proof.temperatureLinkquality, "105")
	for _, entity := range []devices.EntityWithState{proof.humidity, proof.battery} {
		if string(entity.Entity.Support) != `{"state":{"maximum":100,"minimum":0,"unit":"%"},"operations":{}}` {
			t.Fatalf("percentage sensor support = %s", entity.Entity.Support)
		}
		if _, commandErr := service.ExecuteCommand(ctx, devices.CommandInput{
			EntityID:      entity.Entity.ID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"value":50}`),
		}); !errors.Is(commandErr, devices.ErrInvalidCommand) {
			t.Fatalf("percentage sensor command error = %v, want %v", commandErr, devices.ErrInvalidCommand)
		}
	}
	if _, err = service.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      proof.relayLinkquality.Entity.ID,
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":100}`),
	}); !errors.Is(err, devices.ErrInvalidCommand) {
		t.Fatalf("linkquality command error = %v, want %v", err, devices.ErrInvalidCommand)
	}

	devicesPage, err := service.ListDevices(ctx, devices.ListDevicesParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesPage.Items) != 2 {
		t.Fatalf("devices = %#v", devicesPage.Items)
	}
	kinds := make(map[devices.DeviceKind]int)
	for _, item := range devicesPage.Items {
		aggregate, getErr := service.GetDevice(ctx, devices.GetDeviceParams{ID: item.ID, EntityLimit: 10})
		if getErr != nil {
			t.Fatal(getErr)
		}
		kinds[aggregate.Device.Kind]++
	}
	if kinds[devices.DeviceKindRelay] != 1 || kinds[devices.DeviceKindSensor] != 1 {
		t.Fatalf("device kinds = %#v", kinds)
	}

	if _, err = service.ExecuteCommand(ctx, devices.CommandInput{
		EntityID:      proof.temperature.Entity.ID,
		OperationName: devices.OperationNameSet,
		Parameters:    devices.CommandParameters(`{"value":22600}`),
	}); !errors.Is(err, devices.ErrInvalidCommand) {
		t.Fatalf("temperature command error = %v, want %v", err, devices.ErrInvalidCommand)
	}
	history, err := service.ListEntityCommands(ctx, devices.ListEntityCommandsParams{
		EntityID: proof.temperature.Entity.ID,
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Items) != 0 {
		t.Fatalf("temperature commands = %#v, want none persisted", history.Items)
	}
	select {
	case payload := <-temperatureSets:
		t.Fatalf("temperature set dispatched before validation: %s", payload)
	case <-time.After(200 * time.Millisecond):
	}

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after parent cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after parent cancellation: %v", ctx.Err())
	}
}

type proofFlowEntities struct {
	power                  devices.EntityWithState
	temperature            devices.EntityWithState
	humidity               devices.EntityWithState
	battery                devices.EntityWithState
	relayLinkquality       devices.EntityWithState
	temperatureLinkquality devices.EntityWithState
}

func waitForProofFlowEntities(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	runErrors <-chan error,
) proofFlowEntities {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		var proof proofFlowEntities
		var foundPower, foundTemperature, foundRelayLinkquality, foundTemperatureLinkquality bool
		var foundHumidity, foundBattery bool
		for _, entity := range page.Items {
			switch {
			case entity.Entity.TypeID == "hearth.power/v1" && entity.State != nil &&
				string(entity.State.Value) == "true" &&
				entity.Availability.Status == devices.EntityAvailabilityAvailable:
				proof.power = entity
				foundPower = true
			case entity.Entity.TypeID == "hearth.temperature/v1" && entity.State != nil &&
				string(entity.State.Value) == "22600" &&
				entity.Availability.Status == devices.EntityAvailabilityAvailable:
				proof.temperature = entity
				foundTemperature = true
			case entity.Entity.TypeID == "hearth.numericsensor/v1" && entity.State != nil &&
				entity.Availability.Status == devices.EntityAvailabilityAvailable:
				switch string(entity.State.Value) {
				case "48.2":
					proof.humidity = entity
					foundHumidity = entity.Entity.Name == "Humidity"
				case "100":
					proof.battery = entity
					foundBattery = entity.Entity.Name == "Battery"
				case "120":
					proof.relayLinkquality = entity
					foundRelayLinkquality = true
				case "105":
					proof.temperatureLinkquality = entity
					foundTemperatureLinkquality = true
				}
			}
		}
		if len(page.Items) == 6 && foundPower && foundTemperature && foundHumidity && foundBattery &&
			foundRelayLinkquality && foundTemperatureLinkquality {
			return proof
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Run exited before proof-flow Entities were ready: %v", runErr)
		case <-ctx.Done():
			t.Fatalf("waiting for relay and temperature Entities: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func publishProofFlowSnapshots(t *testing.T, client paho.Client) {
	t.Helper()
	var inventory []json.RawMessage
	for _, name := range []string{"bridge-devices-relay-plug.json", "bridge-devices-temperature.json"} {
		var devices []json.RawMessage
		if err := json.Unmarshal(readAdapterFixture(t, name), &devices); err != nil {
			t.Fatal(err)
		}
		inventory = append(inventory, devices...)
	}
	inventoryPayload, err := json.Marshal(inventory)
	if err != nil {
		t.Fatal(err)
	}
	messages := []struct {
		topic   string
		payload []byte
	}{
		{
			topic: "zigbee2mqtt/bridge/info",
			payload: []byte(`{"version":"2.13.0","config":{"mqtt":{"version":4},` +
				`"availability":{"enabled":true},"device_options":{"optimistic":false}}}`),
		},
		{topic: "zigbee2mqtt/bridge/state", payload: []byte(`{"state":"online"}`)},
		{topic: "zigbee2mqtt/bridge/devices", payload: inventoryPayload},
		{topic: "zigbee2mqtt/fixture-plug/availability", payload: []byte(`{"state":"online"}`)},
		{topic: "zigbee2mqtt/fixture-temperature/availability", payload: []byte(`{"state":"online"}`)},
		{topic: "zigbee2mqtt/fixture-plug", payload: readAdapterFixture(t, "state-relay-plug.json")},
		{topic: "zigbee2mqtt/fixture-temperature", payload: readAdapterFixture(t, "state-temperature.json")},
	}
	for _, message := range messages {
		publishMQTT(t, client, message.topic, true, message.payload)
	}
}

func readAdapterFixture(t *testing.T, name string) []byte {
	t.Helper()
	payload, err := os.ReadFile(filepath.Join("..", "..", "adapters", "zigbee2mqtt", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func waitForReadyPowerEntity(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	runErrors <-chan error,
) devices.EntityWithState {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Items) == 1 {
			entity := page.Items[0]
			if entity.Entity.TypeID == "hearth.power/v1" && entity.State != nil &&
				string(entity.State.Value) == "false" &&
				entity.Availability.Status == devices.EntityAvailabilityAvailable {
				return entity
			}
		}
		select {
		case runErr := <-runErrors:
			t.Fatalf("Run exited before the power Entity was ready: %v", runErr)
		case <-ctx.Done():
			t.Fatalf("waiting for registered, observable, available power Entity: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
