package simulator_test

import (
	"os"
	"path/filepath"
	"testing"

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

func TestConfigRejectsScenarioAndDevices(t *testing.T) {
	t.Parallel()
	config := scriptedConfig()
	config.Scenario = "happy"
	if err := config.Validate(); err == nil {
		t.Fatal("scenario+devices accepted, want mutual exclusion")
	}
}

func TestConfigRequiresScenarioOrDevices(t *testing.T) {
	t.Parallel()
	config := appsimulator.Config{AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222"}
	if err := config.Validate(); err == nil {
		t.Fatal("empty mode accepted, want an error")
	}
}

func TestConfigRejectsLegacyBindingKeyWithDevices(t *testing.T) {
	t.Parallel()
	config := scriptedConfig()
	config.BindingKey = "simulated-light"
	if err := config.Validate(); err == nil {
		t.Fatal("legacy binding_key with devices accepted, want an error")
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
