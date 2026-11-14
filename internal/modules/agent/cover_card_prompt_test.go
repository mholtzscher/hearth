package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestCoverCardGuidanceKeepsTheOpeningConfirmation(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"hearth.cover/v1 Entity, such as a garage door or shade, use Cover",
		"layout tile in a Grid of quick switches",
		"Opening asks for a second press unless confirm_open is false",
		"set it false only when the user says the cover is a shade",
	} {
		if !strings.Contains(strings.Join(strings.Fields(defaultSystemPrompt), " "), instruction) {
			t.Errorf("cover card guidance omits %q", instruction)
		}
	}
}
