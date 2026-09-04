package zigbee2mqtt

import (
	"strconv"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

const requiredAccessMask = 7

// devicePlanningInput shares one normalized inventory view with every planner.
type devicePlanningInput struct {
	Device  upstreamDevice
	IEEE    string
	Exposes exposeIndex
}

// plannerContribution is one planner family result for one IEEE address.
type plannerContribution struct {
	Kind     string
	Entities []entityPlan
}

// devicePlanner produces one family contribution without I/O and without
// retaining Device state. Implementations are immutable and concurrency-safe.
type devicePlanner interface {
	Plan(devicePlanningInput) (plannerContribution, error)
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

// defaultDevicePlanners returns the explicit planner assembly in deterministic
// order. All planners may inspect the same index.
func defaultDevicePlanners() []devicePlanner {
	return []devicePlanner{
		lightPlanner{},
		relayPlanner{},
		sensorPlanner{},
	}
}

// planDevice chooses one primary family and merges supplemental sensor plans
// before one registration. A non-empty light result wins; otherwise a
// non-empty relay result wins. The sensor result supplements either family and
// is primary only when neither actuator family contributes.
//
//nolint:gocognit // One function keeps primary-family precedence and merge validation together.
func planDevice(input devicePlanningInput, planners []devicePlanner) (devicePlan, error) {
	contributions := make([]plannerContribution, 0, len(planners))
	for _, planner := range planners {
		contribution, err := planner.Plan(input)
		if err != nil {
			return devicePlan{}, err
		}
		contribution.Entities = deduplicateKeys(contribution.Entities)
		contributions = append(contributions, contribution)
	}
	var primary *plannerContribution
	for index := range contributions {
		if contributions[index].Kind == upstreamDeviceKindLight && len(contributions[index].Entities) != 0 {
			primary = &contributions[index]
			break
		}
	}
	if primary == nil {
		for index := range contributions {
			if contributions[index].Kind == upstreamDeviceKindRelay && len(contributions[index].Entities) != 0 {
				primary = &contributions[index]
				break
			}
		}
	}
	merged := make([]entityPlan, 0)
	kind := upstreamDeviceKindSensor
	if primary != nil {
		merged = append(merged, primary.Entities...)
		kind = primary.Kind
	}
	for _, contribution := range contributions {
		if contribution.Kind == upstreamDeviceKindSensor {
			merged = append(merged, contribution.Entities...)
		}
	}
	if len(merged) == 0 {
		return devicePlan{}, &devicePlanError{code: noEligibleCode(input.Exposes)}
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

// deduplicateKeys removes every same-family Entity key that occurs more than
// once, preserving planner and expose order for the survivors.
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
