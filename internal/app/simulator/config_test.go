package simulator_test

import (
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
	appsimulator "github.com/mholtzscher/hearth/internal/app/simulator"
)

// TestLoadExampleConfig protects the checked-in first-light example: it must
// load and validate as one scripted power Device, so the config users copy
// still starts a working simulator. It fails if the example drifts from the
// scripted schema, for example a Device value the Entity type rejects.
func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := appsimulator.LoadConfig(filepath.Join("..", "..", "..", "configs", "simulator.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if valuesErr := scripted.ValidateValues(value.Devices); valuesErr != nil {
		t.Fatalf("example values invalid: %v", valuesErr)
	}
	if len(value.Devices) != 1 {
		t.Fatalf("example devices = %d, want 1", len(value.Devices))
	}
	device := value.Devices[0]
	if device.BindingKey != "simulated-light" {
		t.Fatalf("example binding_key = %q, want simulated-light", device.BindingKey)
	}
	if len(device.Entities) != 1 || device.Entities[0].Type != "hearth.power/v1" {
		t.Fatalf("example entities = %#v, want one hearth.power/v1 Entity", device.Entities)
	}
}
