package zigbee2mqtt

// planRelayFamily considers switch root exposes. Each eligible root requires
// exactly one binary state feature with the same rules as light power and
// produces a hearth.power/v1 Entity through the shared power constructor, so
// State and Command behavior stay identical across primary families.
//
// Device-root smart-plug attributes (power-on behavior, electrical sensors,
// numeric settings, and the reset action) join only when at least one
// eligible relay power Entity survives, mirroring how light power gates its
// family. Duplicate power keys omit the ambiguous roots while valid siblings
// survive; a malformed attribute is omitted without suppressing valid
// siblings.
//
// The Adapter calls the Device kind relay, not plug or switch, because the
// Zigbee2MQTT expose proves a controllable relay but does not reliably
// distinguish a plug from an in-wall switch.
func planRelayFamily(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindRelay, Role: plannerRolePrimary}
	var powers []entityPlan
	for _, root := range input.Exposes.Roots(upstreamExposeSwitch) {
		if !root.resolved {
			continue
		}
		power, ok := planPowerEntity(input, root)
		if !ok {
			continue
		}
		powers = append(powers, power)
	}
	// Same-root dedup omits ambiguous power keys while valid siblings
	// survive; without a surviving power the device is not a relay and no
	// device-root attribute may join.
	counts := make(map[string]int, len(powers))
	for _, power := range powers {
		counts[power.Descriptor.Key]++
	}
	survived := false
	for _, power := range powers {
		if counts[power.Descriptor.Key] != 1 {
			continue
		}
		contribution.Entities = append(contribution.Entities, power)
		survived = true
	}
	if !survived {
		return contribution
	}
	if powerOnBehavior := planPowerOnBehavior(input); powerOnBehavior != nil {
		contribution.Entities = append(contribution.Entities, *powerOnBehavior)
	}
	for _, spec := range smartPlugElectricalSensors() {
		if sensor := planElectricalSensor(input, spec); sensor != nil {
			contribution.Entities = append(contribution.Entities, *sensor)
		}
	}
	for _, spec := range smartPlugNumericSettings() {
		if setting := planNumericSetting(input, spec); setting != nil {
			contribution.Entities = append(contribution.Entities, *setting)
		}
	}
	if reset := planResetTotalEnergy(input); reset != nil {
		contribution.Entities = append(contribution.Entities, *reset)
	}
	return contribution
}
