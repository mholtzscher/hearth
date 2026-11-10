package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestForecastCardGuidance(t *testing.T) {
	t.Parallel()
	const guidance = "Forecast binds one hourly or daily forecast Entity; hourly periods is 6–48 (default 24) and daily 3–8 (default 8). It shows modeled forecasts, not station readings."
	if !strings.Contains(defaultSystemPrompt, guidance) {
		t.Error("forecast guidance is missing")
	}
}
