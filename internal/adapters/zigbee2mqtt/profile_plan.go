package zigbee2mqtt

import "slices"

// This file evaluates compiled Zigbee2MQTT profile catalogs into one planner
// contribution per profile. Profiles evaluate in ascending order; candidate
// groups and roots evaluate in retained inventory order; device entities
// evaluate once after candidate groups. The evaluator owns override layering,
// exact root selection, root and feature and derived source resolution,
// requires_any sibling dependencies, gate-first sibling gating, duplicate
// scoped gate key removal, ungated sibling isolation, device entity group
// survivors, and per-profile contribution assembly. planDevice keeps
// same-contribution duplicate key removal, cross-contribution collision
// rejection, entity limits, and kind selection, and attributes an empty
// merge from each contribution's candidate-root evidence.
//
// A strategy that reports ineligible omits only its candidate. A malformed
// root or rule never suppresses valid siblings: every candidate resolves and
// plans independently except where gate or requires_any evidence is missing.

// profilePlanningInput builds the evaluator input for one upstream device:
// the normalized IEEE address, the shared expose index, and the definition
// vendor, model, and software build evidence used only to select exact
// vendor, model, and firmware overrides. None of these strings enters
// binding or entity identities.
func profilePlanningInput(device upstreamDevice, ieeeAddress string) devicePlanningInput {
	input := devicePlanningInput{
		IEEE:            ieeeAddress,
		Exposes:         newExposeIndex(device),
		SoftwareBuildID: string(device.SoftwareBuildID),
	}
	if device.Definition != nil {
		input.Vendor = device.Definition.Vendor
		input.Model = device.Definition.Model
	}
	return input
}

// planProfileContributions evaluates every catalog profile in ascending order
// and returns one planner contribution per profile for the existing
// deterministic planDevice merge and validation.
func planProfileContributions(
	catalog *ProfileCatalog,
	input devicePlanningInput,
) []plannerContribution {
	contributions := make([]plannerContribution, 0, len(catalog.profiles))
	for _, profile := range catalog.profiles {
		contributions = append(contributions,
			evaluatePlannerProfile(profile, input, catalog.overrides, catalog.strategies))
	}
	return contributions
}

// evaluatePlannerProfile evaluates one compiled planner profile: candidate
// groups in document order, then device entities whose required candidate
// group survived gate planning and duplicate gate key removal. Matched
// reports whether any candidate group selected a root for evaluation, even
// when no candidate planned, so planDevice can attribute an empty merge to
// the strongest primary family.
func evaluatePlannerProfile(
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) plannerContribution {
	contribution := plannerContribution{
		Kind: profile.document.Contribution.DeviceKind,
		Role: mapProfilePlannerRole(profile.document.Contribution.Role),
	}
	survivors := make(map[string]bool, len(profile.document.CandidateGroups))
	for _, group := range profile.document.CandidateGroups {
		if len(selectProfileRoots(input.Exposes, group.Root)) > 0 {
			contribution.Matched = true
		}
		plans, survived := evaluateProfileCandidateGroup(group, profile, input, overrides, strategies)
		contribution.Entities = append(contribution.Entities, plans...)
		survivors[group.ID] = survived
	}
	for _, rule := range profile.document.DeviceEntities {
		plan, planned := evaluateProfileDeviceEntity(rule, profile, input, overrides, strategies, survivors)
		if planned {
			contribution.Entities = append(contribution.Entities, plan)
		}
	}
	return contribution
}

// mapProfilePlannerRole maps one profile contribution role to the merge role.
// An unknown role maps to the invalid zero value so planDevice rejects the
// contribution as invalid_descriptor instead of silently competing.
func mapProfilePlannerRole(role profilePlannerRole) plannerRole {
	switch role {
	case profilePlannerRolePrimary:
		return plannerRolePrimary
	case profilePlannerRoleSupplemental:
		return plannerRoleSupplemental
	default:
		return plannerRoleInvalid
	}
}

// evaluateProfileCandidateGroup evaluates one candidate group over every
// retained root matching its exact type and optional exact name. The gate
// rule, when present, is the first entity rule: siblings for one root are
// evaluated only when the gate produces a valid plan for that root.
// Duplicate scoped gate keys drop every duplicate candidate with all its
// siblings, preserving light and relay ambiguity behavior. A group without
// a gate evaluates each rule independently so malformed roots or rules do
// not suppress valid siblings.
func evaluateProfileCandidateGroup(
	group profileCandidateGroup,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) ([]entityPlan, bool) {
	roots := selectProfileRoots(input.Exposes, group.Root)
	results := make([]profileRootResult, 0, len(roots))
	for _, root := range roots {
		results = append(results,
			evaluateProfileRootCandidates(group, profile, input, root, overrides, strategies))
	}
	if group.GateRule == "" {
		var plans []entityPlan
		for _, result := range results {
			plans = append(plans, result.plans...)
		}
		return plans, len(plans) > 0
	}
	return deduplicateProfileGateKeys(results)
}

// profileRootResult is one root's planned candidates with its gate
// outcome. Plans is empty when the gate produced no valid plan, because
// siblings are never evaluated for that root.
type profileRootResult struct {
	plans   []entityPlan
	gateKey string
	gated   bool
}

// evaluateProfileRootCandidates evaluates every rule for one root in
// document order. The gate rule, when present, is first: later siblings
// are evaluated only when the gate produces a valid plan for this root.
func evaluateProfileRootCandidates(
	group profileCandidateGroup,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	root indexedExpose,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) profileRootResult {
	planned := make(map[string]entityPlan)
	var candidates []entityPlan
	gatePlanned := group.GateRule == ""
	var gateKey string
	for ruleIndex, rule := range group.Entities {
		if ruleIndex > 0 && group.GateRule != "" && !gatePlanned {
			break
		}
		plan, ok := evaluateProfileCandidateRule(
			rule, profile, input, root, planned, overrides, strategies,
		)
		if !ok {
			continue
		}
		planned[rule.ID] = plan
		candidates = append(candidates, plan)
		if ruleIndex == 0 && group.GateRule != "" {
			gatePlanned = true
			gateKey = plan.Descriptor.Key
		}
	}
	return profileRootResult{plans: candidates, gateKey: gateKey, gated: gatePlanned}
}

// deduplicateProfileGateKeys drops every candidate sharing a duplicate
// scoped gate key with all its siblings, preserving light and relay
// ambiguity behavior. Ungated results never reach this path.
func deduplicateProfileGateKeys(results []profileRootResult) ([]entityPlan, bool) {
	counts := make(map[string]int, len(results))
	for _, result := range results {
		if result.gated {
			counts[result.gateKey]++
		}
	}
	var plans []entityPlan
	for _, result := range results {
		if !result.gated || counts[result.gateKey] != 1 {
			continue
		}
		plans = append(plans, result.plans...)
	}
	return plans, len(plans) > 0
}

// evaluateProfileCandidateRule evaluates one candidate rule for one root:
// override layering, requires_any sibling evidence, source resolution, and
// strategy planning. A disabled rule, a missing dependency, an unresolved
// source, or an ineligible strategy omits only this candidate.
func evaluateProfileCandidateRule(
	rule profileCandidateEntity,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	root indexedExpose,
	planned map[string]entityPlan,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) (entityPlan, bool) {
	effective, ok := overlayCandidateRule(rule, profile, input, overrides, strategies)
	if !ok || !effective.enabled {
		return entityPlan{}, false
	}
	if len(rule.RequiresAny) > 0 && !profileSiblingPlanned(rule.RequiresAny, planned) {
		return entityPlan{}, false
	}
	expose, ok := resolveProfileCandidateSource(input.Exposes, root, effective.source)
	if !ok {
		return entityPlan{}, false
	}
	strategyInput := profileStrategyInput{
		IEEE:     input.IEEE,
		Root:     root,
		Expose:   expose,
		Index:    input.Exposes,
		Identity: rule.Identity,
		Prior:    profilePriorPlans(rule.RequiresAny, planned),
	}
	return effective.definition.Plan(strategyInput, effective.parameters.Parameters)
}

// evaluateProfileDeviceEntity evaluates one device entity rule once after
// candidate groups. The rule is evaluated only when its required candidate
// group retained at least one candidate after gate planning and duplicate
// gate key removal. The expose match uses the existing UniqueRoot behavior,
// so a missing, duplicate, unresolved, or ineligible root omits only that
// device entity.
func evaluateProfileDeviceEntity(
	rule profileDeviceEntity,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
	survivors map[string]bool,
) (entityPlan, bool) {
	if !survivors[rule.RequiresGroupSurvivor] {
		return entityPlan{}, false
	}
	effective, ok := overlayDeviceRule(rule, profile, input, overrides, strategies)
	if !ok || !effective.enabled {
		return entityPlan{}, false
	}
	root, ok := input.Exposes.UniqueRoot(effective.expose.Type, effective.expose.Name)
	if !ok {
		return entityPlan{}, false
	}
	expose := root.expose
	strategyInput := profileStrategyInput{
		IEEE:     input.IEEE,
		Root:     root,
		Expose:   &expose,
		Index:    input.Exposes,
		Identity: rule.Identity,
	}
	return effective.definition.Plan(strategyInput, effective.parameters.Parameters)
}

// selectProfileRoots returns roots matching one exact expose type and optional
// exact name. The default retains every match in inventory order. Unique
// cardinality returns the match only when exactly one exists device-wide;
// duplicates are ambiguous even when one duplicate is unresolved or otherwise
// ineligible, preserving exact-one mappings without mapping-specific Go code.
func selectProfileRoots(index exposeIndex, selector profileCandidateRootSelector) []indexedExpose {
	var roots []indexedExpose
	for _, root := range index.roots {
		if root.expose.Type != selector.Type {
			continue
		}
		if selector.Name != "" && root.expose.Name != selector.Name {
			continue
		}
		roots = append(roots, root)
	}
	if selector.Cardinality == profileRootCardinalityUnique && len(roots) != 1 {
		return nil
	}
	return roots
}

// resolveProfileCandidateSource resolves one candidate source for one root:
// root passes the selected root expose, feature performs the exact-one
// UniqueFeature lookup inside the selected root, and derived passes no
// expose so the strategy plans from its declared prior sibling plans.
func resolveProfileCandidateSource(
	index exposeIndex,
	root indexedExpose,
	source profileEntitySource,
) (*upstreamExpose, bool) {
	switch source.Kind {
	case profileEntitySourceRoot:
		expose := root.expose
		return &expose, true
	case profileEntitySourceFeature:
		feature, ok := index.UniqueFeature(root, featureQuery{Type: source.Type, Name: source.Name})
		if !ok {
			return nil, false
		}
		return &feature, true
	case profileEntitySourceDerived:
		return nil, true
	default:
		return nil, false
	}
}

// profileSiblingPlanned reports whether at least one declared sibling
// dependency produced a successful plan for the same root.
func profileSiblingPlanned(dependencies []string, planned map[string]entityPlan) bool {
	for _, dependency := range dependencies {
		if _, ok := planned[dependency]; ok {
			return true
		}
	}
	return false
}

// profilePriorPlans carries only declared, successfully planned sibling
// dependencies to the strategy, never unrelated earlier plans.
func profilePriorPlans(dependencies []string, planned map[string]entityPlan) map[string]entityPlan {
	prior := make(map[string]entityPlan, len(dependencies))
	for _, dependency := range dependencies {
		if plan, ok := planned[dependency]; ok {
			prior[dependency] = plan
		}
	}
	return prior
}

// effectiveCandidateRule is one candidate rule after override layering: the
// base rule overlaid by at most one matching general vendor and model patch,
// then at most one matching exact build patch. Omitted patch fields retain
// the previous layer; a present source or strategy parameters value replaces
// that complete field.
type effectiveCandidateRule struct {
	enabled    bool
	source     profileEntitySource
	parameters compiledRuleParameters
	definition profileStrategyDefinition
}

// effectiveDeviceRule is one device entity rule after override layering,
// carrying the replacement expose match instead of a candidate source.
type effectiveDeviceRule struct {
	enabled    bool
	expose     profileExposeSelector
	parameters compiledRuleParameters
	definition profileStrategyDefinition
}

// overlaidRuleBase is the strategy, enablement, and parameters shared by
// candidate and device rules after override layering, plus the matched
// patches whose kind-specific replacement fields still apply.
type overlaidRuleBase struct {
	enabled    bool
	parameters compiledRuleParameters
	definition profileStrategyDefinition
	general    *compiledProfileOverridePatch
	exact      *compiledProfileOverridePatch
}

// overlayRuleBase layers the base rule enablement and strategy parameters
// with its general and exact build patches. It reports false when the rule
// strategy or base parameters are unknown, which catalog compilation
// already prevents.
func overlayRuleBase(
	ruleID, strategyName string,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) (overlaidRuleBase, bool) {
	definition, ok := strategies[strategyName]
	if !ok {
		return overlaidRuleBase{}, false
	}
	baseParameters, ok := profile.ruleParameters[ruleID]
	if !ok {
		return overlaidRuleBase{}, false
	}
	base := overlaidRuleBase{enabled: true, parameters: baseParameters, definition: definition}
	base.general, base.exact = matchProfileOverridePatches(ruleID, input, overrides)
	for _, patch := range []*compiledProfileOverridePatch{base.general, base.exact} {
		if patch == nil {
			continue
		}
		if patch.enabled != nil {
			base.enabled = *patch.enabled
		}
		if patch.parameters != nil {
			base.parameters = *patch.parameters
		}
	}
	return base, true
}

// overlayCandidateRule layers the base candidate rule with its general and
// exact build override patches for one device. A present source patch
// replaces the complete candidate source.
func overlayCandidateRule(
	rule profileCandidateEntity,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) (effectiveCandidateRule, bool) {
	base, ok := overlayRuleBase(rule.ID, rule.Strategy.Name, profile, input, overrides, strategies)
	if !ok {
		return effectiveCandidateRule{}, false
	}
	source := rule.Source
	for _, patch := range []*compiledProfileOverridePatch{base.general, base.exact} {
		if patch != nil && patch.source != nil {
			source = *patch.source
		}
	}
	return effectiveCandidateRule{
		enabled: base.enabled, source: source,
		parameters: base.parameters, definition: base.definition,
	}, true
}

// overlayDeviceRule layers the base device entity rule with its general
// and exact build override patches for one device. A present expose patch
// replaces the complete device expose match.
func overlayDeviceRule(
	rule profileDeviceEntity,
	profile compiledPlannerProfile,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
	strategies profileStrategyRegistry,
) (effectiveDeviceRule, bool) {
	base, ok := overlayRuleBase(rule.ID, rule.Strategy.Name, profile, input, overrides, strategies)
	if !ok {
		return effectiveDeviceRule{}, false
	}
	expose := rule.Expose
	for _, patch := range []*compiledProfileOverridePatch{base.general, base.exact} {
		if patch != nil && patch.expose != nil {
			expose = *patch.expose
		}
	}
	return effectiveDeviceRule{
		enabled: base.enabled, expose: expose,
		parameters: base.parameters, definition: base.definition,
	}, true
}

// matchProfileOverridePatches selects the general and exact build patches
// for one target rule and device: the general patch matches exact vendor
// and model without build IDs, and the exact patch additionally requires
// the device build in its exact build ID set. An empty device build never
// matches an exact patch. At most one patch of each layer matches because
// catalog compilation rejects overlapping selectors.
func matchProfileOverridePatches(
	ruleID string,
	input devicePlanningInput,
	overrides []compiledProfileOverride,
) (*compiledProfileOverridePatch, *compiledProfileOverridePatch) {
	var general, exact *compiledProfileOverridePatch
	for overrideIndex := range overrides {
		override := &overrides[overrideIndex]
		if override.document.Selector.Vendor != input.Vendor ||
			override.document.Selector.Model != input.Model {
			continue
		}
		general, exact = matchSelectorPatches(
			override, ruleID, input.SoftwareBuildID, general, exact)
	}
	return general, exact
}

// matchSelectorPatches folds one matching override document's patches for
// one target rule into the running general and exact layers. The general
// patch matches exact vendor and model without build IDs, and the exact
// patch additionally requires the device build in its exact build ID set.
// An empty device build never matches an exact patch. At most one patch
// of each layer matches because catalog compilation rejects overlapping
// selectors.
func matchSelectorPatches(
	override *compiledProfileOverride,
	ruleID, build string,
	general, exact *compiledProfileOverridePatch,
) (*compiledProfileOverridePatch, *compiledProfileOverridePatch) {
	for patchIndex := range override.patches {
		patch := &override.patches[patchIndex]
		if patch.targetRuleID != ruleID {
			continue
		}
		if len(override.document.Selector.SoftwareBuildIDs) == 0 {
			if general == nil {
				general = patch
			}
			continue
		}
		if build != "" && exact == nil &&
			slices.Contains(override.document.Selector.SoftwareBuildIDs, build) {
			exact = patch
		}
	}
	return general, exact
}
