package zigbee2mqtt

// planSensorFamily plans the semantic measurement table first, then the
// binary capability table, each in order. Every mapping visits retained roots
// independently so an ineligible root cannot suppress valid siblings. Family
// order and root selection stay in Go; catalog records contain only
// capability data.
func planSensorFamily(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindSensor, Role: plannerRoleSupplemental}
	for _, mapping := range measurementMappings() {
		appendMeasurementPlans(&contribution, input, mapping)
	}
	for _, mapping := range binarySensorMappings() {
		appendBinarySensorPlans(&contribution, input, mapping)
	}
	return contribution
}

// appendMeasurementPlans adds one read-only semantic measurement Entity per
// eligible resolved root, skipping ineligible roots individually so valid
// siblings survive. Bounds, kind, and canonical unit come from the mapping.
func appendMeasurementPlans(
	contribution *plannerContribution,
	input devicePlanningInput,
	mapping measurementMapping,
) {
	for _, root := range input.Exposes.roots {
		if plan := planMeasurementRoot(input, root, mapping); plan != nil {
			contribution.Entities = append(contribution.Entities, *plan)
		}
	}
}

// readOnlySensorEligible reports whether one resolved root expose may
// register a read-only sensor: a non-empty Device-unique State property,
// publish access, and no set access. Callers add their own type, name, unit,
// or declaration gate.
func readOnlySensorEligible(input devicePlanningInput, expose upstreamExpose) bool {
	return expose.Property != "" &&
		exposeCanPublish(expose) && !exposeCanSet(expose) &&
		input.Exposes.PropertyUnique(expose.Property)
}
