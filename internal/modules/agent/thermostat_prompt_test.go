package agent //nolint:testpackage // Checks the household prompt's explicit thermostat control contract.

import (
	"strings"
	"testing"
)

func TestThermostatPromptSeparatesSettingsActivityUnitsAndHumidityControls(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"discover supported setters",
		"mode is a setting and activity is a separate report",
		"integer milli-Celsius, not displayed Celsius or Fahrenheit",
		"set_range requires both low and high",
		"Setting targets does not select a mode",
		"exact advertised fan_mode",
		"reported setting, not active heating or cooling",
		"climate humidity target is dehumidification",
		"separate Humidification Target Percentage number controls humidification",
		"Missing readings are unknown, not zero or idle",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("missing thermostat control guidance: %s", instruction)
		}
	}
}
