package simulator

import (
	"path/filepath"
	"testing"
)

func TestLoadExampleConfig(t *testing.T) {
	value, err := LoadConfig(filepath.Join("..", "..", "..", "configs", "simulator.example.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if value.Scenario != "happy" {
		t.Fatalf("scenario = %q", value.Scenario)
	}
}

func TestConfigRejectsUnknownScenario(t *testing.T) {
	config := Config{
		AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222",
		BindingKey: "simulated-light", Scenario: "unknown",
	}
	if err := config.Validate(); err == nil {
		t.Fatal("unknown scenario was accepted")
	}
}
