package zigbee2mqtt

// relayPlanner considers switch root exposes. Each eligible root requires
// exactly one binary state feature with the same rules as light power and
// produces a hearth.power/v1 Entity through the shared power constructor, so
// State and Command behavior stay identical across primary families.
//
// The Adapter calls the Device kind relay, not plug or switch, because the
// Zigbee2MQTT expose proves a controllable relay but does not reliably
// distinguish a plug from an in-wall switch.
type relayPlanner struct{}

func (relayPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindRelay, Role: plannerRolePrimary}
	for _, root := range input.Exposes.Roots(upstreamExposeSwitch) {
		if !root.resolved {
			continue
		}
		power, ok := planPowerEntity(input, root)
		if !ok {
			continue
		}
		contribution.Entities = append(contribution.Entities, power)
	}
	return contribution
}
