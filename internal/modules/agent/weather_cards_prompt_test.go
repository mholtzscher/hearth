package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestWeatherCardGuidanceNamesSlotsAndUnits(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"OutsideNow", "Rain", "Wind", "Pressure", "hearth.temperature/v1", "hearth.pressure/v1",
		"mm/h for a rain rate", "mm for rain totals", "deg for wind direction", "Choose them by unit, not by name",
		"never sums of the rate",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("weather card guidance omits %q", instruction)
		}
	}
}
