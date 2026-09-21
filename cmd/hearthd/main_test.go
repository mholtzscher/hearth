package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

// This test protects executable bootstrap and fails if invalid logging flags
// do not fail fast before configuration load, echo the rejected value, or
// misreport the configuration failure in either format.
func TestMainLoggingFlagsAndConfigFailure(t *testing.T) {
	t.Parallel()
	binary := cmdtest.Build(t, ".")

	t.Run("invalid flags", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidLogFlags(t, binary)
	})
	t.Run("missing config text", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigText(t, binary)
	})
	t.Run("missing config json", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckMissingConfigJSON(t, binary, "hearthd")
	})
	t.Run("invalid config detail", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidConfigDetail(t, binary, "hearthd",
			"household_timezone: UTC\n"+
				"http_addr: not-a-host-port\n")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		apiKeyPath := filepath.Join(t.TempDir(), "agent-api-key")
		if err := os.WriteFile(apiKeyPath, []byte("test-model-api-key\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		configYAML :=
			"http_addr: \"127.0.0.1:" + cmdtest.FreeLoopbackPort(t) + "\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"household_timezone: UTC\nsqlite_path: \"" + filepath.Join(t.TempDir(), "hearth.db") + "\"\n" +
				"agent:\n  api_key_file: \"" + apiKeyPath + "\"\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearthd")
	})
}
