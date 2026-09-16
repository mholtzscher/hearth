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

func TestLoadScriptedExampleFile(t *testing.T) {
	t.Parallel()
	value, err := appsimulator.LoadConfig(filepath.Join("..", "..", "..", "configs", "simulator.scripted.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if valuesErr := scripted.ValidateValues(value.Devices); valuesErr != nil {
		t.Fatalf("scripted example values invalid: %v", valuesErr)
	}
	if len(value.Devices) != 2 {
		t.Fatalf("scripted example devices = %d, want 2", len(value.Devices))
	}
}

func TestLoadFullExampleFile(t *testing.T) {
	t.Parallel()
	value, err := appsimulator.LoadConfig(filepath.Join("..", "..", "..", "configs", "simulator.full.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if valuesErr := scripted.ValidateValues(value.Devices); valuesErr != nil {
		t.Fatalf("full example values invalid: %v", valuesErr)
	}
	seen := make(map[string]struct{})
	for _, device := range value.Devices {
		for _, entity := range device.Entities {
			seen[entity.Type] = struct{}{}
		}
	}
	if len(seen) != len(scripted.KnownTypes()) {
		t.Fatalf("full example covers %d Entity types, want all %d", len(seen), len(scripted.KnownTypes()))
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
