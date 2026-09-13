package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

// This test protects executable bootstrap and fails if invalid logging flags do
// not fail fast before configuration load, echo the rejected value, or
// misreport configuration failure in either format.
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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-ecowitt")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		passkeyFile := filepath.Join(t.TempDir(), "passkey")
		if err := os.WriteFile(passkeyFile, []byte("0123456789abcdef0123456789abcdef\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		configYAML := "adapter_id: \"test-ecowitt\"\n" +
			"nats_url: \"" + natsURL + "\"\n" +
			"mqtt:\n" +
			"  url: \"tcp://127.0.0.1:1883\"\n" +
			"  topic: \"ecowitt/943cc64457a7\"\n" +
			"station:\n" +
			"  gateway_name: \"Weather Station Gateway\"\n" +
			"  outdoor_array_name: \"Outdoor Weather Array\"\n" +
			"  passkey_file: \"" + passkeyFile + "\"\n" +
			"  upload_interval_seconds: 16\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-adapter-ecowitt")
	})
}
