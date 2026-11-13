package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestAirPurifierGuidanceBindsRolesExplicitly(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"kind air-purifier, use AirPurifier", "PM2.5 numeric sensor in µg/m³",
		"mode, fan_level, display, child_lock, and filter_life", "Identify Display and Child lock by their Entity names",
		"Omit a role you cannot identify", "layout tile in a Grid of quick switches",
		"Every bound Entity must belong to the power Entity's Device",
	} {
		if !strings.Contains(strings.Join(strings.Fields(defaultSystemPrompt), " "), instruction) {
			t.Errorf("air purifier guidance omits %q", instruction)
		}
	}
}
