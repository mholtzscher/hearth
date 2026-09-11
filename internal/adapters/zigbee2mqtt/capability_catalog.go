package zigbee2mqtt

// numericSensorMapping maps one exact Zigbee2MQTT numeric expose to a
// read-only Hearth Entity. Upstream units must match exactly; an empty unit
// is not a wildcard. The family planner decides root selection and bounds:
// ambient sensors use these fixed bounds, electrical sensors prefer valid
// upstream bounds and use these bounds only when both are absent.
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

// ambientNumericSensors preserves capability order, then inventory order
// within each capability. Temperature has a different contract and is
// planned separately before these read-only numeric sensors.
//
//nolint:mnd // Literal catalog bounds are capability data, not algorithmic constants.
func ambientNumericSensors() []numericSensorMapping {
	return []numericSensorMapping{
		{
			exposeName: "humidity", key: "humidity", displayName: "Humidity",
			upstreamUnit: "%", unit: "%", minimum: 0, maximum: 100,
		},
		{
			// The Third Reality 3RSNL02043Z night light reports a root
			// illuminance expose in lux. The envelope is a validation bound,
			// not a claimed operating range.
			exposeName: "illuminance", key: "illuminance", displayName: "Illuminance",
			upstreamUnit: "lx", unit: "lx", minimum: 0, maximum: 1e9,
		},
		{
			exposeName: "battery", key: "battery", displayName: "Battery",
			upstreamUnit: "%", unit: "%", minimum: 0, maximum: 100,
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
