package household_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/household"
)

func TestParseUnitSystem(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		value   string
		want    household.UnitSystem
		wantErr bool
	}{
		{"", household.UnitSystemMetric, false},
		{"metric", household.UnitSystemMetric, false},
		{"us", household.UnitSystemUS, false},
		{"US", "", true},
		{"imperial", "", true},
	} {
		got, err := household.ParseUnitSystem(test.value)
		if (err != nil) != test.wantErr || got != test.want {
			t.Errorf("ParseUnitSystem(%q) = %q, %v", test.value, got, err)
		}
	}
}
