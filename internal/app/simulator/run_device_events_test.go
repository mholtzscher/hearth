package simulator_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsserver "github.com/nats-io/nats-server/v2/server"
	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/modules/devices"

	simulatoradapter "github.com/mholtzscher/hearth/internal/adapters/simulator"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
)

// This test protects the device-events scenario assembly and fails if the
// scenario drops the power Entity, omits the generated event-source Entity, or
// reports the event source with State instead of Events support.
//
//nolint:gocognit // One assembly sequence keeps registration and Entity reads causal.
func TestRunDeviceEventsRegistersEventSourceBesidePower(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger, recorder := lifecycleLogger(slog.LevelInfo)

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

	service := startSimulatorTestCore(ctx, t, coreConnection, logger)

	runContext, stopRun := context.WithCancel(ctx)
	runErrors := make(chan error, 1)
	go func() {
		runErrors <- simulatorapp.Run(runContext, simulatorapp.Config{
			AdapterID:  "simulator",
			NATSURL:    server.ClientURL(),
			BindingKey: "simulated-light",
			Scenario:   simulatoradapter.ScenarioDeviceEvents,
		}, logger)
	}()

	initialized := waitForLifecycleEvent(t, recorder, "simulator.initialized", 10*time.Second)
	if scenario, ok := lifecycleAttr(initialized, "scenario"); !ok ||
		scenario.String() != simulatoradapter.ScenarioDeviceEvents {
		t.Fatalf("simulator.initialized = %#v", initialized)
	}
	if entityID, ok := lifecycleAttr(initialized, "entity_id"); !ok || entityID.String() == "" {
		t.Fatalf("simulator.initialized omitted entity_id: %#v", initialized)
	}

	page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 50})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("registered Entities = %#v", page.Items)
	}
	byType := make(map[devices.EntityTypeID]devices.EntityWithState, len(page.Items))
	for _, item := range page.Items {
		byType[item.Entity.TypeID] = item
	}
	power, hasPower := byType[devices.EntityTypePowerV1]
	events, hasEvents := byType[devices.EntityTypeEnumeventV1]
	if !hasPower || !hasEvents {
		t.Fatalf("registered Entity types = %#v", byType)
	}
	if events.State != nil {
		t.Fatalf("event source Entity carries State: %#v", events.State)
	}
	if events.Entity.Name != "Events" || !events.Entity.Enabled ||
		events.Availability.Status != devices.EntityAvailabilityAvailable {
		t.Fatalf("event source Entity = %#v", events)
	}
	support := string(events.Entity.Support)
	for _, name := range []string{
		simulatoradapter.DeviceEventSinglePress, simulatoradapter.DeviceEventDoublePress,
	} {
		if !strings.Contains(support, name) {
			t.Fatalf("event source support %s omitted %q", support, name)
		}
	}
	if events.Entity.DeviceID != power.Entity.DeviceID {
		t.Fatalf("event source and power Entity Device IDs differ: %#v", page.Items)
	}

	stopRun()
	select {
	case runErr := <-runErrors:
		if runErr != nil {
			t.Fatalf("Run returned after cancellation: %v", runErr)
		}
	case <-ctx.Done():
		t.Fatalf("Run did not stop after cancellation: %v", ctx.Err())
	}
}
