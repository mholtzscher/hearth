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

func (relayPlanner) Plan(input devicePlanningInput) (plannerContribution, error) {
	contribution := plannerContribution{Kind: upstreamDeviceKindRelay}
	for _, root := range input.Exposes.Roots(upstreamExposeSwitch) {
		if !root.resolved {
			continue
		}
		powerFeature, ok := input.Exposes.UniqueFeature(root, featureQuery{Type: upstreamExposeBinary, Name: "state"})
		if !ok || !validPowerFeature(powerFeature) || !input.Exposes.PropertyUnique(powerFeature.Property) {
			continue
		}
		powerOn, onErr := canonicalScalar(powerFeature.ValueOn)
		powerOff, offErr := canonicalScalar(powerFeature.ValueOff)
		if onErr != nil || offErr != nil || powerOn.canonical == powerOff.canonical {
			continue
		}
		metadata, ok := powerMetadata(input.IEEE, root)
		if !ok {
			continue
		}
		power, err := newPowerPlan(metadata, powerFeature.Property, powerOn, powerOff)
		if err != nil {
			continue
		}
		contribution.Entities = append(contribution.Entities, power)
	}
	return contribution, nil
}
