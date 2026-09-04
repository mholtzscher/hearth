package zigbee2mqtt

import (
	"github.com/mholtzscher/hearth/sdk/adapter"
)

const (
	upstreamBrightnessName = "brightness"
	upstreamColorTempName  = "color_temp"
)

// lightPlanner moves current light discovery without changing its contract:
// valid power gates the family, brightness and color temperature join only
// when each optional feature is valid, and malformed optional features are
// isolated without suppressing valid siblings.
type lightPlanner struct{}

//nolint:gocognit // One pass keeps the power gate and optional-feature isolation visibly together.
func (lightPlanner) Plan(input devicePlanningInput) (plannerContribution, error) {
	contribution := plannerContribution{Kind: upstreamDeviceKindLight}
	type rootCandidate struct {
		power entityPlan
		extra []entityPlan
	}
	var candidates []rootCandidate
	for _, root := range input.Exposes.Roots(upstreamDeviceKindLight) {
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
		entities := []entityPlan{}
		if brightness := planBrightness(input, root); brightness != nil {
			entities = append(entities, *brightness)
		}
		if colorTemp := planColorTemp(input, root); colorTemp != nil {
			entities = append(entities, *colorTemp)
		}
		candidates = append(candidates, rootCandidate{power: power, extra: entities})
	}
	// Same-family dedup omits whole duplicate power-root candidates: when two
	// roots resolve to one power key, every optional Entity from those roots is
	// dropped with the ambiguous power so brightness cannot survive without
	// eligible power.
	counts := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		counts[candidate.power.Descriptor.Key]++
	}
	for _, candidate := range candidates {
		if counts[candidate.power.Descriptor.Key] != 1 {
			continue
		}
		contribution.Entities = append(contribution.Entities, candidate.power)
		contribution.Entities = append(contribution.Entities, candidate.extra...)
	}
	return contribution, nil
}

func planBrightness(input devicePlanningInput, root indexedExpose) *entityPlan {
	feature, ok := input.Exposes.UniqueFeature(
		root,
		featureQuery{Type: upstreamExposeNumeric, Name: upstreamBrightnessName},
	)
	if !ok || !validBrightnessFeature(feature) || !input.Exposes.PropertyUnique(feature.Property) {
		return nil
	}
	key, name := scopedIdentity("brightness", "Brightness", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newBrightnessPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/brightness",
		Name:       name,
	}, feature.Property, *feature.ValueMax)
	if err != nil {
		return nil
	}
	return &plan
}

func planColorTemp(input devicePlanningInput, root indexedExpose) *entityPlan {
	feature, ok := input.Exposes.UniqueFeature(
		root,
		featureQuery{Type: upstreamExposeNumeric, Name: upstreamColorTempName},
	)
	minimum, maximum, valid := colorTempRange(feature)
	if !ok || !valid || !input.Exposes.PropertyUnique(feature.Property) {
		return nil
	}
	key, name := scopedIdentity("colortemp", "Color Temperature", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newColorTempPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/colortemp",
		Name:       name,
	}, feature.Property, minimum, maximum)
	if err != nil {
		return nil
	}
	return &plan
}
