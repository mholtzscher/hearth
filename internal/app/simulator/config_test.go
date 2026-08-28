package simulator_test

import (
	"path/filepath"
	"testing"

	appsimulator "github.com/mholtzscher/hearth/internal/app/simulator"
)

func TestLoadExampleConfig(t *testing.T) {
	t.Parallel()
	value, err := appsimulator.LoadConfig(filepath.Join("..", "..", "..", "configs", "simulator.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.Scenario != "happy" {
		t.Fatalf("scenario = %q", value.Scenario)
	}
}

func TestConfigRejectsUnknownScenario(t *testing.T) {
	t.Parallel()
	config := appsimulator.Config{
		AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222",
		BindingKey: "simulated-light", Scenario: "unknown",
	}
	if err := config.Validate(); err == nil {
		t.Fatal("unknown scenario was accepted")
	}
}
