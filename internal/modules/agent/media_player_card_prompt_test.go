package agent //nolint:testpackage // The contract under test is the private built-in prompt.

import (
	"strings"
	"testing"
)

func TestMediaPlayerCardGuidance(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"hearth.mediaplayer/v1 Entity, use MediaPlayer bound to the Entity's root",
		"layout card (the default)",
		"layout tile in a Grid for a compact play/pause control",
		"Grouped speakers cannot be controlled.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(defaultSystemPrompt), " "), instruction) {
			t.Errorf("media player card guidance omits %q", instruction)
		}
	}
}
