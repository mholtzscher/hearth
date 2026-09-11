package zigbee2mqtt

import (
	"strconv"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// devicePlanningInput shares one normalized inventory view with every planner.
type devicePlanningInput struct {
	IEEE    string
	Exposes exposeIndex
}

// plannerRole declares whether one planner contribution competes as the
// primary contribution for Device kind or appends as a supplemental contribution.
// The zero value is invalid so a planner that omits its role is rejected
// instead of silently competing as a primary.
type plannerRole int

const (
	// plannerRoleInvalid marks a contribution whose planner omitted its role.
	// planDevice rejects it as invalid_descriptor before selection and merge.
	plannerRoleInvalid plannerRole = iota
	// plannerRolePrimary marks a primary contribution: the first non-empty
	// primary contribution wins Device kind and later primaries are discarded.
	plannerRolePrimary
	// plannerRoleSupplemental marks a supplemental contribution: every
	// supplemental contribution appends in planner order, and the first
	// non-empty supplemental contribution establishes Device kind when no
	// primary contribution exists.
	plannerRoleSupplemental
)

// plannerContribution is one planner family result for one IEEE address. A
// primary contribution competes to establish Device kind; a supplemental
// contribution always appends and establishes kind only when no primary
// contribution exists.
type plannerContribution struct {
	Kind     string
	Entities []entityPlan
	Role     plannerRole
}

// devicePlan is the merged registration content for one normalized IEEE address.
type devicePlan struct {
	Kind     string
	Entities []entityPlan
}

// devicePlanError carries the stable device rejection code for a planning failure.
type devicePlanError struct {
	code string
}

func (err *devicePlanError) Error() string { return "Zigbee2MQTT Device plan rejected: " + err.code }

// planDevice calls the supported families directly in precedence order.
// All functions share the same expose index and perform no I/O.
func planDevice(input devicePlanningInput) (devicePlan, error) {
	return mergeDeviceContributions(input.Exposes, []plannerContribution{
		planLightFamily(input),
		planRelayFamily(input),
		planSensorFamily(input),
		planOccupancy(input),
		planLinkquality(input),
		planActionEvent(input),
	})
}

// mergeDeviceContributions chooses one primary contribution and merges every supplemental
// contribution before one registration. The first non-empty primary
// contribution wins; later primary contributions are discarded. Every
// supplemental contribution appends in planner order, and the first non-empty
// supplemental contribution establishes Device kind when no primary
// contribution exists. A contribution with an invalid role or an empty kind
// rejects the Device as invalid_descriptor before selection and merge.
func mergeDeviceContributions(exposes exposeIndex, contributions []plannerContribution) (devicePlan, error) {
	for index, contribution := range contributions {
		if !validPlannerContribution(contribution) {
			return devicePlan{}, &devicePlanError{code: rejectionInvalidDescriptor}
		}
		contributions[index].Entities = deduplicateKeys(contribution.Entities)
	}
	var primary *plannerContribution
	for index := range contributions {
		if contributions[index].Role == plannerRolePrimary && len(contributions[index].Entities) != 0 {
			primary = &contributions[index]
			break
		}
	}
	merged := make([]entityPlan, 0)
	kind := upstreamDeviceKindSensor
	if primary != nil {
		merged = append(merged, primary.Entities...)
		kind = primary.Kind
	} else {
		for index := range contributions {
			if contributions[index].Role == plannerRoleSupplemental && len(contributions[index].Entities) != 0 {
				kind = contributions[index].Kind
				break
			}
		}
	}
	for _, contribution := range contributions {
		if contribution.Role == plannerRoleSupplemental {
			merged = append(merged, contribution.Entities...)
		}
	}
	if len(merged) == 0 {
		return devicePlan{}, &devicePlanError{code: noEligibleCode(exposes)}
	}
	if len(merged) > maximumEntitiesPerDevice {
		return devicePlan{}, &devicePlanError{code: rejectionTooManyEntities}
	}
	if duplicateEntityKey(merged) {
		return devicePlan{}, &devicePlanError{code: rejectionAmbiguousEntityPlan}
	}
	if err := validateEntityPlans(merged); err != nil {
		return devicePlan{}, &devicePlanError{code: rejectionInvalidDescriptor}
	}
	return devicePlan{Kind: kind, Entities: merged}, nil
}

// validPlannerContribution reports whether a contribution declares a known
// role and a non-empty kind. The plannerRoleInvalid zero value catches
// planners that omit their role, and the default case catches out-of-range
// roles; both reject as invalid_descriptor before selection and merge.
func validPlannerContribution(contribution plannerContribution) bool {
	switch contribution.Role {
	case plannerRoleInvalid:
		return false
	case plannerRolePrimary, plannerRoleSupplemental:
		return contribution.Kind != ""
	default:
		return false
	}
}

// deduplicateKeys removes every same-contribution Entity key that occurs more
// than once, preserving planner and expose order for the survivors.
// mergeDeviceContributions applies it before selection and merge.
func deduplicateKeys(plans []entityPlan) []entityPlan {
	counts := make(map[string]int, len(plans))
	for _, plan := range plans {
		counts[plan.Descriptor.Key]++
	}
	kept := make([]entityPlan, 0, len(plans))
	for _, plan := range plans {
		if counts[plan.Descriptor.Key] == 1 {
			kept = append(kept, plan)
		}
	}
	return kept
}

func duplicateEntityKey(plans []entityPlan) bool {
	seen := make(map[string]struct{}, len(plans))
	for _, plan := range plans {
		if _, duplicate := seen[plan.Descriptor.Key]; duplicate {
			return true
		}
		seen[plan.Descriptor.Key] = struct{}{}
	}
	return false
}

// noEligibleCode attributes an empty merge to the strongest root family
// present so diagnostics distinguish light, relay, and sensor-only Devices.
func noEligibleCode(index exposeIndex) string {
	var hasLight, hasSwitch bool
	for _, root := range index.roots {
		switch root.expose.Type {
		case upstreamDeviceKindLight:
			hasLight = true
		case upstreamExposeSwitch:
			hasSwitch = true
		}
	}
	switch {
	case hasLight:
		return rejectionNoEligibleLight
	case hasSwitch:
		return rejectionNoEligibleRelay
	default:
		return rejectionNoEligibleEntity
	}
}

// entityLocation names the external-ID path segment for one root.
func entityLocation(root indexedExpose) string {
	if root.scoped {
		return "ep" + strconv.Itoa(root.endpoint)
	}
	return "root"
}

// planPowerEntity discovers the shared power Entity for one light or relay
// root. Light power and relay power share binary state eligibility, unique
// property ownership, on/off canonical distinction, scoped identity, and the
// shared power constructor; callers keep their own family gating and endpoint
// resolution behavior.
func planPowerEntity(input devicePlanningInput, root indexedExpose) (entityPlan, bool) {
	powerFeature, ok := input.Exposes.UniqueFeature(root, featureQuery{Type: upstreamExposeBinary, Name: "state"})
	if !ok || !validPowerFeature(powerFeature) || !input.Exposes.PropertyUnique(powerFeature.Property) {
		return entityPlan{}, false
	}
	powerOn, onErr := canonicalScalar(powerFeature.ValueOn)
	powerOff, offErr := canonicalScalar(powerFeature.ValueOff)
	if onErr != nil || offErr != nil || powerOn.canonical == powerOff.canonical {
		return entityPlan{}, false
	}
	metadata, ok := powerMetadata(input.IEEE, root)
	if !ok {
		return entityPlan{}, false
	}
	power, err := newPowerPlan(metadata, powerFeature.Property, powerOn, powerOff)
	if err != nil {
		return entityPlan{}, false
	}
	return power, true
}

// powerMetadata builds the shared power Entity identity for light and relay
// roots. A physical Device changing between a light and relay expose retains
// its power Entity identity when IEEE and numeric endpoint stay the same.
func powerMetadata(ieee string, root indexedExpose) (adapter.EntityMetadata, bool) {
	key, name := scopedIdentity("power", "Power", root.expose.Endpoint, root.endpoint, root.scoped)
	if !validDescriptorName(name) {
		return adapter.EntityMetadata{}, false
	}
	return adapter.EntityMetadata{
		Key:        key,
		ExternalID: ieee + "/" + entityLocation(root) + "/power",
		Name:       name,
	}, true
}

func validDescriptorName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maximumDescriptorRunes
}
