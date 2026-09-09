//nolint:unused // D1 defines the profile contract; dependent stack layers add the compiler and strategy consumers.
package zigbee2mqtt

import (
	"encoding/json"
	"fmt"
)

// This file defines the private Zigbee2MQTT profile wire contract and the
// compiled catalog forms owned by later deliverables. The JSON Schema at
// profiles/profile.schema.json is authoritative; these types show the required
// implementation shape for planner profiles, profile overrides, and the closed
// strategy registry. Profile evaluation stays private to this package: no
// profile internals are exported.

// profileDocumentKind discriminates one embedded profile document.
type profileDocumentKind string

const (
	// profileDocumentPlanner marks one entity mapping planner profile.
	profileDocumentPlanner profileDocumentKind = "planner-profile"
	// profileDocumentOverride marks one vendor/model/firmware rule patch document.
	profileDocumentOverride profileDocumentKind = "profile-override"
)

// profilePlannerRole declares whether one profile contribution competes as the
// primary contribution for device kind or appends as a supplemental
// contribution.
type profilePlannerRole string

const (
	// profilePlannerRolePrimary marks a primary contribution: the first
	// non-empty primary contribution wins device kind.
	profilePlannerRolePrimary profilePlannerRole = "primary"
	// profilePlannerRoleSupplemental marks a supplemental contribution: every
	// supplemental contribution appends in profile order.
	profilePlannerRoleSupplemental profilePlannerRole = "supplemental"
)

// profileRootCardinality selects how a candidate group resolves matching
// roots. The omitted zero value evaluates every retained matching root.
type profileRootCardinality string

const (
	// profileRootCardinalityUnique requires exactly one matching root across
	// the device, preserving mappings whose upstream identity is device-wide.
	profileRootCardinalityUnique profileRootCardinality = "unique"
)

// profileEntitySourceKind selects one closed entity source form.
type profileEntitySourceKind string

const (
	// profileEntitySourceRoot passes the selected root expose to the strategy.
	profileEntitySourceRoot profileEntitySourceKind = "root"
	// profileEntitySourceFeature looks up one exact nested feature inside the root.
	profileEntitySourceFeature profileEntitySourceKind = "feature"
	// profileEntitySourceDerived plans from prior sibling plans without an expose.
	profileEntitySourceDerived profileEntitySourceKind = "derived"
)

// Closed profile strategy names. Profiles select strategies by these exact
// names; unknown names fail catalog compilation.
const (
	profileStrategyBinaryPower             = "binary-power"
	profileStrategyBrightness              = "brightness"
	profileStrategyColorTemperature        = "color-temperature"
	profileStrategyColorXY                 = "color-xy"
	profileStrategyColorHS                 = "color-hs"
	profileStrategyColorMode               = "color-mode"
	profileStrategyStartupColorTemperature = "startup-color-temperature"
	profileStrategyTemperature             = "temperature"
	profileStrategyNumericSensor           = "numeric-sensor"
	profileStrategyNumericSetting          = "numeric-setting"
	profileStrategyEnumSetting             = "enum-setting"
	profileStrategyEnumAction              = "enum-action"
)

// plannerProfileDocument is one validated planner profile wire document.
type plannerProfileDocument struct {
	Version         int                     `json:"version"`
	Kind            profileDocumentKind     `json:"kind"`
	ID              string                  `json:"id"`
	Order           int                     `json:"order"`
	Contribution    profileContribution     `json:"contribution"`
	CandidateGroups []profileCandidateGroup `json:"candidate_groups"`
	DeviceEntities  []profileDeviceEntity   `json:"device_entities"`
}

// profileContribution states the device kind one planner profile establishes
// and whether it competes as primary or appends as supplemental.
type profileContribution struct {
	DeviceKind string             `json:"device_kind"`
	Role       profilePlannerRole `json:"role"`
}

// profileCandidateGroup evaluates roots in retained inventory order and plans
// one entity rule per eligible candidate.
type profileCandidateGroup struct {
	ID       string                       `json:"id"`
	Root     profileCandidateRootSelector `json:"root"`
	GateRule string                       `json:"gate_rule,omitempty"`
	Entities []profileCandidateEntity     `json:"entities"`
}

// profileCandidateEntity is one entity rule evaluated inside a candidate group.
type profileCandidateEntity struct {
	ID          string                `json:"id"`
	Source      profileEntitySource   `json:"source"`
	RequiresAny []string              `json:"requires_any,omitempty"`
	Identity    profileEntityIdentity `json:"identity"`
	Strategy    profileStrategyRef    `json:"strategy"`
}

// profileDeviceEntity is one device-level entity rule evaluated once after
// candidate groups, gated on a surviving candidate group.
type profileDeviceEntity struct {
	ID                    string                `json:"id"`
	Expose                profileExposeSelector `json:"expose"`
	RequiresGroupSurvivor string                `json:"requires_group_survivor"`
	Identity              profileEntityIdentity `json:"identity"`
	Strategy              profileStrategyRef    `json:"strategy"`
}

// profileCandidateRootSelector selects candidate roots by exact expose type
// and optional exact name. Cardinality omitted evaluates every retained match;
// unique requires exactly one device-wide match.
type profileCandidateRootSelector struct {
	Type        string                 `json:"type"`
	Name        string                 `json:"name,omitempty"`
	Cardinality profileRootCardinality `json:"cardinality,omitempty"`
}

// profileExposeSelector selects one device-level upstream root by exact expose
// type and optional exact name through the existing UniqueRoot behavior.
type profileExposeSelector struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

// profileEntitySource selects the upstream evidence one candidate rule plans
// from: the root expose, one exact nested feature, or derived sibling plans.
type profileEntitySource struct {
	Kind profileEntitySourceKind `json:"kind"`
	Type string                  `json:"type,omitempty"`
	Name string                  `json:"name,omitempty"`
}

// profileEntityIdentity carries the unscoped stable entity base key and its
// display name. Go applies scoped identity; profiles never build endpoint
// suffixes or external IDs.
type profileEntityIdentity struct {
	Key  string `json:"key"`
	Name string `json:"name"`
}

// profileStrategyRef names one closed registry strategy with its raw profile
// parameters. Parameters compile once at catalog load, never per device.
type profileStrategyRef struct {
	Name       string          `json:"name"`
	Parameters json.RawMessage `json:"parameters"`
}

// profileOverrideDocument is one validated vendor/model/firmware patch document.
type profileOverrideDocument struct {
	Version  int                   `json:"version"`
	Kind     profileDocumentKind   `json:"kind"`
	ID       string                `json:"id"`
	Selector profileDeviceSelector `json:"selector"`
	Patches  []profileRulePatch    `json:"patches"`
}

// profileDeviceSelector matches devices by exact vendor, model, and optional
// exact firmware build strings. These strings only select overrides and never
// enter binding or entity identities.
type profileDeviceSelector struct {
	Vendor           string   `json:"vendor"`
	Model            string   `json:"model"`
	SoftwareBuildIDs []string `json:"software_build_ids,omitempty"`
}

// profileRulePatch overlays one target rule. Omitted fields retain the previous
// layer; a present source, expose, or strategy_parameters value replaces that
// complete field.
type profileRulePatch struct {
	Rule               string                 `json:"rule"`
	Enabled            *bool                  `json:"enabled,omitempty"`
	Source             *profileEntitySource   `json:"source,omitempty"`
	Expose             *profileExposeSelector `json:"expose,omitempty"`
	StrategyParameters json.RawMessage        `json:"strategy_parameters,omitempty"`
}

// profileStrategyDefinition is one closed Go strategy. CompileParameters runs
// once at catalog compilation; Plan receives typed compiled parameters, never
// unvalidated JSON. Plan reports false when the upstream expose is ineligible,
// omitting only that candidate.
type profileStrategyDefinition struct {
	Name               string
	AllowedSourceKinds []profileEntitySourceKind
	CompileParameters  func(json.RawMessage) (any, error)
	Plan               func(profileStrategyInput, any) (entityPlan, bool)
}

// profileStrategyInput is the per-candidate evidence one strategy plans from.
// Prior holds only declared, successfully planned sibling dependencies.
type profileStrategyInput struct {
	IEEE     string
	Root     indexedExpose
	Expose   *upstreamExpose
	Index    exposeIndex
	Identity profileEntityIdentity
	Prior    map[string]entityPlan
}

// profileStrategyRegistry is the closed strategy set profiles may reference.
// The constructor arrives with the strategy deliverable.
type profileStrategyRegistry map[string]profileStrategyDefinition

// compiledRuleParameters pairs one rule with its compiled strategy parameters.
type compiledRuleParameters struct {
	RuleID     string
	Strategy   string
	Parameters any
}

// compiledPlannerProfile is one schema-validated planner profile with every
// strategy parameter compiled. The compiler deliverable owns construction.
type compiledPlannerProfile struct {
	document       plannerProfileDocument
	ruleParameters map[string]compiledRuleParameters
}

// compiledProfileOverride is one schema-validated override with every
// replacement parameter compiled. The compiler deliverable owns construction.
type compiledProfileOverride struct {
	document       profileOverrideDocument
	ruleParameters map[string]compiledRuleParameters
}

// ProfileCatalog is an immutable, validated catalog of embedded Zigbee2MQTT
// entity mapping profiles. Its zero value is invalid. The catalog loader
// deliverable owns construction; the adapter stores the compiled result.
type ProfileCatalog struct {
	profiles   []compiledPlannerProfile
	overrides  []compiledProfileOverride
	strategies profileStrategyRegistry
}

// Stable profile catalog error codes. A catalog conflict means two
// repository-authored declarations cannot compile deterministically; runtime
// device data is never a catalog conflict.
const (
	profileCatalogErrorSchemaInvalid            = "schema_invalid"
	profileCatalogErrorDocumentTooLarge         = "document_too_large"
	profileCatalogErrorCatalogLimitExceeded     = "catalog_limit_exceeded"
	profileCatalogErrorDuplicateDocumentID      = "duplicate_document_id"
	profileCatalogErrorDuplicateProfileOrder    = "duplicate_profile_order"
	profileCatalogErrorDuplicateGroupID         = "duplicate_group_id"
	profileCatalogErrorDuplicateRuleID          = "duplicate_rule_id"
	profileCatalogErrorDuplicateEntityKey       = "duplicate_entity_key"
	profileCatalogErrorInvalidContribution      = "invalid_contribution"
	profileCatalogErrorUnknownReference         = "unknown_reference"
	profileCatalogErrorInvalidGroupReference    = "invalid_group_reference"
	profileCatalogErrorInvalidDependencyOrder   = "invalid_dependency_order"
	profileCatalogErrorUnknownStrategy          = "unknown_strategy"
	profileCatalogErrorStrategySourceMismatch   = "strategy_source_mismatch"
	profileCatalogErrorStrategyParamsInvalid    = "strategy_parameters_invalid"
	profileCatalogErrorOverrideTargetUnknown    = "override_target_unknown"
	profileCatalogErrorOverrideTargetMismatch   = "override_target_kind_mismatch"
	profileCatalogErrorOverrideSelectorConflict = "override_selector_conflict"
)

// ProfileCatalogError is one deterministic catalog compilation failure with
// the document, profile, rule, and JSON pointer needed to locate it.
type ProfileCatalogError struct {
	Code        string
	Document    string
	ProfileID   string
	RuleID      string
	JSONPointer string
	Err         error
}

// Error reports one profile catalog failure without profile contents.
func (err *ProfileCatalogError) Error() string {
	return fmt.Sprintf("zigbee2mqtt profile catalog %s: document=%q profile=%q rule=%q pointer=%q: %v",
		err.Code, err.Document, err.ProfileID, err.RuleID, err.JSONPointer, err.Err)
}

// Unwrap returns the underlying catalog failure cause.
func (err *ProfileCatalogError) Unwrap() error { return err.Err }
