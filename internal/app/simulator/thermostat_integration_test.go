//go:build integration

package simulator_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	natsgo "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
	"github.com/mholtzscher/hearth/internal/modules/devices"
	devicesnats "github.com/mholtzscher/hearth/internal/modules/devices/nats"
	"github.com/mholtzscher/hearth/internal/platform/nats/natstest"
)

// The real Core command and observation paths must record linked thermostat
// evidence, not just a simulator snapshot or an SDK acceptance.
func TestThermostatCommandsReachCoreState(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	logger, recorder := lifecycleLogger(slog.LevelInfo)
	server := natstest.StartServer(t)
	connection, err := natsgo.Connect(server.ClientURL())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(connection.Close)
	service := startSimulatorTestCore(ctx, t, connection, logger)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	durable, err := devicesnats.ProvisionObservationResources(ctx, js)
	if err != nil {
		t.Fatal(err)
	}
	consumer, err := devicesnats.StartObservationConsumer(
		ctx,
		durable,
		mustCompileValidator(t),
		service.Service,
		logger,
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(consumer.Stop)

	config := loadValidationSimulatorExample(t)
	var thermostat scripted.DeviceSpec
	for _, device := range config.Adapters[0].Devices {
		if device.BindingKey == "hallway-thermostat" {
			thermostat = device
		}
	}
	if thermostat.BindingKey == "" {
		t.Fatal("default simulator missing thermostat")
	}
	runCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		done <- simulatorapp.Run(runCtx, simulatorapp.Config{NATSURL: server.ClientURL(),
			Adapters: []simulatorapp.ScriptedAdapterConfig{{AdapterID: "simulator", Devices: []scripted.DeviceSpec{thermostat}}},
		}, logger)
	}()
	defer func() {
		stop()
		select {
		case runErr := <-done:
			if runErr != nil {
				t.Error(runErr)
			}
		case <-time.After(5 * time.Second):
			t.Error("simulator did not stop")
		}
	}()
	waitForLifecycleEvent(t, recorder, "simulator.initialized", 10*time.Second)
	waitForLifecycleEvent(t, recorder, "adapter.commands_listening", 9*time.Second)
	page, err := service.ListEntities(ctx, devices.ListEntitiesParams{Limit: 50})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("entities: %+v, %v", page.Items, err)
	}
	id := page.Items[0].Entity.ID
	waitForFixtureAvailableEntity(ctx, t, service, id)

	assertThermostatCommands(ctx, t, service, id)
}

func assertThermostatCommands(ctx context.Context, t *testing.T, service *testCoreService, id devices.EntityID) {
	t.Helper()
	for _, tc := range []struct {
		operation, params, field string
		value                    any
	}{
		{"set_target", `{"temperature":21000}`, "target_temperature", float64(21000)},
		{"set_range", `{"low":18000,"high":24000}`, "target_temperature_low", float64(18000)},
		{"set_mode", `{"mode":"cool"}`, "mode", "cool"},
	} {
		result, commandErr := service.ExecuteCommand(ctx, devices.CommandInput{
			EntityID:      id,
			OperationName: devices.OperationName(tc.operation),
			Parameters:    devices.CommandParameters(tc.params),
		})
		if commandErr != nil {
			t.Fatalf("%s: %v", tc.operation, commandErr)
		}
		record, getErr := service.GetCommand(ctx, result.CommandID)
		if getErr != nil || record.Status != devices.CommandStatusSatisfied || record.OutcomeObservationID == nil ||
			result.ObservationID == nil || *record.OutcomeObservationID != *result.ObservationID {
			t.Fatalf("%s command evidence: %+v result %+v error %v", tc.operation, record, result, getErr)
		}
		entity, getErr := service.GetEntity(ctx, id)
		if getErr != nil || entity.State == nil || entity.State.ObservationID != *result.ObservationID {
			t.Fatalf("%s Core state: %+v error %v", tc.operation, entity.State, getErr)
		}
		var state map[string]any
		if decodeErr := json.Unmarshal(entity.State.Value, &state); decodeErr != nil || state[tc.field] != tc.value ||
			state["current_temperature"] != float64(18500) ||
			(tc.operation == "set_range" && state["target_temperature_high"] != float64(24000)) {
			t.Fatalf("%s Core value: %s error %v", tc.operation, entity.State.Value, decodeErr)
		}
	}
}
