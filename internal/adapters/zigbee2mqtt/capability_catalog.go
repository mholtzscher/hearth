package zigbee2mqtt

// numericSensorMapping maps one exact Zigbee2MQTT numeric expose to a
// read-only hearth.numericsensor/v1 Entity. Upstream units must match
// exactly; an empty unit is not a wildcard. It is retained for numeric
// readings that intentionally carry no first-class measurement semantics:
// link quality and the smart-plug electrical diagnostics.
//
// This is capability data, not a device-model catalog or a planning language.
// Familiar exposes on another model need no entry. New conversion or family
// behavior belongs in Go, not in flags or expressions on these records.
type numericSensorMapping struct {
	exposeName   string
	key          string
	displayName  string
	upstreamUnit string
	unit         string
	minimum      float64
	maximum      float64
}

// measurementMappings preserves semantic-measurement capability order, then
// inventory order within each capability. Every record selects one
// measurement kind, its single canonical UCUM unit, and the kind-wide
// envelope from the authoritative contract; the mapping is not authoritative
// and the generated facade rejects a kind/unit pair the contract does not
// admit.
//
//nolint:goconst,mnd // Literal capability data: bounds and initial-kind names, not algorithmic constants.
func measurementMappings() []measurementMapping {
	return []measurementMapping{
		{
			// Zigbee2MQTT reports ambient temperature in °C. Celsius magnitude
			// is unchanged; the canonical UCUM unit is Cel, so upstream 21.5
			// stays 21.5 rather than a scaled integer.
			exposeName: "temperature", key: "temperature", displayName: "Temperature",
			upstreamUnit: "°C", measurementKind: "temperature", canonicalUnit: "Cel",
			minimum: -273.15, maximum: 1000,
		},
		{
			exposeName: "humidity", key: "humidity", displayName: "Humidity",
			upstreamUnit: "%", measurementKind: "relative_humidity", canonicalUnit: "%",
			minimum: 0, maximum: 100,
		},
		{
			// The Third Reality 3RSNL02043Z night light reports a root
			// illuminance expose in lux. The envelope is a validation bound,
			// not a claimed operating range.
			exposeName: "illuminance", key: "illuminance", displayName: "Illuminance",
			upstreamUnit: "lx", measurementKind: "illuminance", canonicalUnit: "lx",
			minimum: 0, maximum: 1e9,
		},
		{
			exposeName: "battery", key: "battery", displayName: "Battery",
			upstreamUnit: "%", measurementKind: "battery_level", canonicalUnit: "%",
			minimum: 0, maximum: 100,
		},
	}
}

// smartPlugElectricalSensors preserves relay attribute order. The bounds
// are conservative validation envelopes, not claimed operating ranges.
// Numeric power deliberately has a different key from boolean relay power.
//
//nolint:mnd // Keep each fallback validation envelope beside its exact capability mapping.
func smartPlugElectricalSensors() []numericSensorMapping {
	return []numericSensorMapping{
		{
			exposeName: "ac_frequency", key: "acfrequency", displayName: "AC Frequency",
			upstreamUnit: "Hz", unit: "Hz", minimum: 0, maximum: 1000,
		},
		{
			exposeName: "power", key: "electricalpower", displayName: "Electrical Power",
			upstreamUnit: "W", unit: "W", minimum: 0, maximum: 1e9,
		},
		{
			exposeName: "power_factor", key: "powerfactor", displayName: "Power Factor",
			upstreamUnit: "", unit: "ratio", minimum: 0, maximum: 1,
		},
		{
			exposeName: "energy", key: "energy", displayName: "Energy",
			upstreamUnit: "kWh", unit: "kWh", minimum: 0, maximum: 1e15,
		},
		{
			exposeName: "current", key: "current", displayName: "Current",
			upstreamUnit: "A", unit: "A", minimum: 0, maximum: 1e6,
		},
		{
			exposeName: "voltage", key: "voltage", displayName: "Voltage",
			upstreamUnit: "V", unit: "V", minimum: 0, maximum: 1e6,
		},
	}
}

// binarySensorMapping maps one exact Zigbee2MQTT binary expose to a
// read-only Hearth Entity. The record carries capability identity only: the
// root's Device-unique State property, endpoint, and declared value_on/
// value_off all come from inventory, so a new on/off capability such as
// contact, leak, or smoke is one record plus capture tests.
//
// This is capability data, not a device-model catalog or a planning language.
type binarySensorMapping struct {
	exposeName  string
	key         string
	displayName string
}

// binarySensorMappings preserves read-only binary capability order. These
// records follow the numeric sensor records so the 3RSNL02043Z night light
// keeps illuminance before occupancy.
func binarySensorMappings() []binarySensorMapping {
	return []binarySensorMapping{
		{
			// The Third Reality 3RSNL02043Z night light reports a device-root
			// occupancy expose with declared true/false scalars.
			exposeName: "occupancy", key: "occupancy", displayName: "Occupancy",
		},
	}
}

// numericSettingMapping identifies one observable setting. An empty
// expectedUnit requires an empty upstream unit and omits the Hearth unit.
type numericSettingMapping struct {
	exposeName   string
	key          string
	displayName  string
	expectedUnit string
}

// smartPlugNumericSettings preserves relay setting order. All settings use
// discovered bounds and require publish, set, and get access.
func smartPlugNumericSettings() []numericSettingMapping {
	return []numericSettingMapping{
		{
			exposeName: "led_brightness", key: "ledbrightness", displayName: "LED Brightness",
			expectedUnit: "%",
		},
		{
			exposeName: "countdown_to_turn_off", key: "countdowntoturnoff",
			displayName: "Countdown To Turn Off", expectedUnit: "s",
		},
		{
			exposeName: "countdown_to_turn_on", key: "countdowntoturnon",
			displayName: "Countdown To Turn On", expectedUnit: "s",
		},
	}
}
