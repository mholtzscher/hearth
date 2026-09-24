package simulator_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	appsimulator "github.com/mholtzscher/hearth/internal/app/simulator"
)

func scriptedConfig() appsimulator.Config {
	return appsimulator.Config{
		AdapterID: "simulator",
		NATSURL:   "nats://127.0.0.1:4222",
		Devices: []scripted.DeviceSpec{{
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
			},
		}},
	}
}

func TestScriptedConfigValidates(t *testing.T) {
	t.Parallel()
	if err := scriptedConfig().Validate(); err != nil {
		t.Fatalf("valid scripted config rejected: %v", err)
	}
}

func TestConfigRequiresDevices(t *testing.T) {
	t.Parallel()
	config := appsimulator.Config{AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222"}
	if err := config.Validate(); err == nil {
		t.Fatal("config without devices accepted, want an error")
	}
}

// TestConfigValidateRejectsInvalidScriptedValues protects fail-fast config
// load: a Device whose support, initial, or outputs value does not match its
// Entity type schema must be rejected by Validate, which runs before the
// simulator connects to NATS. It fails if Validate only checks structure and
// defers value schemas to scripted.New after the connection is established.
func TestConfigValidateRejectsInvalidScriptedValues(t *testing.T) {
	t.Parallel()
	cases := map[string]func(*appsimulator.Config){
		"invalid initial value": func(config *appsimulator.Config) {
			config.Devices[0].Entities[0].Initial = "on"
		},
		"invalid outputs value": func(config *appsimulator.Config) {
			config.Devices[0].Entities[0].Outputs = &scripted.OutputsSpec{
				Interval: scripted.Duration(5 * time.Second),
				Values:   []any{true, "on"},
			}
		},
		"invalid support": func(config *appsimulator.Config) {
			config.Devices[0].Entities[0].Support = map[string]any{
				"state":      map[string]any{},
				"operations": map[string]any{"set": "yes"},
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			config := scriptedConfig()
			mutate(&config)
			err := config.Validate()
			if err == nil {
				t.Fatalf("config with %s accepted by Validate, want an error before startup", name)
			}
			// The index points the reader at the offending Device in the file.
			if !strings.Contains(err.Error(), "devices[0]") {
				t.Fatalf("Validate error %q does not name devices[0]", err)
			}
		})
	}
}

func TestConfigValidatesControlAddr(t *testing.T) {
	t.Parallel()
	for _, addr := range []string{"127.0.0.1:8181", "[::1]:8181", "localhost:8181"} {
		config := scriptedConfig()
		config.ControlAddr = addr
		if err := config.Validate(); err != nil {
			t.Fatalf("loopback control_addr %q rejected: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:8181", "192.168.1.10:8181", "127.0.0.1", "127.0.0.1:0", "example.com:8181"} {
		config := scriptedConfig()
		config.ControlAddr = addr
		if err := config.Validate(); err == nil {
			t.Fatalf("control_addr %q accepted, want loopback-only", addr)
		}
	}
}

func loadValidationSimulatorExample(t *testing.T) appsimulator.Config {
	t.Helper()
	path := filepath.Join("..", "..", "..", "configs", "simulator.scripted.example.yaml")
	value, err := appsimulator.LoadConfigWithOverrides(path, appsimulator.ConfigOverrides{
		NATSURL: "nats://127.0.0.1:4222", ControlAddr: "127.0.0.1:8181",
	})
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func TestLoadScriptedExampleFile(t *testing.T) {
	t.Parallel()
	value := loadValidationSimulatorExample(t)
	seen := make(map[string]bool)
	for _, device := range value.Adapters[0].Devices {
		for _, entity := range device.Entities {
			seen[entity.Type] = true
		}
	}
	for _, typeID := range scripted.KnownTypes() {
		if !seen[typeID] {
			t.Errorf("scripted example missing Entity type %s", typeID)
		}
	}
}

func TestSimulatorTransportOverridesBeforeValidation(t *testing.T) {
	t.Parallel()
	path := filepath.Join("..", "..", "..", "configs", "simulator.scripted.example.yaml")
	config, err := appsimulator.LoadConfigWithOverrides(path, appsimulator.ConfigOverrides{
		NATSURL: "nats://127.0.0.1:4282", ControlAddr: "127.0.0.1:8241",
	})
	if err != nil {
		t.Fatal(err)
	}
	if config.NATSURL != "nats://127.0.0.1:4282" || config.ControlAddr != "127.0.0.1:8241" ||
		len(config.Adapters) != 5 {
		t.Fatalf("simulator scenario changed while overriding transport: %+v", config)
	}
	if _, invalidErr := appsimulator.LoadConfigWithOverrides(path, appsimulator.ConfigOverrides{
		ControlAddr: "0.0.0.0:8241",
	}); invalidErr == nil {
		t.Fatal("override bypassed loopback-only control validation")
	}
}

func TestScriptedExampleDevicesStayHealthy(t *testing.T) {
	t.Parallel()
	value := loadValidationSimulatorExample(t)
	if len(value.Adapters) != 5 || value.Adapters[0].AdapterID != "sim-healthy" {
		t.Fatalf("expected five isolated Adapters starting with sim-healthy: %+v", value.Adapters)
	}
	healthyKeys := map[string]bool{
		"simulated-light": true, "simulated-button": true,
		"simulated-climate": true, "simulated-plug": true,
	}
	for _, device := range value.Adapters[0].Devices {
		if !healthyKeys[device.BindingKey] {
			t.Errorf("unexpected Device %s in healthy simulator", device.BindingKey)
			continue
		}
		delete(healthyKeys, device.BindingKey)
		if device.Health != "" && device.Health != "healthy" {
			t.Errorf("scripted device %s is not healthy: %s", device.BindingKey, device.Health)
		}
		if device.OmitAvailabilityWhenUnhealthy {
			t.Errorf("scripted device %s omits availability", device.BindingKey)
		}
		for _, entity := range device.Entities {
			checkHealthySimulatorEntity(t, device.BindingKey, entity)
		}
	}
	for key := range healthyKeys {
		t.Errorf("scripted example missing healthy Device %s", key)
	}
}

func checkHealthySimulatorEntity(t *testing.T, bindingKey string, entity scripted.EntitySpec) {
	t.Helper()
	if entity.Available != nil && !*entity.Available {
		t.Errorf("scripted entity %s/%s is unavailable", bindingKey, entity.Key)
	}
	for operation, command := range entity.Commands {
		if command.Behavior != "" && command.Behavior != "accept-and-publish" {
			t.Errorf("scripted command %s/%s/%s does not publish an outcome",
				bindingKey, entity.Key, operation)
		}
	}
}

func TestFaultExampleDevices(t *testing.T) {
	t.Parallel()
	value := loadValidationSimulatorExample(t)
	seen := make(map[string]scripted.DeviceSpec)
	for _, entry := range value.Adapters[1:] {
		if len(entry.Devices) != 1 {
			t.Errorf("fault Adapter %s has %d Devices, want 1", entry.AdapterID, len(entry.Devices))
			continue
		}
		seen[entry.AdapterID] = entry.Devices[0]
	}
	for _, key := range []string{"sim-unhealthy", "sim-rejecting", "sim-timeout", "sim-unavailable"} {
		if _, ok := seen[key]; !ok {
			t.Errorf("scripted example missing fault Adapter %s", key)
		}
	}
	if device, ok := seen["sim-unhealthy"]; ok &&
		(device.Health != "unhealthy:hearth.external_system_unavailable" || !device.OmitAvailabilityWhenUnhealthy) {
		t.Error("named unhealthy Device does not report unhealthy with omitted availability")
	}
	if device, ok := seen["sim-rejecting"]; ok && device.Entities[0].Commands["set"].Behavior != "reject" {
		t.Error("named rejecting Device does not reject its set Command")
	}
	if device, ok := seen["sim-timeout"]; ok &&
		device.Entities[0].Commands["set"].Behavior != "accept-no-publish" {
		t.Error("named timeout Device publishes a Command outcome")
	}
	if device, ok := seen["sim-unavailable"]; ok &&
		(device.Entities[0].Available == nil || *device.Entities[0].Available ||
			!device.Entities[0].Commands["set"].MarkAvailable) {
		t.Error("named unavailable Device does not start unavailable and repair on set")
	}
}

// TestLoadConfigAcceptsFaultPrimitiveKeys protects the strict config loader's
// knowledge of the fault-primitive YAML keys: the loader rejects unknown
// fields, so a missing yaml tag would fail to load a valid file. It also shows
// the negative source offset and the omit/unavailable/repair combination pass
// config validation end to end.
func TestLoadConfigAcceptsFaultPrimitiveKeys(t *testing.T) {
	t.Parallel()
	yaml := `adapter_id: simulator
nats_url: nats://127.0.0.1:4222
devices:
  - binding_key: simulated-broken
    name: Simulated broken
    kind: sensor
    health: "unhealthy:hearth.external_system_unavailable"
    omit_availability_when_unhealthy: true
    entities:
      - key: temperature
        name: Temperature
        type: hearth.temperature/v1
        support: {state: {unit: mCel}, operations: {}}
        initial: 20000
        source_time_offset: -24h
        received_time_offset: +2m
  - binding_key: simulated-light
    name: Simulated light
    kind: light
    entities:
      - key: power
        name: Power
        type: hearth.power/v1
        support: {state: {}, operations: {set: {}}}
        initial: true
        available: false
        availability_reason: adapter.simulated-light.entity_unavailable
        commands: {set: {behavior: accept-and-publish, mark_available: true}}
`
	path := filepath.Join(t.TempDir(), "simulator.yaml")
	if writeErr := os.WriteFile(path, []byte(yaml), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	value, err := appsimulator.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if !value.Devices[0].OmitAvailabilityWhenUnhealthy {
		t.Fatal("omit_availability_when_unhealthy did not load")
	}
	sensor := value.Devices[0].Entities[0]
	if got := time.Duration(sensor.SourceTimeOffset); got != -24*time.Hour {
		t.Fatalf("source_time_offset = %s, want -24h", got)
	}
	if got := time.Duration(sensor.ReceivedTimeOffset); got != 2*time.Minute {
		t.Fatalf("received_time_offset = %s, want 2m", got)
	}
	behavior := value.Devices[1].Entities[0].Commands["set"]
	if !behavior.MarkAvailable {
		t.Fatal("mark_available did not load")
	}
}

func TestLoadScriptedExampleConfig(t *testing.T) {
	t.Parallel()
	yaml := `adapter_id: simulator
nats_url: nats://127.0.0.1:4222
control_addr: 127.0.0.1:8181
devices:
  - binding_key: simulated-light
    name: Simulated light
    kind: light
    entities:
      - key: power
        name: Power
        type: hearth.power/v1
        support: {state: {}, operations: {set: {}}}
        initial: true
        outputs: {interval: 5s, values: [true, false]}
        commands: {set: {behavior: accept-and-publish}}
`
	path := filepath.Join(t.TempDir(), "simulator.yaml")
	if writeErr := os.WriteFile(path, []byte(yaml), 0o600); writeErr != nil {
		t.Fatal(writeErr)
	}
	value, err := appsimulator.LoadConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(value.Devices) != 1 || len(value.Devices[0].Entities) != 1 {
		t.Fatalf("loaded devices = %+v, want one Device with one Entity", value.Devices)
	}
	entity := value.Devices[0].Entities[0]
	if entity.Outputs == nil || len(entity.Outputs.Values) != 2 {
		t.Fatalf("loaded outputs = %+v, want two scripted values", entity.Outputs)
	}
	if _, newErr := scripted.New(nil, value.Devices); newErr == nil {
		t.Fatal("scripted.New with nil Session succeeded, want an error")
	}
}
