// Package quantitykind owns the closed vocabulary of canonical quantity meanings.
package quantitykind

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
)

const (
	Temperature              = "temperature"
	AtmosphericPressure      = "atmospheric_pressure"
	Speed                    = "speed"
	PrecipitationDepth       = "precipitation_depth"
	PrecipitationRate        = "precipitation_rate"
	RelativeHumidity         = "relative_humidity"
	WindDirection            = "wind_direction"
	PrecipitationProbability = "precipitation_probability"
	CloudCover               = "cloud_cover"
	UVIndex                  = "uv_index"
	SolarIrradiance          = "solar_irradiance"
	Brightness               = "brightness"
	Illuminance              = "illuminance"
	ColorHue                 = "color_hue"
	ColorSaturation          = "color_saturation"
	ColorTemperature         = "color_temperature"
	Chromaticity             = "chromaticity"
	BatteryLevel             = "battery_level"
	LinkQuality              = "link_quality"
	Frequency                = "frequency"
	ElectricalPower          = "electrical_power"
	PowerFactor              = "power_factor"
	Energy                   = "energy"
	ElectricCurrent          = "electric_current"
	Voltage                  = "voltage"
	Duration                 = "duration"
)

// Validate rejects unknown meanings and incompatible canonical stored unit codes.
func Validate(kind, unit string) error {
	units := map[string][]string{
		Temperature: {"mCel", "Cel"}, AtmosphericPressure: {"hPa"}, Speed: {"m/s"},
		PrecipitationDepth: {"mm"}, PrecipitationRate: {"mm/h"}, RelativeHumidity: {"%"},
		WindDirection: {"deg"}, PrecipitationProbability: {"%"}, CloudCover: {"%"}, UVIndex: {"index"},
		SolarIrradiance: {"W/m²"}, Brightness: {"%"}, Illuminance: {"lx"}, ColorHue: {"deg"},
		ColorSaturation: {"%"}, ColorTemperature: {"/MK", "mired"}, Chromaticity: {"10*-4"},
		BatteryLevel: {"%"}, LinkQuality: {"lqi"}, Frequency: {"Hz"}, ElectricalPower: {"W"},
		PowerFactor: {"ratio"}, Energy: {"kWh"}, ElectricCurrent: {"A"}, Voltage: {"V"}, Duration: {"s"},
	}
	if !slices.Contains(units[kind], unit) {
		return fmt.Errorf("invalid quantity kind %q and unit %q", kind, unit)
	}
	return nil
}

// ValidateSupport checks a generated pair of support references. An absent kind
// remains unclassified; an absent optional unit has the canonical default "1".
func ValidateSupport(support any, kindPath, unitPath, fixedUnit string) error {
	raw, err := json.Marshal(support)
	if err != nil {
		return fmt.Errorf("encode support: %w", err)
	}
	var object map[string]any
	if decodeErr := json.Unmarshal(raw, &object); decodeErr != nil {
		return fmt.Errorf("decode support: %w", decodeErr)
	}
	kind, present, err := supportString(object, kindPath)
	if err != nil || !present {
		return err
	}
	unit := fixedUnit
	if unitPath != "" {
		var unitPresent bool
		unit, unitPresent, err = supportString(object, unitPath)
		if err != nil {
			return err
		}
		if !unitPresent {
			unit = "1"
		}
	}
	return Validate(kind, unit)
}

func supportString(object map[string]any, path string) (string, bool, error) {
	var value any = object
	for token := range strings.SplitSeq(strings.TrimPrefix(path, "/"), "/") {
		container, ok := value.(map[string]any)
		if !ok {
			return "", false, fmt.Errorf("invalid support path %q", path)
		}
		value, ok = container[token]
		if !ok {
			return "", false, nil
		}
	}
	text, ok := value.(string)
	if !ok || text == "" {
		return "", false, fmt.Errorf("support path %q must be a nonempty string", path)
	}
	return text, true, nil
}
