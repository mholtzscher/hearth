package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

// This test protects the built-in agent workflow and fails if Automation
// creation loses the scalar-pointer rule or post-save verification guidance.
func TestDefaultSystemPromptGuidesAutomationCreation(t *testing.T) {
	t.Parallel()
	for _, guidance := range []string{
		"inspect each referenced Entity's state.value",
		"use value_pointer \"\" for a scalar",
		"never use /state/value",
		"read the Automation back",
		"inspect its history",
	} {
		if !strings.Contains(defaultSystemPrompt, guidance) {
			t.Fatalf("default system prompt does not contain %q", guidance)
		}
	}
}
