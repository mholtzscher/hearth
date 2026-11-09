package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestPowerCardGuidanceKeepsIntentAndBindingsExplicit(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"PowerTile", "SwitchList", "hearth.power/v1", "power Entity's Device",
		"one explicit numeric source", "Ordinary power tiles have no sparkline",
		"only on an explicit browser gesture", "saving a view never executes Commands",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("power card guidance omits %q", instruction)
		}
	}
	if strings.Contains(defaultSystemPrompt, "Cards are read-only and never execute Commands") {
		t.Fatal("guidance incorrectly denies native browser controls")
	}
}
