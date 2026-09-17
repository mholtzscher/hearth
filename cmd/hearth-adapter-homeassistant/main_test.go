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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-homeassistant")
	})
	t.Run("invalid config detail", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidConfigDetail(t, binary, "hearth-adapter-homeassistant",
			"adapter_id: test-homeassistant\n"+
				"nats_url: not-a-nats-url\n")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		tokenPath := filepath.Join(t.TempDir(), "token")
		if err := os.WriteFile(tokenPath, []byte("test-token"), 0o600); err != nil {
			t.Fatal(err)
		}
		configYAML :=
			"adapter_id: \"test-homeassistant\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"binding:\n" +
				"  key: \"test-light\"\n" +
				"  device_name: \"Test light\"\n" +
				"  entity_id: \"light.test\"\n" +
				"  entity_name: \"Test light\"\n" +
				"home_assistant:\n" +
				"  url: \"http://127.0.0.1:8123\"\n" +
				"  token_file: \"" + tokenPath + "\"\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-adapter-homeassistant")
	})
}
