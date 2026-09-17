package zigbee2mqtt //nolint:testpackage // Tests exercise package-private lifecycle supervision.

import (
	"context"
	"encoding/json"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	contractsv1 "github.com/mholtzscher/hearth/contracts/v1"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	devicessqlite "github.com/mholtzscher/hearth/internal/modules/devices/sqlite"
	"github.com/mholtzscher/hearth/internal/platform/db/dbtest"
	"github.com/mholtzscher/hearth/internal/testbroker"
)

// TestRunProjectsColorBulbAndLinksColorCommand protects the end-to-end color
// contract through unchanged envelopes: API discovery exposes native XY, HS,
// and mode Entities beside power, brightness, and temperature; an XY set
// command publishes the exact native payload; and a later complete active
// report links the outcome visible in command history, State reads, and
// State history reads.
//
//nolint:cyclop,gocognit,gocyclo // One process-level color flow keeps discovery, command, and audit causally connected.
func TestRunProjectsColorBulbAndLinksColorCommand(t *testing.T) {
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

	database := dbtest.OpenMigrated(t, filepath.Join(t.TempDir(), "hearth.db"))
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

	setRequests := make(chan []byte, 4)
	getRequests := make(chan []byte, 4)
	upstream := connectMQTTClient(t, mqttURL, "zigbee2mqtt-color-test")
	subscribeMQTT(t, upstream, "zigbee2mqtt/fixture-color-dual/set", func(_ paho.Client, message paho.Message) {
		payload := append([]byte(nil), message.Payload()...)
		select {
		case setRequests <- payload:
		default:
		}
	})
	subscribeMQTT(t, upstream, "zigbee2mqtt/fixture-color-dual/get", func(_ paho.Client, message paho.Message) {
		payload := append([]byte(nil), message.Payload()...)
		select {
		case getRequests <- payload:
		default:
		}
	})
	publishMQTT(t, upstream, "zigbee2mqtt/bridge/info", true, []byte(
		`{"version":"2.13.0","config":{"mqtt":{"version":4},`+
			`"availability":{"enabled":true},"device_options":{"optimistic":false}}}`,
	))
	publishMQTT(t, upstream, "zigbee2mqtt/bridge/state", true, []byte(`{"state":"online"}`))
	publishMQTT(
		t,
		upstream,
		"zigbee2mqtt/bridge/devices",
		true,
		readAdapterFixture(t, "bridge-devices-color-dual.json"),
	)
	publishMQTT(
		t,
		upstream,
		"zigbee2mqtt/fixture-color-dual/availability",
		true,
		[]byte(`{"state":"online"}`),
	)
	publishMQTT(t, upstream, "zigbee2mqtt/fixture-color-dual", true, []byte(
		`{"state":"ON","brightness":127,`+
			`"color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},`+
			`"color_temp":370,"color_mode":"xy"}`,
	))

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

	colorXY := waitForColorBulbEntities(ctx, t, service, runErrors)

	devicesPage, err := service.ListDevices(ctx, devices.ListDevicesParams{Limit: 10})
	if err != nil {
		t.Fatal(err)
	}
	if len(devicesPage.Items) != 1 {
		t.Fatalf("devices = %#v", devicesPage.Items)
	}
	aggregate, err := service.GetDevice(
		ctx,
		devices.GetDeviceParams{ID: devicesPage.Items[0].ID, EntityLimit: 10},
	)
	if err != nil {
		t.Fatal(err)
	}
	if aggregate.Device.Kind != devices.DeviceKindLight {
		t.Fatalf("device kind = %v", aggregate.Device.Kind)
	}
	types := make(map[devices.EntityTypeID]bool, len(aggregate.Entities.Items))
	for _, entity := range aggregate.Entities.Items {
		types[entity.Entity.TypeID] = true
	}
	for _, want := range []devices.EntityTypeID{
		"hearth.power/v1", "hearth.brightness/v1", "hearth.colortemp/v1",
		"hearth.colorxy/v1", "hearth.colorhs/v1", "hearth.colormode/v1",
	} {
		if !types[want] {
			t.Fatalf("discovered Entity types = %v, missing %s", types, want)
		}
	}

	type commandResult struct {
		result devices.CommandResult
		err    error
	}
	commandDone := make(chan commandResult, 1)
	go func() {
		result, executeErr := service.ExecuteCommand(ctx, devices.CommandInput{
			EntityID:      colorXY.Entity.ID,
			OperationName: devices.OperationNameSet,
			Parameters:    devices.CommandParameters(`{"x":4000,"y":2000}`),
		})
		commandDone <- commandResult{result: result, err: executeErr}
	}()
	select {
	case payload := <-setRequests:
		var command map[string]json.RawMessage
		if err = json.Unmarshal(payload, &command); err != nil {
			t.Fatal(err)
		}
		if len(command) != 1 || string(command["color"]) != `{"x":0.4,"y":0.2}` {
			t.Fatalf("Zigbee2MQTT color set payload = %s", payload)
		}
	case <-ctx.Done():
		t.Fatalf("waiting for Zigbee2MQTT color set: %v", ctx.Err())
	}
	for colorGet := false; !colorGet; {
		select {
		case payload := <-getRequests:
			colorGet = string(payload) == `{"color":""}`
		case <-ctx.Done():
			t.Fatalf("waiting for Zigbee2MQTT color get: %v", ctx.Err())
		}
	}
	for {
		history, historyErr := service.ListEntityCommands(ctx, devices.ListEntityCommandsParams{
			EntityID: colorXY.Entity.ID,
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
			t.Fatalf("color Command completed before evidence: result=%#v error=%v", completed.result, completed.err)
		case <-time.After(time.Millisecond):
		}
	}
	publishMQTT(t, upstream, "zigbee2mqtt/fixture-color-dual", false, []byte(
		`{"color":{"x":0.4,"y":0.2},"color_mode":"xy"}`,
	))
	var completed commandResult
	select {
	case completed = <-commandDone:
	case <-ctx.Done():
		t.Fatalf("waiting for color Command outcome: %v", ctx.Err())
	}
	if completed.err != nil {
		t.Fatal(completed.err)
	}
	if completed.result.Outcome != devices.OutcomeObserved || completed.result.Value == nil ||
		string(*completed.result.Value) != `{"active":true,"x":4000,"y":2000}` {
		t.Fatalf("color Command result = %#v", completed.result)
	}

	record, err := service.GetCommand(ctx, completed.result.CommandID)
	if err != nil {
		t.Fatal(err)
	}
	if record.Status != devices.CommandStatusSatisfied {
		t.Fatalf("color Command record = %#v", record)
	}
	commands, err := service.ListEntityCommands(ctx, devices.ListEntityCommandsParams{
		EntityID: colorXY.Entity.ID,
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(commands.Items) != 1 || commands.Items[0].Status != devices.CommandStatusSatisfied {
		t.Fatalf("color Command history = %#v", commands.Items)
	}
	current, err := service.GetEntity(ctx, colorXY.Entity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.State == nil || string(current.State.Value) != `{"active":true,"x":4000,"y":2000}` {
		t.Fatalf("color XY State = %#v", current)
	}
	stateHistory, err := service.ListEntityStateHistory(ctx, devices.ListEntityStateHistoryParams{
		EntityID: colorXY.Entity.ID,
		Limit:    10,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stateHistory.Items) < 2 {
		t.Fatalf("color XY State history = %#v", stateHistory.Items)
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

func waitForColorBulbEntities(
	ctx context.Context,
	t *testing.T,
	service *devices.Service,
	runErrors <-chan error,
) devices.EntityWithState {
	t.Helper()
	want := map[string]string{
		"hearth.power/v1":      "true",
		"hearth.brightness/v1": "50",
		"hearth.colortemp/v1":  `{"active":false,"value":370}`,
		"hearth.colorxy/v1":    `{"active":true,"x":3125,"y":3291}`,
		"hearth.colorhs/v1":    `{"active":false,"hue":120,"saturation":80}`,
		"hearth.colormode/v1":  `"xy"`,
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 20})
		if err != nil {
			t.Fatal(err)
		}
		seen := make(map[string]devices.EntityWithState, len(page.Items))
		for _, entity := range page.Items {
			if entity.State == nil ||
				entity.Availability.Status != devices.EntityAvailabilityAvailable {
				continue
			}
			if want[string(entity.Entity.TypeID)] == string(entity.State.Value) {
				seen[string(entity.Entity.TypeID)] = entity
			}
		}
		if len(seen) == len(want) {
			if string(seen["hearth.colormode/v1"].Entity.Support) != `{"state":{},"operations":{}}` {
				t.Fatalf("mode support = %s", seen["hearth.colormode/v1"].Entity.Support)
			}
			return seen["hearth.colorxy/v1"]
		}
		_ = seen
		select {
		case runErr := <-runErrors:
			t.Fatalf("Run exited before color Entities were ready: %v", runErr)
		case <-ctx.Done():
			t.Fatalf("waiting for color Entities: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}
