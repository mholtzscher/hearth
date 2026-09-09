package zigbee2mqtt

import (
	"strconv"
	"unicode/utf8"
)

// devicePlanningInput shares one normalized inventory view with every profile.
// Vendor, Model, and SoftwareBuildID carry the definition evidence the
// profile evaluator uses to select exact vendor, model, and firmware
// overrides. None of these strings enters binding or entity identities.
type devicePlanningInput struct {
	IEEE            string
	Exposes         exposeIndex
	Vendor          string
	Model           string
	SoftwareBuildID string
}

// plannerRole declares whether one planner contribution competes as the
// primary contribution for Device kind or appends as a supplemental contribution.
// The zero value is invalid so a contribution that omits its role is rejected
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

// plannerContribution is one profile result for one IEEE address. A primary
// contribution competes to establish Device kind; a supplemental contribution
// always appends and establishes kind only when no primary contribution
// exists. Matched reports whether the profile selected at least one
// candidate root for evaluation, even when no candidate planned; planDevice
// uses it only to attribute an empty merge to the strongest primary family.
type plannerContribution struct {
	Kind     string
	Entities []entityPlan
	Role     plannerRole
	Matched  bool
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

// planDevice chooses one primary contribution and merges every supplemental
// contribution before one registration. The caller passes one contribution
// per profile in ascending profile order, produced by
// planProfileContributions. The first non-empty primary contribution wins;
// later primary contributions are discarded. Every supplemental contribution
// appends in order, and the first non-empty supplemental contribution
// establishes Device kind when no primary contribution exists. A
// contribution with an invalid role or an empty kind rejects the Device as
// invalid_descriptor before selection and merge.
func planDevice(contributions []plannerContribution) (devicePlan, error) {
	mergedInputs := make([]plannerContribution, 0, len(contributions))
	for _, contribution := range contributions {
		if !validPlannerContribution(contribution) {
			return devicePlan{}, &devicePlanError{code: rejectionInvalidDescriptor}
		}
		contribution.Entities = deduplicateKeys(contribution.Entities)
		mergedInputs = append(mergedInputs, contribution)
	}
	var primary *plannerContribution
	for index := range mergedInputs {
		if mergedInputs[index].Role == plannerRolePrimary && len(mergedInputs[index].Entities) != 0 {
			primary = &mergedInputs[index]
			break
		}
	}
	merged := make([]entityPlan, 0)
	kind := upstreamDeviceKindSensor
	if primary != nil {
		merged = append(merged, primary.Entities...)
		kind = primary.Kind
	} else {
		for index := range mergedInputs {
			if mergedInputs[index].Role == plannerRoleSupplemental && len(mergedInputs[index].Entities) != 0 {
				kind = mergedInputs[index].Kind
				break
			}
		}
	}
	for _, contribution := range mergedInputs {
		if contribution.Role == plannerRoleSupplemental {
			merged = append(merged, contribution.Entities...)
		}
	}
	if len(merged) == 0 {
		return devicePlan{}, &devicePlanError{code: noEligibleAttribution(mergedInputs)}
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
// contributions that omit their role, and the default case catches
// out-of-range roles; both reject as invalid_descriptor before selection and
// merge.
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
// planDevice applies it to each contribution before selection and merge.
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

// noEligibleAttribution attributes an empty merge to the first primary
// contribution in profile order that selected candidate roots. The compiler
// restricts primary contribution kinds, so the stable code derives from
// profile data without a mapping-specific root or family allowlist. A merge
// no primary family participated in rejects as no_eligible_entity.
func noEligibleAttribution(contributions []plannerContribution) string {
	for _, contribution := range contributions {
		if contribution.Role == plannerRolePrimary && contribution.Matched {
			return "no_eligible_" + contribution.Kind
		}
	}
	return rejectionNoEligibleEntity
}

// entityLocation names the external-ID path segment for one root.
func entityLocation(root indexedExpose) string {
	if root.scoped {
		return "ep" + strconv.Itoa(root.endpoint)
	}
	return "root"
}

func validDescriptorName(name string) bool {
	return name != "" && utf8.RuneCountInString(name) <= maximumDescriptorRunes
}
