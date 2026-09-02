package simulator_test

import (
	"testing"

	simulatorapp "github.com/mholtzscher/hearth/internal/app/simulator"
)

func TestConfigRejectsRawObservationFaultScenarios(t *testing.T) {
	t.Parallel()
	for _, scenario := range []string{"duplicate", "malformed"} {
		config := simulatorapp.Config{
			AdapterID: "simulator", NATSURL: "nats://127.0.0.1:4222",
			BindingKey: "simulated-light", Scenario: scenario,
		}
		if err := config.Validate(); err == nil {
			t.Fatalf("raw transport scenario %q was accepted by the runtime simulator", scenario)
		}
	}
}
