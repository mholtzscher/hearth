package devices_test

import (
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestBuiltinReadingCoversQuantityTypes(t *testing.T) {
	t.Parallel()
	for _, want := range []struct {
		id      devices.EntityTypeID
		reading devices.Reading
	}{
		{devices.EntityTypeTemperatureV1, devices.Reading{Kind: devices.ReadingQuantity, ValueType: "integer", FixedUnit: "mCel"}},
		{devices.EntityTypeRelativehumidityV1, devices.Reading{Kind: devices.ReadingQuantity, ValueType: "number", FixedUnit: "%"}},
		{devices.EntityTypePressureV1, devices.Reading{Kind: devices.ReadingQuantity, ValueType: "number", FixedUnit: "hPa"}},
		{devices.EntityTypeSpeedV1, devices.Reading{Kind: devices.ReadingQuantity, ValueType: "number", FixedUnit: "m/s"}},
		{devices.EntityTypeBrightnessV1, devices.Reading{Kind: devices.ReadingQuantity, ValueType: "integer", FixedUnit: "%"}},
		{devices.EntityTypeNumericsensorV1, devices.Reading{
			Kind: devices.ReadingQuantity, ValueType: "number", SupportUnitPath: "/state/unit",
		}},
	} {
		if got, ok := devices.BuiltinReading(want.id); !ok || got != want.reading {
			t.Errorf("BuiltinReading(%s) = %+v, %v; want %+v", want.id, got, ok, want.reading)
		}
	}
	for _, id := range []devices.EntityTypeID{
		devices.EntityTypePowerV1, devices.EntityTypeColortempV1, devices.EntityTypeBinarysensorV1,
	} {
		if _, ok := devices.BuiltinReading(id); ok {
			t.Errorf("BuiltinReading(%s) reported a reading", id)
		}
	}
}
