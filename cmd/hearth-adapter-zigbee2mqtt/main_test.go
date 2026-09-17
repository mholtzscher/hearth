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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearth-adapter-zigbee2mqtt")
	})
	t.Run("invalid config detail", func(t *testing.T) {
		t.Parallel()
		cmdtest.CheckInvalidConfigDetail(t, binary, "hearth-adapter-zigbee2mqtt",
			"adapter_id: test-zigbee2mqtt\n"+
				"nats_url: nats://127.0.0.1:4222\n"+
				"mqtt:\n"+
				"  url: http://127.0.0.1:1883\n"+
				"  base_topic: zigbee2mqtt\n")
	})
	t.Run("startup cancellation", func(t *testing.T) {
		t.Parallel()
		natsURL := cmdtest.StartProcessNATS(t)
		configYAML :=
			"adapter_id: \"test-zigbee2mqtt\"\n" +
				"nats_url: \"" + natsURL + "\"\n" +
				"mqtt:\n" +
				"  url: \"tcp://127.0.0.1:1883\"\n" +
				"  base_topic: \"zigbee2mqtt\"\n"
		cmdtest.CheckStartupCancellation(t, binary, configYAML, "hearth-adapter-zigbee2mqtt")
	})
}
