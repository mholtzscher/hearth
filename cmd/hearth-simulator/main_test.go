package main

import (
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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-simulator")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		configYAML :=
			"adapter_id: \"test-simulator\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"devices:\n" +
				"  - binding_key: \"test-light\"\n" +
				"    name: \"Test light\"\n" +
				"    kind: \"light\"\n" +
				"    entities:\n" +
				"      - key: \"power\"\n" +
				"        name: \"Power\"\n" +
				"        type: \"hearth.power/v1\"\n" +
				"        support: {state: {}, operations: {set: {}}}\n" +
				"        initial: true\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-simulator")
	})
}
