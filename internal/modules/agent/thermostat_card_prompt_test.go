package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestThermostatCardGuidance(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"hearth.thermostat/v1 Entity, use Thermostat bound to the Entity's root",
		"targets the current mode uses with −/+ controls",
		"mode and activity, inside temperature and humidity, and Mode and Fan controls",
	} {
		if !strings.Contains(strings.Join(strings.Fields(defaultSystemPrompt), " "), instruction) {
			t.Errorf("thermostat card guidance omits %q", instruction)
		}
	}
}
