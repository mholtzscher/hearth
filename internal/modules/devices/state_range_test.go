package devices_test

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestResolveRangeUsesDeclaredBounds(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		id            devices.EntityTypeID
		path, support string
		want          devices.ResolvedRange
		err           bool
	}{
		{"percent", devices.EntityTypeBrightnessV1, "", `{}`, devices.ResolvedRange{Minimum: 0, Maximum: 100}, false},
		{"sensor", devices.EntityTypeNumericsensorV1, "", `{"state":{"minimum":400,"maximum":2000}}`, devices.ResolvedRange{Minimum: 400, Maximum: 2000}, false},
		{"equal", devices.EntityTypeNumericsensorV1, "", `{"state":{"minimum":400,"maximum":400}}`, devices.ResolvedRange{}, true},
		{"missing", devices.EntityTypeNumericsensorV1, "", `{"state":{}}`, devices.ResolvedRange{}, true},
		{"string", devices.EntityTypeNumericsensorV1, "", `{"state":{"minimum":400,"maximum":"2000"}}`, devices.ResolvedRange{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			field, _ := devices.BuiltinStateField(tc.id, tc.path)
			got, err := devices.ResolveRange(field.Value.(devices.NumericField), json.RawMessage(tc.support))
			if (err != nil) != tc.err || got != tc.want {
				t.Fatalf("range = %+v, %v", got, err)
			}
		})
	}
	field, _ := devices.BuiltinStateField(devices.EntityTypeTemperatureV1, "")
	if _, err := devices.ResolveRange(
		field.Value.(devices.NumericField),
		nil,
	); !errors.Is(
		err,
		devices.ErrNoQuantityRange,
	) {
		t.Fatal(err)
	}
}
