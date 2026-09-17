package simulator_test

import (
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"

	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
)

// TestRunEntityEventsRegistersEventSourceBesidePower protects the scripted
// entity-events Device assembly and fails if the Device drops the power Entity,
// omits the declared event-source Entity, or reports the event source with
// State instead of Events support.
func TestRunEntityEventsRegistersEventSourceBesidePower(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	logger, recorder := lifecycleLogger(slog.LevelInfo)

	server := natstest.StartServer(t)
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
			AdapterID: "simulator",
			NATSURL:   server.ClientURL(),
			Devices:   []scripted.DeviceSpec{entityEventsDevice()},
		}, logger)
	}()

	initialized := waitForLifecycleEvent(t, recorder, "simulator.initialized", 10*time.Second)
	if mode, ok := lifecycleAttr(initialized, "mode"); !ok || mode.String() != "scripted" {
		t.Fatalf("simulator.initialized = %#v", initialized)
	}
	if entities, ok := lifecycleAttr(initialized, "entities"); !ok || entities.Int64() != 2 {
		t.Fatalf("simulator.initialized entities = %#v, want 2", initialized)
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
	for _, name := range []string{"single_press", "double_press"} {
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

// entityEventsDevice is the scripted equivalent of the removed entity-events
// scenario, as one Device registering the power State Entity beside an event
// source Entity advertising the synthetic press names.
func entityEventsDevice() scripted.DeviceSpec {
	return scripted.DeviceSpec{
		BindingKey: "simulated-light",
		Name:       "Simulated light",
		Kind:       "light",
		Entities: []scripted.EntitySpec{
			{
				Key:  "power",
				Name: "Power",
				Type: "hearth.power/v1",
				Support: map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{"set": map[string]any{}},
				},
				Initial: true,
			},
			{
				Key:  "events",
				Name: "Events",
				Type: "hearth.enumevent/v1",
				Support: map[string]any{
					"state":      map[string]any{},
					"operations": map[string]any{},
					"events":     map[string]any{"names": []any{"single_press", "double_press"}},
				},
			},
		},
	}
}
