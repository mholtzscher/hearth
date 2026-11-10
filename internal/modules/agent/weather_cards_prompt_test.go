package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestWeatherCardGuidanceNamesSlotsAndUnits(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"OutsideNow", "Rain", "Wind", "Pressure", "hearth.temperature/v1", "hearth.pressure/v1",
		"mm/h for a rain rate", "mm for rain totals", "deg for wind direction", "sensor named for that period", "leave an optional slot out rather than guess",
		"never sums of the rate",
		"actual Entity readings and retained history",
		"not derived feels-like, dew point, or condition classifications",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("weather card guidance omits %q", instruction)
		}
	}
}

func TestReadUnitsGuidancePreservesCanonicalWriteInputs(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"household-unit values by default", "read quantities", "units:canonical",
		"Never copy a displayed number into a canonical write", "audit and Automation definitions remain canonical",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("read-unit guidance omits %q", instruction)
		}
	}
}
