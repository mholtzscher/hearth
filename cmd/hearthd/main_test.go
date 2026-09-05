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
		cmdtest.CheckMissingConfigJSON(t, binary, "hearthd")
	})
}
