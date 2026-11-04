package devices_test

import (
	"slices"
	"testing"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

func TestBuiltinReadingCoversQuantityTypes(t *testing.T) {
	t.Parallel()
	for _, want := range []struct {
		id      devices.EntityTypeID
		reading devices.Reading
	}{
		{devices.EntityTypeTemperatureV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "temperature", FixedUnit: "mCel",
			DefaultDisplay: "celsius", Displays: []string{"celsius", "fahrenheit"},
		}},
		{devices.EntityTypeRelativehumidityV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "relative_humidity", FixedUnit: "%",
			DefaultDisplay: "percent", Displays: []string{"percent"},
		}},
		{devices.EntityTypePressureV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "pressure", FixedUnit: "hPa",
			DefaultDisplay: "hpa", Displays: []string{"hpa"},
		}},
		{devices.EntityTypeSpeedV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "speed", FixedUnit: "m/s",
			DefaultDisplay: "mps", Displays: []string{"mps"},
		}},
		{devices.EntityTypeBrightnessV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "level", FixedUnit: "%",
			DefaultDisplay: "percent", Displays: []string{"percent"},
		}},
		{devices.EntityTypeNumericsensorV1, devices.Reading{
			Kind: devices.ReadingQuantity, Quantity: "generic", SupportUnitPath: "/state/unit",
			DefaultDisplay: "native", Displays: []string{"native"},
		}},
	} {
		got, ok := devices.BuiltinReading(want.id)
		expected := want.reading
		if !ok || got.Kind != expected.Kind || got.Quantity != expected.Quantity ||
			got.FixedUnit != expected.FixedUnit || got.SupportUnitPath != expected.SupportUnitPath ||
			got.DefaultDisplay != expected.DefaultDisplay || !slices.Equal(got.Displays, expected.Displays) {
			t.Errorf("BuiltinReading(%s) = %+v, %v; want %+v", want.id, got, ok, expected)
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

func TestReadingAcceptsOnlyItsDisplays(t *testing.T) {
	t.Parallel()
	temperature, _ := devices.BuiltinReading(devices.EntityTypeTemperatureV1)
	if !temperature.AcceptsDisplay("celsius") || !temperature.AcceptsDisplay("fahrenheit") ||
		temperature.AcceptsDisplay("percent") || temperature.AcceptsDisplay("native") {
		t.Fatalf("temperature displays = %v", temperature.Displays)
	}
}
