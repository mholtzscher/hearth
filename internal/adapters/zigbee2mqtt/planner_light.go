package zigbee2mqtt

import (
	"math/big"

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

// colorModePolicy carries one root's companion mode property and whether
// Hearth may trust it. An ambiguous or colliding property omits the
// affected color and mode capabilities instead of guessing.
type colorModePolicy struct {
	property string
	usable   bool
}

//nolint:gocognit // One pass keeps the power gate and optional-feature isolation visibly together.
func (lightPlanner) Plan(input devicePlanningInput) plannerContribution {
	contribution := plannerContribution{Kind: upstreamDeviceKindLight, Role: plannerRolePrimary}
	expectations := make(map[string]int)
	for _, root := range input.Exposes.Roots(upstreamDeviceKindLight) {
		if root.resolved {
			expectations[colorModeProperty(root)]++
		}
	}
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
		mode := colorModePolicy{
			property: colorModeProperty(root),
			usable: expectations[colorModeProperty(root)] == 1 &&
				!input.Exposes.propertyHasForeignClaim(colorModeProperty(root)),
		}
		advertisedColor := hasColorComposite(root)
		temperature := planColorTemp(input, root, advertisedColor, mode)
		colorXY := planColorXY(input, root, mode)
		colorHS := planColorHS(input, root, mode)
		if temperature != nil {
			entities = append(entities, *temperature)
		}
		if colorXY != nil {
			entities = append(entities, *colorXY)
		}
		if colorHS != nil {
			entities = append(entities, *colorHS)
		}
		if modeEntity := planColorMode(
			input.IEEE, root, mode, temperature != nil || colorXY != nil || colorHS != nil,
		); modeEntity != nil {
			entities = append(entities, *modeEntity)
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
	return contribution
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

func planColorTemp(
	input devicePlanningInput,
	root indexedExpose,
	advertisedColor bool,
	mode colorModePolicy,
) *entityPlan {
	feature, ok := input.Exposes.UniqueFeature(
		root,
		featureQuery{Type: upstreamExposeNumeric, Name: upstreamColorTempName},
	)
	minimum, maximum, valid := colorTempRange(feature)
	if !ok || !valid || !input.Exposes.PropertyUnique(feature.Property) {
		return nil
	}
	// Temperature always needs a trustworthy companion mode property. A
	// temp-only root falls back to active only when the mode is absent; a
	// present mode (hs, xy, or malformed) must drive activity or raise a
	// decode issue. An ambiguous or colliding companion therefore omits
	// temperature instead of silently treating present modes as active.
	// An advertised but unplannable color composite keeps the root
	// mode-sensitive with no silent always-active fallback.
	if !mode.usable {
		return nil
	}
	requireMode := advertisedColor
	modeProperty := mode.property
	key, name := scopedIdentity("colortemp", "Color Temperature", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newColorTempPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/colortemp",
		Name:       name,
	}, feature.Property, modeProperty, requireMode, minimum, maximum)
	if err != nil {
		return nil
	}
	return &plan
}

// colorModeProperty resolves the Zigbee2MQTT companion mode property: the
// bare color_mode at root, or color_mode_<endpoint-label> for a scoped root
// using the exact upstream endpoint label rather than the numeric Hearth
// identity.
func colorModeProperty(root indexedExpose) string {
	if !root.scoped {
		return upstreamColorMode
	}
	return upstreamColorMode + "_" + root.expose.Endpoint
}

// hasColorComposite reports whether the root advertises any color composite,
// regardless of whether that composite is plannable.
func hasColorComposite(root indexedExpose) bool {
	for _, feature := range root.expose.Features {
		if feature.Type == upstreamExposeComposite && isColorCompositeName(feature.Name) {
			return true
		}
	}
	return false
}

// colorAxisSpec is one required numeric child of a color composite.
type colorAxisSpec struct {
	name    string
	maximum *big.Rat
}

func planColorXY(input devicePlanningInput, root indexedExpose, mode colorModePolicy) *entityPlan {
	if !mode.usable {
		return nil
	}
	feature, ok := input.Exposes.UniqueFeature(
		root,
		featureQuery{Type: upstreamExposeComposite, Name: upstreamColorXYName},
	)
	if !ok || !validColorComposite(input, root, feature, []colorAxisSpec{
		{name: "x", maximum: maxRawXY},
		{name: "y", maximum: maxRawXY},
	}) {
		return nil
	}
	key, name := scopedIdentity("colorxy", "Color XY", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newColorXYPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/colorxy",
		Name:       name,
	}, feature.Property, mode.property)
	if err != nil {
		return nil
	}
	return &plan
}

func planColorHS(input devicePlanningInput, root indexedExpose, mode colorModePolicy) *entityPlan {
	if !mode.usable {
		return nil
	}
	feature, ok := input.Exposes.UniqueFeature(
		root,
		featureQuery{Type: upstreamExposeComposite, Name: upstreamColorHSName},
	)
	if !ok || !validColorComposite(input, root, feature, []colorAxisSpec{
		{name: "hue", maximum: maxRawHue},
		{name: "saturation", maximum: maxRawSaturation},
	}) {
		return nil
	}
	key, name := scopedIdentity("colorhs", "Color Hue/Saturation", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newColorHSPlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: input.IEEE + "/" + entityLocation(root) + "/colorhs",
		Name:       name,
	}, feature.Property, mode.property)
	if err != nil {
		return nil
	}
	return &plan
}

// planColorMode discovers the read-only mode Entity for every eligible root
// with a usable color or temperature capability.
func planColorMode(ieee string, root indexedExpose, mode colorModePolicy, hasColorOrTemp bool) *entityPlan {
	if !hasColorOrTemp || !mode.usable {
		return nil
	}
	key, name := scopedIdentity("colormode", "Color Mode", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return nil
	}
	plan, err := newColorModePlan(adapter.EntityMetadata{
		Key:        key,
		ExternalID: ieee + "/" + entityLocation(root) + "/colormode",
		Name:       name,
	}, mode.property)
	if err != nil {
		return nil
	}
	return &plan
}

// validColorComposite validates one color candidate independently after
// ownership checks: a nonempty property, state/set/get access, exactly one
// numeric child per required coordinate with a matching child property and
// the same access, and optional bounds that must match the upstream domain.
// Extra unrelated children are harmless; duplicate required children
// disqualify.
func validColorComposite(
	input devicePlanningInput,
	root indexedExpose,
	feature upstreamExpose,
	axes []colorAxisSpec,
) bool {
	if feature.Property == "" || !exposeCanPublish(feature) || !exposeCanSet(feature) || !exposeCanGet(feature) ||
		!input.Exposes.colorPropertyShareable(root, feature) {
		return false
	}
	for _, axis := range axes {
		var match *upstreamExpose
		matches := 0
		for index := range feature.Features {
			if feature.Features[index].Name == axis.name {
				matches++
				match = &feature.Features[index]
			}
		}
		if matches != 1 || match.Type != upstreamExposeNumeric || match.Property != axis.name ||
			!exposeCanPublish(*match) || !exposeCanSet(*match) || !exposeCanGet(*match) ||
			!colorAxisBound(match.valueMinRaw, match.ValueMin, big.NewRat(0, 1)) ||
			!colorAxisBound(match.valueMaxRaw, match.ValueMax, axis.maximum) {
			return false
		}
	}
	return true
}
