package zigbee2mqtt

import "bytes"

// electricalSensorBounds prefers both valid finite upstream bounds. Both
// absent uses the conservative validation envelope from the capability
// mapping. Malformed, one-sided, or inverted bounds omit only that sensor.
func electricalSensorBounds(expose upstreamExpose, fallbackMin, fallbackMax float64) (float64, float64, bool) {
	if expose.ValueMin == nil && expose.ValueMax == nil {
		minimumAbsent := len(bytes.TrimSpace(expose.valueMinRaw)) == 0
		maximumAbsent := len(bytes.TrimSpace(expose.valueMaxRaw)) == 0
		if minimumAbsent && maximumAbsent {
			return fallbackMin, fallbackMax, true
		}
		return 0, 0, false
	}
	if expose.ValueMin == nil || expose.ValueMax == nil {
		return 0, 0, false
	}
	minimum, maximum := *expose.ValueMin, *expose.ValueMax
	if !isFinite(minimum) || !isFinite(maximum) || minimum >= maximum {
		return 0, 0, false
	}
	return minimum, maximum, true
}

// planElectricalSensor selects exactly one device-root expose, including
// unresolved roots in the ambiguity check, then resolves its bounds before
// using the shared read-only numeric sensor translation. Relay power gates
// these attributes in planRelayFamily, not in the capability catalog.
func planElectricalSensor(input devicePlanningInput, mapping numericSensorMapping) *entityPlan {
	root, ok := input.Exposes.UniqueRoot(upstreamExposeNumeric, mapping.exposeName)
	if !ok || !root.resolved {
		return nil
	}
	minimum, maximum, valid := electricalSensorBounds(root.expose, mapping.minimum, mapping.maximum)
	if !valid {
		return nil
	}
	mapping.minimum, mapping.maximum = minimum, maximum
	return planNumericSensorRoot(input, root, mapping)
}
