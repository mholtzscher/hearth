package main

import (
	"testing"

	"github.com/mholtzscher/hearth/cmd/internal/cmdtest"
)

// This test protects executable bootstrap and fails if invalid logging flags do
// not fail fast before configuration load, echo the rejected value, or misreport
// the configuration failure in either format.
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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-zwavejs")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		// The reserved port has no listener, so the Adapter sits in its bounded
		// reconnect loop until the interrupt arrives. That is precisely the
		// startup state this check must interrupt gracefully.
		port := cmdtest.FreeLoopbackPort(t)
		configYAML :=
			"adapter_id: \"test-zwavejs\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"zwave_js:\n" +
				"  url: \"ws://127.0.0.1:" + port + "\"\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-adapter-zwavejs")
	})
}
