package automations_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

// newEntityID mints one canonical Entity identity for a fixture.
func newEntityID(t *testing.T) devices.EntityID {
	t.Helper()
	id, err := devices.NewEntityID()
	if err != nil {
		t.Fatal(err)
	}
	return id
}
