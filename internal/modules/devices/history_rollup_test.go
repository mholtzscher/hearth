package devices_test

import (
	"slices"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestHistoryRollupFieldsListsOnlyNumericFields(t *testing.T) {
	t.Parallel()
	if got := devices.HistoryRollupFields(devices.EntityTypeColortempV1); !slices.Equal(got, []string{"/value"}) {
		t.Fatalf("colortemp fields = %v, want [/value]", got)
	}
	if got := devices.HistoryRollupFields(devices.EntityTypePowerV1); len(got) != 0 {
		t.Fatalf("power fields = %v, want none", got)
	}
	if got := devices.HistoryRollupFields(devices.EntityTypeTemperatureV1); !slices.Equal(got, []string{""}) {
		t.Fatalf("temperature fields = %v, want root", got)
	}
}
