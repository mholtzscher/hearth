package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestDefaultSystemPromptGuidesAdapterFunctionInvocation(t *testing.T) {
	t.Parallel()
	for _, guidance := range []string{
		"advertised by the function schema",
		"notification destination display name",
		"ask the user instead of guessing",
	} {
		if !strings.Contains(defaultSystemPrompt, guidance) {
			t.Fatalf("default system prompt does not contain %q", guidance)
		}
	}
}
