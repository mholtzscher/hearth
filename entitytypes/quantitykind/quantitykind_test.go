package quantitykind //nolint:testpackage // The table exercises the vocabulary directly.

import "testing"

func TestValidate(t *testing.T) {
	t.Parallel()
	pairs := map[string][]string{
		Temperature: {"mCel", "Cel"}, AtmosphericPressure: {"hPa"}, Speed: {"m/s"},
		PrecipitationDepth: {"mm"}, PrecipitationRate: {"mm/h"}, RelativeHumidity: {"%"},
		WindDirection: {"deg"}, PrecipitationProbability: {"%"}, CloudCover: {"%"},
		UVIndex: {"index"}, SolarIrradiance: {"W/m²"}, Brightness: {"%"},
		Illuminance: {"lx"}, ColorHue: {"deg"}, ColorSaturation: {"%"},
		ColorTemperature: {"/MK", "mired"}, Chromaticity: {"10*-4"}, BatteryLevel: {"%"},
		LinkQuality: {"lqi"}, Frequency: {"Hz"}, ElectricalPower: {"W"}, PowerFactor: {"ratio"},
		Energy: {"kWh"}, ElectricCurrent: {"A"}, Voltage: {"V"}, Duration: {"s"},
		CarbonDioxide: {"ppm"}, ParticulateMatter: {"µg/m³"}, AirQualityIndex: {"index"},
	}
	if len(pairs) != 29 {
		t.Fatalf("got %d kinds", len(pairs))
	}
	for kind, units := range pairs {
		for _, unit := range units {
			if err := Validate(kind, unit); err != nil {
				t.Errorf("%s/%s: %v", kind, unit, err)
			}
		}
		if err := Validate(kind, "incompatible"); err == nil {
			t.Errorf("%s accepted incompatible unit", kind)
		}
	}
	if err := Validate("unknown", "%"); err == nil {
		t.Error("unknown kind accepted")
	}
}
