package agenteval //nolint:testpackage // Shared test setup for the package.

import (
	"os"
	"testing"
)

// fakeProviderKey is the synthetic credential tests give Core and workers. The
// driver test fails if it appears in any run output.
const fakeProviderKey = "fake-provider-key-must-not-appear"

// TestMain replaces inherited credentials before tests run, so no test can
// reach a hosted provider and parallel tests need no per-test environment.
func TestMain(m *testing.M) {
	for _, name := range []string{"HEARTH_OPENAI_API_KEY", "HEARTH_OPENCODE_GO_API_KEY"} {
		if err := os.Setenv(name, fakeProviderKey); err != nil {
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}
