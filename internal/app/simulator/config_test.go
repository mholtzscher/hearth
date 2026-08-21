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
