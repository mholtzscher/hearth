package agenteval //nolint:testpackage // Checks the package's household fixture.

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestHouseholdEventsAreQuiet guards scenario outcomes: a periodic event would
// run a seeded Automation mid-trial and send Commands the Agent never asked
// for, failing no_commands in both arms.
func TestHouseholdEventsAreQuiet(t *testing.T) {
	t.Parallel()
	raw, err := os.ReadFile(filepath.Join("..", "..", "testdata", "agenteval", "household.simulator.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	var household struct {
		Adapters []struct {
			Devices []struct {
				BindingKey string `yaml:"binding_key"`
				Entities   []struct {
					Key     string    `yaml:"key"`
					Type    string    `yaml:"type"`
					Outputs yaml.Node `yaml:"outputs"`
				} `yaml:"entities"`
			} `yaml:"devices"`
		} `yaml:"adapters"`
	}
	if err = yaml.Unmarshal(raw, &household); err != nil {
		t.Fatal(err)
	}
	events := 0
	for _, adapter := range household.Adapters {
		for _, device := range adapter.Devices {
			for _, entity := range device.Entities {
				if entity.Type != "hearth.enumevent/v1" {
					continue
				}
				events++
				if !entity.Outputs.IsZero() {
					t.Errorf("%s/%s emits periodic events", device.BindingKey, entity.Key)
				}
			}
		}
	}
	if events == 0 {
		t.Fatal("found no event Entities; the fixture's shape changed")
	}
}
