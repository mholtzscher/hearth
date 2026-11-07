package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestDefaultSystemPromptRoutesSavedAutomationCards(t *testing.T) {
	t.Parallel()
	for _, guidance := range []string{
		"To show a saved Automation as a card",
		"present_automation with mode reference",
		"discovered automation_id",
	} {
		if !strings.Contains(defaultSystemPrompt, guidance) {
			t.Fatalf("default system prompt omits saved Automation presentation guidance: %q", guidance)
		}
	}
}
