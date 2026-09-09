package zigbee2mqtt

import (
	"bytes"
	"fmt"
	"maps"
	"slices"
	"sort"
	"strconv"
)

// This file implements the semantic catalog compiler for Zigbee2MQTT JSON
// profiles. The JSON Schema is authoritative for document shapes; this
// compiler enforces every cross-field and cross-document rule the schema
// cannot express: aggregate limits, unique document and order and group and
// rule identifiers, contribution combinations, gate and dependency references,
// strategy resolution with compiled parameters, strategy and source
// compatibility, duplicate entity keys, and override target and selector
// overlap. The compiler works against one injected strategy registry so the
// transitional layer compiles the currently empty document set with an empty
// registry; the strategy deliverable switches the registry without changing
// this path. Any failure returns no partial catalog.

// profileCatalogCompiler carries catalog-wide uniqueness state across one
// compilation. Planner profiles compile before overrides so override targets
// resolve against the full rule set; each pass runs in lexical path order so
// diagnostics are deterministic. Planner precedence sorting happens only
// after every document succeeds.
// profileSelectorLayer tracks the general and exact-build patches already
// accepted for one vendor, model, and target rule so later patches conflict
// deterministically: at most one general patch, and exact-build patches must
// carry disjoint build-ID sets. Layers accumulate across override documents
// because the at-most-one rule applies per target rule, not per document.
type profileCatalogCompiler struct {
	strategies profileStrategyRegistry
	documents  map[string]string
	orders     map[int]string
	groups     map[string]profileGroupLocation
	rules      map[string]profileRuleLocation
	layers     map[profileSelectorLayerKey]*profileSelectorLayer
}

// profileGroupLocation names the first document that claimed one candidate
// group identifier.
type profileGroupLocation struct {
	document string
}

// profileRuleLocation names the first document that claimed one globally
// unique rule identifier and records the target kind and strategy an override
// patch must match.
type profileRuleLocation struct {
	document  string
	candidate bool
	strategy  string
}

// compileDecodedProfileCatalog semantically compiles every schema-validated
// document and returns an immutable catalog only if every document succeeds.
func compileDecodedProfileCatalog(
	decoded []decodedProfileDocument,
	strategies profileStrategyRegistry,
) (*ProfileCatalog, error) {
	compiler := &profileCatalogCompiler{
		strategies: strategies,
		documents:  make(map[string]string),
		orders:     make(map[int]string),
		groups:     make(map[string]profileGroupLocation),
		rules:      make(map[string]profileRuleLocation),
		layers:     make(map[profileSelectorLayerKey]*profileSelectorLayer),
	}
	if compiler.strategies == nil {
		compiler.strategies = profileStrategyRegistry{}
	}

	if err := compiler.checkTotalRuleLimit(decoded); err != nil {
		return nil, err
	}
	if err := compiler.checkUniqueDocumentIDs(decoded); err != nil {
		return nil, err
	}
	if err := compiler.checkUniqueProfileOrders(decoded); err != nil {
		return nil, err
	}
	if err := compiler.checkUniqueProfileStructureIDs(decoded); err != nil {
		return nil, err
	}

	var profiles []compiledPlannerProfile
	for _, document := range decoded {
		if document.planner == nil {
			continue
		}
		compiled, err := compiler.compilePlannerDocument(document.path, document.planner)
		if err != nil {
			return nil, err
		}
		profiles = append(profiles, compiled)
	}
	var overrides []compiledProfileOverride
	for _, document := range decoded {
		if document.override == nil {
			continue
		}
		compiled, err := compiler.compileOverrideDocument(document.path, document.override)
		if err != nil {
			return nil, err
		}
		overrides = append(overrides, compiled)
	}

	sort.Slice(profiles, func(i, j int) bool {
		return profiles[i].document.Order < profiles[j].document.Order
	})
	strategyCopy := make(profileStrategyRegistry, len(compiler.strategies))
	maps.Copy(strategyCopy, compiler.strategies)
	return &ProfileCatalog{
		profiles:   profiles,
		overrides:  overrides,
		strategies: strategyCopy,
		loaded:     true,
	}, nil
}

// checkTotalRuleLimit enforces the catalog-wide rule aggregate before any
// per-rule validation so an oversized catalog fails deterministically at the
// rule that overflows the limit.
func (compiler *profileCatalogCompiler) checkTotalRuleLimit(decoded []decodedProfileDocument) error {
	total := 0
	overflow := func(path, profileID, ruleID, pointer string) error {
		return profileCatalogFailure(profileCatalogErrorCatalogLimitExceeded, path, profileID, ruleID,
			pointer, "profile catalog exceeds %d total rules at rule %q", profileCatalogMaxTotalRules, ruleID)
	}
	for _, document := range decoded {
		if document.planner == nil {
			continue
		}
		planner := document.planner
		for groupIndex := range planner.CandidateGroups {
			group := &planner.CandidateGroups[groupIndex]
			for entityIndex := range group.Entities {
				total++
				if total > profileCatalogMaxTotalRules {
					rule := &group.Entities[entityIndex]
					return overflow(document.path, planner.ID, rule.ID,
						profileCandidateRulePointer(groupIndex, entityIndex)+"/id")
				}
			}
		}
		for deviceIndex := range planner.DeviceEntities {
			total++
			if total > profileCatalogMaxTotalRules {
				rule := &planner.DeviceEntities[deviceIndex]
				return overflow(document.path, planner.ID, rule.ID,
					profileDeviceRulePointer(deviceIndex)+"/id")
			}
		}
	}
	return nil
}

// checkUniqueDocumentIDs rejects a reused document identifier at the second
// document in lexical path order.
func (compiler *profileCatalogCompiler) checkUniqueDocumentIDs(decoded []decodedProfileDocument) error {
	for _, document := range decoded {
		id := decodedProfileDocumentID(document)
		if first, seen := compiler.documents[id]; seen {
			return profileCatalogFailure(profileCatalogErrorDuplicateDocumentID, document.path, id, "",
				"/id", "profile document ID %q first appears in %q", id, first)
		}
		compiler.documents[id] = document.path
	}
	return nil
}

// checkUniqueProfileOrders requires every planner order to be positive,
// bounded, and unique. The schema bounds orders to 1-1000; the compiler
// repeats the bound so direct document compilation fails with the same
// deterministic schema code.
func (compiler *profileCatalogCompiler) checkUniqueProfileOrders(decoded []decodedProfileDocument) error {
	for _, document := range decoded {
		if document.planner == nil {
			continue
		}
		planner := document.planner
		if planner.Order < 1 || planner.Order > 1000 {
			return profileCatalogFailure(profileCatalogErrorSchemaInvalid, document.path, planner.ID, "",
				"/order", "profile order %d is outside the bounds 1-1000", planner.Order)
		}
		if first, seen := compiler.orders[planner.Order]; seen {
			return profileCatalogFailure(profileCatalogErrorDuplicateProfileOrder, document.path, planner.ID, "",
				"/order", "profile order %d first appears in %q", planner.Order, first)
		}
		compiler.orders[planner.Order] = document.path
	}
	return nil
}

// checkUniqueProfileStructureIDs claims every candidate group and rule ID
// before semantic compilation so a reference or strategy failure in an early
// profile cannot mask a catalog-wide identity conflict in a later profile.
func (compiler *profileCatalogCompiler) checkUniqueProfileStructureIDs(
	decoded []decodedProfileDocument,
) error {
	for _, document := range decoded {
		if document.planner == nil {
			continue
		}
		planner := document.planner
		for groupIndex := range planner.CandidateGroups {
			group := &planner.CandidateGroups[groupIndex]
			groupPointer := profileCandidateGroupPointer(groupIndex)
			if first, seen := compiler.groups[group.ID]; seen {
				return profileCatalogFailure(profileCatalogErrorDuplicateGroupID,
					document.path, planner.ID, "", groupPointer+"/id",
					"candidate group ID %q first appears in %q", group.ID, first.document)
			}
			compiler.groups[group.ID] = profileGroupLocation{document: document.path}
			for entityIndex := range group.Entities {
				rule := &group.Entities[entityIndex]
				if err := compiler.claimProfileRuleID(document.path, planner.ID, rule.ID,
					profileCandidateRulePointer(groupIndex, entityIndex)+"/id", true, rule.Strategy.Name); err != nil {
					return err
				}
			}
		}
		for deviceIndex := range planner.DeviceEntities {
			rule := &planner.DeviceEntities[deviceIndex]
			if err := compiler.claimProfileRuleID(document.path, planner.ID, rule.ID,
				profileDeviceRulePointer(deviceIndex)+"/id", false, rule.Strategy.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// claimProfileRuleID records one globally unique candidate or device rule.
func (compiler *profileCatalogCompiler) claimProfileRuleID(
	path, profileID, ruleID, pointer string,
	candidate bool,
	strategy string,
) error {
	if first, seen := compiler.rules[ruleID]; seen {
		return profileCatalogFailure(profileCatalogErrorDuplicateRuleID,
			path, profileID, ruleID, pointer,
			"rule ID %q first appears in %q", ruleID, first.document)
	}
	compiler.rules[ruleID] = profileRuleLocation{
		document:  path,
		candidate: candidate,
		strategy:  strategy,
	}
	return nil
}

// compilePlannerDocument validates one planner profile and compiles every
// rule strategy parameter against the injected registry.
func (compiler *profileCatalogCompiler) compilePlannerDocument(
	path string,
	planner *plannerProfileDocument,
) (compiledPlannerProfile, error) {
	compiled := compiledPlannerProfile{
		document:       *planner,
		ruleParameters: make(map[string]compiledRuleParameters),
	}
	if err := validateProfileContribution(path, planner); err != nil {
		return compiledPlannerProfile{}, err
	}
	if len(planner.CandidateGroups) > profileCatalogMaxGroupsPerProfile {
		return compiledPlannerProfile{}, profileCatalogFailure(profileCatalogErrorCatalogLimitExceeded,
			path, planner.ID, "", "/candidate_groups",
			"profile %q exceeds %d candidate groups", planner.ID, profileCatalogMaxGroupsPerProfile)
	}
	if len(planner.DeviceEntities) > profileCatalogMaxDeviceRules {
		return compiledPlannerProfile{}, profileCatalogFailure(profileCatalogErrorCatalogLimitExceeded,
			path, planner.ID, "", "/device_entities",
			"profile %q exceeds %d device entities", planner.ID, profileCatalogMaxDeviceRules)
	}

	entityKeys := make(map[string]string)
	for groupIndex := range planner.CandidateGroups {
		group := &planner.CandidateGroups[groupIndex]
		groupPointer := profileCandidateGroupPointer(groupIndex)
		if group.Root.Cardinality != "" && group.Root.Cardinality != profileRootCardinalityUnique {
			return compiledPlannerProfile{}, profileCatalogFailure(profileCatalogErrorSchemaInvalid,
				path, planner.ID, "", groupPointer+"/root/cardinality",
				"candidate group %q has unknown root cardinality %q", group.ID, group.Root.Cardinality)
		}
		if len(group.Entities) > profileCatalogMaxRulesPerGroup {
			return compiledPlannerProfile{}, profileCatalogFailure(profileCatalogErrorCatalogLimitExceeded,
				path, planner.ID, "", groupPointer+"/entities",
				"candidate group %q exceeds %d rules", group.ID, profileCatalogMaxRulesPerGroup)
		}
		if err := compiler.compileCandidateGroup(path, planner, groupIndex, &compiled, entityKeys); err != nil {
			return compiledPlannerProfile{}, err
		}
	}
	for deviceIndex := range planner.DeviceEntities {
		if err := compiler.compileDeviceEntity(path, planner, deviceIndex, &compiled, entityKeys); err != nil {
			return compiledPlannerProfile{}, err
		}
	}
	return compiled, nil
}

// compileCandidateGroup validates gate and dependency references, resolves
// every rule strategy, and rejects duplicate base entity keys.
func (compiler *profileCatalogCompiler) compileCandidateGroup(
	path string,
	planner *plannerProfileDocument,
	groupIndex int,
	compiled *compiledPlannerProfile,
	entityKeys map[string]string,
) error {
	group := &planner.CandidateGroups[groupIndex]
	indexByRule := candidateRuleIndexes(group)
	if gateErr := validateCandidateGate(path, planner, groupIndex); gateErr != nil {
		return gateErr
	}
	for entityIndex := range group.Entities {
		if ruleErr := compiler.compileCandidateRule(
			path,
			planner,
			groupIndex,
			entityIndex,
			indexByRule,
			compiled,
			entityKeys,
		); ruleErr != nil {
			return ruleErr
		}
	}
	return nil
}

// candidateRuleIndexes returns sibling rule positions for dependency
// validation. Catalog-wide rule IDs were claimed in the structural preflight.
func candidateRuleIndexes(group *profileCandidateGroup) map[string]int {
	indexByRule := make(map[string]int, len(group.Entities))
	for entityIndex := range group.Entities {
		indexByRule[group.Entities[entityIndex].ID] = entityIndex
	}
	return indexByRule
}

// validateCandidateGate requires the group gate to name the first entity rule.
func validateCandidateGate(path string, planner *plannerProfileDocument, groupIndex int) error {
	group := &planner.CandidateGroups[groupIndex]
	if group.GateRule == "" {
		return nil
	}
	gatePointer := profileCandidateGroupPointer(groupIndex) + "/gate_rule"
	for entityIndex := range group.Entities {
		if group.Entities[entityIndex].ID == group.GateRule {
			if entityIndex != 0 {
				return profileCatalogFailure(profileCatalogErrorInvalidDependencyOrder,
					path, planner.ID, group.GateRule, gatePointer,
					"candidate group %q gate rule %q must be the first entity rule", group.ID, group.GateRule)
			}
			return nil
		}
	}
	return profileCatalogFailure(profileCatalogErrorUnknownReference,
		path, planner.ID, group.GateRule, gatePointer,
		"candidate group %q gates on unknown rule %q", group.ID, group.GateRule)
}

// compileCandidateRule validates sibling dependencies, resolves the rule
// strategy with compiled parameters, and rejects duplicate base entity keys.
func (compiler *profileCatalogCompiler) compileCandidateRule(
	path string,
	planner *plannerProfileDocument,
	groupIndex, entityIndex int,
	indexByRule map[string]int,
	compiled *compiledPlannerProfile,
	entityKeys map[string]string,
) error {
	rule := &planner.CandidateGroups[groupIndex].Entities[entityIndex]
	rulePointer := profileCandidateRulePointer(groupIndex, entityIndex)
	if err := validateCandidateDependencies(path, planner, rule, rulePointer, entityIndex, indexByRule); err != nil {
		return err
	}
	definition, resolveErr := compiler.resolveProfileStrategy(
		path,
		planner.ID,
		rule.ID,
		rulePointer+"/strategy/name",
		rule.Strategy.Name,
	)
	if resolveErr != nil {
		return resolveErr
	}
	sourcePointer := rulePointer + "/source"
	if sourceErr := validateProfileEntitySource(
		path,
		planner.ID,
		rule.ID,
		sourcePointer,
		rule.Source,
		definition.AllowedSourceKinds,
	); sourceErr != nil {
		return sourceErr
	}
	parameters, paramsErr := compileProfileStrategyParameters(
		path,
		planner.ID,
		rule.ID,
		rulePointer+"/strategy/parameters",
		rule.Strategy,
		definition,
	)
	if paramsErr != nil {
		return paramsErr
	}
	compiled.ruleParameters[rule.ID] = parameters
	if first, seen := entityKeys[rule.Identity.Key]; seen {
		return profileCatalogFailure(profileCatalogErrorDuplicateEntityKey,
			path, planner.ID, rule.ID, rulePointer+"/identity/key",
			"entity key %q first appears in rule %q", rule.Identity.Key, first)
	}
	entityKeys[rule.Identity.Key] = rule.ID
	return nil
}

// validateCandidateDependencies requires every sibling dependency to name an
// earlier rule in the same candidate group.
func validateCandidateDependencies(
	path string,
	planner *plannerProfileDocument,
	rule *profileCandidateEntity,
	rulePointer string,
	entityIndex int,
	indexByRule map[string]int,
) error {
	for dependencyIndex, dependency := range rule.RequiresAny {
		dependencyPointer := rulePointer + "/requires_any/" + strconv.Itoa(dependencyIndex)
		dependencyPosition, ok := indexByRule[dependency]
		if !ok {
			return profileCatalogFailure(profileCatalogErrorUnknownReference,
				path, planner.ID, rule.ID, dependencyPointer,
				"rule %q requires unknown sibling rule %q", rule.ID, dependency)
		}
		if dependencyPosition >= entityIndex {
			return profileCatalogFailure(profileCatalogErrorInvalidDependencyOrder,
				path, planner.ID, rule.ID, dependencyPointer,
				"rule %q must require an earlier sibling rule, not %q", rule.ID, dependency)
		}
	}
	return nil
}

// compileDeviceEntity validates the group-survivor reference, resolves the
// rule strategy, and rejects duplicate base entity keys.
func (compiler *profileCatalogCompiler) compileDeviceEntity(
	path string,
	planner *plannerProfileDocument,
	deviceIndex int,
	compiled *compiledPlannerProfile,
	entityKeys map[string]string,
) error {
	rule := &planner.DeviceEntities[deviceIndex]
	rulePointer := profileDeviceRulePointer(deviceIndex)

	survivorPointer := rulePointer + "/requires_group_survivor"
	var survivor *profileCandidateGroup
	for groupIndex := range planner.CandidateGroups {
		if planner.CandidateGroups[groupIndex].ID == rule.RequiresGroupSurvivor {
			survivor = &planner.CandidateGroups[groupIndex]
			break
		}
	}
	if survivor == nil {
		return profileCatalogFailure(profileCatalogErrorInvalidGroupReference,
			path, planner.ID, rule.ID, survivorPointer,
			"device rule %q requires unknown candidate group %q", rule.ID, rule.RequiresGroupSurvivor)
	}
	if survivor.GateRule == "" {
		return profileCatalogFailure(profileCatalogErrorInvalidGroupReference,
			path, planner.ID, rule.ID, survivorPointer,
			"device rule %q requires group %q without a gate rule", rule.ID, survivor.ID)
	}

	definition, err := compiler.resolveProfileStrategy(
		path,
		planner.ID,
		rule.ID,
		rulePointer+"/strategy/name",
		rule.Strategy.Name,
	)
	if err != nil {
		return err
	}
	if !slices.Contains(definition.AllowedSourceKinds, profileEntitySourceRoot) {
		return profileCatalogFailure(profileCatalogErrorStrategySourceMismatch,
			path, planner.ID, rule.ID, rulePointer+"/expose",
			"device rule %q uses a root expose its strategy does not accept", rule.ID)
	}
	parameters, err := compileProfileStrategyParameters(
		path,
		planner.ID,
		rule.ID,
		rulePointer+"/strategy/parameters",
		rule.Strategy,
		definition,
	)
	if err != nil {
		return err
	}
	compiled.ruleParameters[rule.ID] = parameters
	if first, seen := entityKeys[rule.Identity.Key]; seen {
		return profileCatalogFailure(profileCatalogErrorDuplicateEntityKey,
			path, planner.ID, rule.ID, rulePointer+"/identity/key",
			"entity key %q first appears in rule %q", rule.Identity.Key, first)
	}
	entityKeys[rule.Identity.Key] = rule.ID
	return nil
}

// compileOverrideDocument validates every patch target, target-rule kind,
// layered selector overlap, and replacement field before storing the compiled
// patch layers.
func (compiler *profileCatalogCompiler) compileOverrideDocument(
	path string,
	override *profileOverrideDocument,
) (compiledProfileOverride, error) {
	compiled := compiledProfileOverride{document: *override}
	if len(override.Patches) > profileCatalogMaxPatchesPerOverride {
		return compiledProfileOverride{}, profileCatalogFailure(profileCatalogErrorCatalogLimitExceeded,
			path, override.ID, "", "/patches",
			"override %q exceeds %d patches", override.ID, profileCatalogMaxPatchesPerOverride)
	}
	for patchIndex := range override.Patches {
		patchPointer := "/patches/" + strconv.Itoa(patchIndex)
		compiledPatch, err := compiler.compileOverridePatch(path, override, patchIndex, patchPointer)
		if err != nil {
			return compiledProfileOverride{}, err
		}
		compiled.patches = append(compiled.patches, compiledPatch)
	}
	return compiled, nil
}

// compileOverridePatch validates one patch target, target-rule kind, layered
// selector overlap, and replacement field before storing the compiled layer.
func (compiler *profileCatalogCompiler) compileOverridePatch(
	path string,
	override *profileOverrideDocument,
	patchIndex int,
	patchPointer string,
) (compiledProfileOverridePatch, error) {
	patch := &override.Patches[patchIndex]
	target, ok := compiler.rules[patch.Rule]
	if !ok {
		return compiledProfileOverridePatch{}, profileCatalogFailure(profileCatalogErrorOverrideTargetUnknown,
			path, override.ID, patch.Rule, patchPointer+"/rule",
			"override %q targets unknown rule %q", override.ID, patch.Rule)
	}
	if err := validateOverridePatchKind(path, override, patch, patchPointer, target); err != nil {
		return compiledProfileOverridePatch{}, err
	}
	definition, resolveErr := compiler.resolveProfileStrategy(
		path,
		override.ID,
		patch.Rule,
		patchPointer+"/strategy_parameters",
		target.strategy,
	)
	if resolveErr != nil {
		return compiledProfileOverridePatch{}, resolveErr
	}
	compiledPatch := compiledProfileOverridePatch{
		targetRuleID:     patch.Rule,
		targetDeviceRule: !target.candidate,
	}
	if patch.Enabled != nil {
		enabled := *patch.Enabled
		compiledPatch.enabled = &enabled
	}
	if patch.Source != nil {
		sourcePointer := patchPointer + "/source"
		if sourceErr := validateProfileEntitySource(
			path,
			override.ID,
			patch.Rule,
			sourcePointer,
			*patch.Source,
			definition.AllowedSourceKinds,
		); sourceErr != nil {
			return compiledProfileOverridePatch{}, sourceErr
		}
		source := *patch.Source
		compiledPatch.source = &source
	}
	if patch.Expose != nil {
		expose := *patch.Expose
		compiledPatch.expose = &expose
	}
	if len(bytes.TrimSpace(patch.StrategyParameters)) > 0 {
		parameters, paramsErr := compileProfileStrategyParameters(path, override.ID, patch.Rule,
			patchPointer+"/strategy_parameters",
			profileStrategyRef{Name: target.strategy, Parameters: patch.StrategyParameters},
			definition)
		if paramsErr != nil {
			return compiledProfileOverridePatch{}, paramsErr
		}
		compiledPatch.parameters = &parameters
	}
	layerKey := profileSelectorLayerKey{
		vendor: override.Selector.Vendor,
		model:  override.Selector.Model,
		ruleID: patch.Rule,
	}
	layer := compiler.layers[layerKey]
	if layer == nil {
		layer = &profileSelectorLayer{}
		compiler.layers[layerKey] = layer
	}
	if layerErr := layer.checkPatch(path, override.ID, patch.Rule, patchPointer, override.Selector); layerErr != nil {
		return compiledProfileOverridePatch{}, layerErr
	}
	return compiledPatch, nil
}

// validateOverridePatchKind matches the replacement field to the target rule
// kind: candidate-entity patches replace source, device-entity patches
// replace expose, and no patch replaces both.
func validateOverridePatchKind(
	path string,
	override *profileOverrideDocument,
	patch *profileRulePatch,
	patchPointer string,
	target profileRuleLocation,
) error {
	if patch.Source != nil && patch.Expose != nil {
		return profileCatalogFailure(profileCatalogErrorOverrideTargetMismatch,
			path, override.ID, patch.Rule, patchPointer+"/source",
			"override %q replaces both source and expose on rule %q", override.ID, patch.Rule)
	}
	if patch.Source != nil && !target.candidate {
		return profileCatalogFailure(profileCatalogErrorOverrideTargetMismatch,
			path, override.ID, patch.Rule, patchPointer+"/source",
			"override %q replaces source on device rule %q", override.ID, patch.Rule)
	}
	if patch.Expose != nil && target.candidate {
		return profileCatalogFailure(profileCatalogErrorOverrideTargetMismatch,
			path, override.ID, patch.Rule, patchPointer+"/expose",
			"override %q replaces expose on candidate rule %q", override.ID, patch.Rule)
	}
	return nil
}

// profileSelectorLayerKey identifies one vendor, model, and target rule
// without delimiter-based string collisions.
type profileSelectorLayerKey struct {
	vendor string
	model  string
	ruleID string
}

// profileSelectorLayer is the accumulated general and exact-build patch
// state for one vendor, model, and target rule.
type profileSelectorLayer struct {
	general     bool
	generalPath string
	exact       []profileExactBuildPatch
}

// profileExactBuildPatch records one accepted exact-build patch for overlap
// comparison.
type profileExactBuildPatch struct {
	path   string
	builds map[string]struct{}
}

// checkPatch records one patch selector or reports the layered selector
// conflict it creates with an earlier patch for the same target.
func (layer *profileSelectorLayer) checkPatch(
	path, overrideID, ruleID, patchPointer string,
	selector profileDeviceSelector,
) error {
	if len(selector.SoftwareBuildIDs) == 0 {
		if layer.general {
			return profileCatalogFailure(profileCatalogErrorOverrideSelectorConflict,
				path, overrideID, ruleID, patchPointer,
				"override %q duplicates the general patch for rule %q from %q", overrideID, ruleID, layer.generalPath)
		}
		layer.general = true
		layer.generalPath = path
		return nil
	}
	sortedBuilds := make([]string, 0, len(selector.SoftwareBuildIDs))
	builds := make(map[string]struct{}, len(selector.SoftwareBuildIDs))
	for _, build := range selector.SoftwareBuildIDs {
		if _, seen := builds[build]; seen {
			continue
		}
		builds[build] = struct{}{}
		sortedBuilds = append(sortedBuilds, build)
	}
	sort.Strings(sortedBuilds)
	for _, prior := range layer.exact {
		for _, build := range sortedBuilds {
			if _, overlaps := prior.builds[build]; overlaps {
				return profileCatalogFailure(profileCatalogErrorOverrideSelectorConflict,
					path, overrideID, ruleID, patchPointer,
					"override %q reuses build ID %q for rule %q from %q", overrideID, build, ruleID, prior.path)
			}
		}
	}
	layer.exact = append(layer.exact, profileExactBuildPatch{path: path, builds: builds})
	return nil
}

// resolveProfileStrategy resolves one strategy name against the injected
// registry. Unknown names fail compilation; the schema already rejects names
// outside the closed contract for embedded documents.
func (compiler *profileCatalogCompiler) resolveProfileStrategy(
	document, profileID, ruleID, pointer, name string,
) (profileStrategyDefinition, error) {
	definition, ok := compiler.strategies[name]
	if !ok {
		return profileStrategyDefinition{}, profileCatalogFailure(profileCatalogErrorUnknownStrategy,
			document, profileID, ruleID, pointer,
			"rule %q references unknown strategy %q", ruleID, name)
	}
	return definition, nil
}

// validateProfileEntitySource enforces the closed root, feature, and derived
// source forms and the target strategy source compatibility.
func validateProfileEntitySource(
	document, profileID, ruleID, pointer string,
	source profileEntitySource,
	allowed []profileEntitySourceKind,
) error {
	closedForm := false
	switch source.Kind {
	case profileEntitySourceRoot:
		closedForm = source.Type == "" && source.Name == ""
	case profileEntitySourceFeature:
		closedForm = source.Type != "" && source.Name != ""
	case profileEntitySourceDerived:
		closedForm = source.Type == "" && source.Name == "color-mode"
	}
	if !closedForm {
		return profileCatalogFailure(profileCatalogErrorStrategySourceMismatch,
			document, profileID, ruleID, pointer,
			"rule %q uses source kind %q outside its closed form", ruleID, string(source.Kind))
	}
	if slices.Contains(allowed, source.Kind) {
		return nil
	}
	return profileCatalogFailure(profileCatalogErrorStrategySourceMismatch,
		document, profileID, ruleID, pointer,
		"rule %q uses source kind %q its strategy does not accept", ruleID, string(source.Kind))
}

// compileProfileStrategyParameters requires a parameter object and compiles it
// once through the target strategy. A compile failure names the target rule
// and the replacement pointer so override patches report the same evidence.
func compileProfileStrategyParameters(
	document, profileID, ruleID, pointer string,
	ref profileStrategyRef,
	definition profileStrategyDefinition,
) (compiledRuleParameters, error) {
	trimmed := bytes.TrimSpace(ref.Parameters)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return compiledRuleParameters{}, profileCatalogFailure(profileCatalogErrorStrategyParamsInvalid,
			document, profileID, ruleID, pointer,
			"rule %q requires a strategy parameter object for strategy %q", ruleID, ref.Name)
	}
	if definition.CompileParameters == nil {
		return compiledRuleParameters{}, profileCatalogFailure(profileCatalogErrorStrategyParamsInvalid,
			document, profileID, ruleID, pointer,
			"strategy %q has no parameter compiler for rule %q", ref.Name, ruleID)
	}
	parameters, err := definition.CompileParameters(ref.Parameters)
	if err != nil {
		return compiledRuleParameters{}, profileCatalogFailure(profileCatalogErrorStrategyParamsInvalid,
			document, profileID, ruleID, pointer,
			"rule %q has invalid parameters for strategy %q: %v", ruleID, ref.Name, err)
	}
	return compiledRuleParameters{RuleID: ruleID, Strategy: ref.Name, Parameters: parameters}, nil
}

// validateProfileContribution requires a known device kind and planner role.
// The schema enumerates both; the compiler repeats the check so direct
// document compilation fails with the contribution code.
func validateProfileContribution(path string, planner *plannerProfileDocument) error {
	switch planner.Contribution.DeviceKind {
	case "light", "relay", "sensor":
	default:
		return profileCatalogFailure(profileCatalogErrorInvalidContribution,
			path, planner.ID, "", "/contribution/device_kind",
			"profile %q has unknown device kind %q", planner.ID, planner.Contribution.DeviceKind)
	}
	switch planner.Contribution.Role {
	case profilePlannerRolePrimary, profilePlannerRoleSupplemental:
	default:
		return profileCatalogFailure(profileCatalogErrorInvalidContribution,
			path, planner.ID, "", "/contribution/role",
			"profile %q has unknown planner role %q", planner.ID, string(planner.Contribution.Role))
	}
	validCombination := planner.Contribution.Role == profilePlannerRolePrimary &&
		(planner.Contribution.DeviceKind == "light" || planner.Contribution.DeviceKind == "relay")
	validCombination = validCombination || planner.Contribution.Role == profilePlannerRoleSupplemental &&
		planner.Contribution.DeviceKind == "sensor"
	if !validCombination {
		return profileCatalogFailure(profileCatalogErrorInvalidContribution,
			path, planner.ID, "", "/contribution",
			"profile %q has invalid device kind %q and role %q combination",
			planner.ID, planner.Contribution.DeviceKind, planner.Contribution.Role)
	}
	return nil
}

// decodedProfileDocumentID reports the document identifier regardless of kind.
func decodedProfileDocumentID(document decodedProfileDocument) string {
	if document.planner != nil {
		return document.planner.ID
	}
	if document.override != nil {
		return document.override.ID
	}
	return ""
}

// profileCatalogFailure builds one deterministic catalog error without profile
// contents: only identifiers, paths, pointers, and counts enter the message.
func profileCatalogFailure(
	code, document, profileID, ruleID, pointer, format string,
	args ...any,
) *ProfileCatalogError {
	return &ProfileCatalogError{
		Code:        code,
		Document:    document,
		ProfileID:   profileID,
		RuleID:      ruleID,
		JSONPointer: pointer,
		Err:         fmt.Errorf("zigbee2mqtt "+format, args...),
	}
}

// profileCandidateGroupPointer names one candidate group by position.
func profileCandidateGroupPointer(groupIndex int) string {
	return "/candidate_groups/" + strconv.Itoa(groupIndex)
}

// profileCandidateRulePointer names one candidate rule by position.
func profileCandidateRulePointer(groupIndex, entityIndex int) string {
	return profileCandidateGroupPointer(groupIndex) + "/entities/" + strconv.Itoa(entityIndex)
}

// profileDeviceRulePointer names one device rule by position.
func profileDeviceRulePointer(deviceIndex int) string {
	return "/device_entities/" + strconv.Itoa(deviceIndex)
}
