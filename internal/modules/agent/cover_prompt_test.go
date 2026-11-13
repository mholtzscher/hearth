package agent //nolint:testpackage // Tests the household control guidance.

import (
	"strings"
	"testing"
)

func TestCoverPromptDistinguishesDispatchReportedPositionAndRemoteLockout(t *testing.T) {
	t.Parallel()
	for _, instruction := range []string{
		"set position 100 to open, 0 to close",
		"stop with empty parameters only when advertised",
		"confirms dispatch, not that motion stopped",
		"Position may be estimated or uninitialized",
		"Remote lockout is not a physical door bolt",
		"do not infer a lock's meaning from its name alone",
		"firmware may report lockout changes optimistically",
		"not independently verified remote enforcement",
	} {
		if !strings.Contains(defaultSystemPrompt, instruction) {
			t.Errorf("missing control guidance: %s", instruction)
		}
	}
}
