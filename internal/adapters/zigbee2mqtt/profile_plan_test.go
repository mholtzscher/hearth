package zigbee2mqtt //nolint:testpackage // Evaluator tests exercise package-private planning and catalog compilation.

// This test protects the D4 shared profile evaluator and fails if root
// order, source resolution, gate-first sibling gating, duplicate gate key
// removal, requires_any evidence, device entity group survivors, override
// layering, contribution assembly, or malformed candidate isolation diverge
// from the approved zigbee2mqtt-json-profile-catalog spec. The oracle is the
// spec planning semantics; each case names the plausible defect it guards.
// Every catalog here is an in-memory document set compiled with the
// production strategy registry.

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
)

// evalTestCatalog compiles one in-memory profile document set with the
// production strategy registry. A compilation failure is a test defect, not
// evaluator evidence.
func evalTestCatalog(t *testing.T, docs map[string]string) *ProfileCatalog {
	t.Helper()
	files := make([]profileCatalogFile, 0, len(docs))
	for path, body := range docs {
		files = append(files, profileCatalogFile{path: path, data: []byte(body)})
	}
	catalog, err := compileProfileCatalogFiles(files, defaultProfileStrategyRegistry())
	if err != nil {
		t.Fatalf("in-memory profile catalog failed to compile: %v", err)
	}
	return catalog
}

// evalTestContributions evaluates every catalog profile for one device.
func evalTestContributions(catalog *ProfileCatalog, device upstreamDevice) []plannerContribution {
	return planProfileContributions(catalog, profilePlanningInput(device, device.IEEEAddress))
}

// evalTestProfile finds one compiled profile by document ID.
func evalTestProfile(t *testing.T, catalog *ProfileCatalog, id string) compiledPlannerProfile {
	t.Helper()
	for _, profile := range catalog.profiles {
		if profile.document.ID == id {
			return profile
		}
	}
	t.Fatalf("profile %q not found in catalog", id)
	return compiledPlannerProfile{}
}

// evalTestRuleJSON builds one candidate rule fragment.
func evalTestRuleJSON(
	ruleID, sourceJSON, strategy, params, key, name string,
	requiresAny ...string,
) string {
	dependencies := ""
	if len(requiresAny) > 0 {
		quoted := make([]string, 0, len(requiresAny))
		for _, dependency := range requiresAny {
			quoted = append(quoted, strconv.Quote(dependency))
		}
		dependencies = `,"requires_any":[` + strings.Join(quoted, ",") + `]`
	}
	return `{"id":"` + ruleID + `","source":` + sourceJSON + dependencies +
		`,"identity":{"key":"` + key + `","name":"` + name + `"}` +
		`,"strategy":{"name":"` + strategy + `","parameters":` + params + `}}`
}

// evalTestGroupJSON builds one candidate group fragment.
func evalTestGroupJSON(groupID, rootType, rootName, gateRule, entities string) string {
	root := `{"type":"` + rootType + `"`
	if rootName != "" {
		root += `,"name":"` + rootName + `"`
	}
	root += `}`
	gate := ""
	if gateRule != "" {
		gate = `,"gate_rule":"` + gateRule + `"`
	}
	return `{"id":"` + groupID + `","root":` + root + gate + `,"entities":[` + entities + `]}`
}

// evalTestPlannerJSON builds one planner profile document.
func evalTestPlannerJSON(
	docID string,
	order int,
	deviceKind, role, groups, devices string,
) string {
	document := `{"version":1,"kind":"planner-profile","id":"` + docID +
		`","order":` + strconv.Itoa(order) +
		`,"contribution":{"device_kind":"` + deviceKind + `","role":"` + role + `"}` +
		`,"candidate_groups":[` + groups + `]`
	if devices != "" {
		document += `,"device_entities":[` + devices + `]`
	}
	return document + `}`
}

// evalTestDeviceRuleJSON builds one device entity rule fragment.
func evalTestDeviceRuleJSON(
	ruleID, exposeType, exposeName, survivor, strategy, params, key, name string,
) string {
	return `{"id":"` + ruleID + `","expose":{"type":"` + exposeType + `","name":"` + exposeName + `"}` +
		`,"requires_group_survivor":"` + survivor + `"` +
		`,"identity":{"key":"` + key + `","name":"` + name + `"}` +
		`,"strategy":{"name":"` + strategy + `","parameters":` + params + `}}`
}

// evalTestOverrideJSON builds one override document with an exact vendor and
// model selector and optional exact build IDs.
func evalTestOverrideJSON(docID, vendor, model string, builds []string, patches string) string {
	buildsFragment := ""
	if len(builds) > 0 {
		quoted := make([]string, 0, len(builds))
		for _, build := range builds {
			quoted = append(quoted, strconv.Quote(build))
		}
		buildsFragment = `,"software_build_ids":[` + strings.Join(quoted, ",") + `]`
	}
	return `{"version":1,"kind":"profile-override","id":"` + docID +
		`","selector":{"vendor":"` + vendor + `","model":"` + model + `"` + buildsFragment +
		`},"patches":[` + patches + `]}`
}

// evalTestBinaryFeature builds one binary feature with exact on/off scalars
// and full publish, set, and get access, matching binary-power eligibility.
func evalTestBinaryFeature(name, property, on, off string) upstreamExpose {
	return upstreamExpose{
		Type: "binary", Name: name, Property: property,
		Access:  exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
		ValueOn: json.RawMessage(strconv.Quote(on)), ValueOff: json.RawMessage(strconv.Quote(off)),
	}
}

// evalTestNumericRoot builds one numeric root expose with publish and get
// access, matching read-only sensor eligibility with startup refresh.
func evalTestNumericRoot(name, property, unit string) upstreamExpose {
	return upstreamExpose{
		Type: "numeric", Name: name, Property: property, Unit: unit,
		Access: exposePublishAccessBit | exposeGetAccessBit,
	}
}

// evalTestSwitchDevice builds one synthetic switch device with the given
// roots, vendor, model, and software build evidence.
func evalTestSwitchDevice(vendor, model, build string, exposes ...upstreamExpose) upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "Router", Supported: true,
		FriendlyName: "eval-fixture", InterviewState: "SUCCESSFUL",
		SoftwareBuildID: tolerantSoftwareBuildID(build),
		Endpoints:       map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: model, Vendor: vendor, Description: "Evaluator fixture", Exposes: exposes,
		},
	}
}

// evalTestStateProperties reports the claimed state properties of every plan
// in order.
func evalTestStateProperties(plans []entityPlan) []string {
	properties := make([]string, 0, len(plans))
	for _, plan := range plans {
		properties = append(properties, plan.StateProperties...)
	}
	return properties
}

// evalPercentSensorParams is the fixed 0-100 percent float parameter object
// shared by humidity and battery rules.
const evalPercentSensorParams = `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
	`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`

// evalLinkqualitySensorParams is the fixed 0-255 integer parameter object
// accepting an absent or lqi upstream unit.
const evalLinkqualitySensorParams = `{"accepted_units":["","lqi"],"unit":"lqi","number_format":"integer",` +
	`"bounds":{"mode":"fixed","minimum":0,"maximum":255}}`

// This test protects retained inventory root order and fails if the
// evaluator reorders roots: two eligible temperature roots must plan in
// inventory order. A map-ordered root walk would scramble entity order.
func TestProfileEvaluatorPreservesRootOrder(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.temperature", `{"kind":"root"}`,
		"temperature", `{}`, "temperature", "Temperature")
	groups := evalTestGroupJSON("sensor.temperature-roots", "numeric", "temperature", "", rule)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
	})
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("temperature", "temperature_second", "°C"),
		evalTestNumericRoot("temperature", "temperature_first", "°C"),
	)
	// Roots are evaluated in retained inventory order, so reorder the device
	// roots to prove order follows inventory rather than document position.
	device.Definition.Exposes[0], device.Definition.Exposes[1] =
		device.Definition.Exposes[1], device.Definition.Exposes[0]
	contributions := evalTestContributions(catalog, device)
	if len(contributions) != 1 || len(contributions[0].Entities) != 2 {
		t.Fatalf("expected 2 temperature plans, got %+v", contributions)
	}
	got := evalTestStateProperties(contributions[0].Entities)
	if len(got) != 2 || got[0] != "temperature_first" || got[1] != "temperature_second" {
		t.Fatalf("root order = %v, want [temperature_first temperature_second]", got)
	}
}

// This test protects exact root selection and fails if the evaluator matches
// names loosely: calibration and comfort roots must never plan through a
// temperature group. A substring match would register phantom sensors.
func TestProfileEvaluatorSelectsExactRootNames(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.temperature", `{"kind":"root"}`,
		"temperature", `{}`, "temperature", "Temperature")
	groups := evalTestGroupJSON("sensor.temperature-roots", "numeric", "temperature", "", rule)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
	})
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("temperature_calibration", "temperature_calibration", "°C"),
		evalTestNumericRoot("comfort_temperature", "comfort_temperature", "°C"),
	)
	if contributions := evalTestContributions(catalog, device); len(contributions[0].Entities) != 0 {
		t.Fatalf("near-name roots planned %d entities, want 0", len(contributions[0].Entities))
	}
}

// This test protects feature source resolution and fails if the evaluator
// accepts ambiguous features: a duplicated binary state feature omits only
// its candidate through the exact-one UniqueFeature lookup.
func TestProfileEvaluatorResolvesFeatureSourcesExactlyOnce(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("relay.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	groups := evalTestGroupJSON("relay.roots", "switch", "", "relay.power", rule)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/relay.profile.json": evalTestPlannerJSON(
			"relay", 20, "relay", "primary", groups, ""),
	})
	ambiguous := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{
			evalTestBinaryFeature("state", "state", "ON", "OFF"),
			evalTestBinaryFeature("state", "state_shadow", "ON", "OFF"),
		},
	})
	if contributions := evalTestContributions(catalog, ambiguous); len(contributions[0].Entities) != 0 {
		t.Fatalf("duplicated feature planned %d entities, want 0", len(contributions[0].Entities))
	}
	unique := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"))
	if contributions := evalTestContributions(catalog, unique); len(contributions[0].Entities) != 1 {
		t.Fatalf("unique feature planned %d entities, want 1", len(contributions[0].Entities))
	}
}

// This test protects derived source resolution with requires_any and fails
// if the color-mode companion plans without a surviving color sibling or is
// skipped despite one. The defect would be a mode entity that guesses or a
// missing mode entity on a valid color light.
func TestProfileEvaluatorResolvesDerivedSourcesFromDeclaredSiblings(t *testing.T) {
	t.Parallel()
	temperature := evalTestRuleJSON("light.colortemp",
		`{"kind":"feature","type":"numeric","name":"color_temp"}`,
		"color-temperature", `{}`, "colortemp", "Color Temperature")
	mode := evalTestRuleJSON("light.colormode", `{"kind":"derived","name":"color-mode"}`,
		"color-mode", `{}`, "colormode", "Color Mode", "light.colortemp")
	groups := evalTestGroupJSON("light.roots", "light", "", "", temperature+","+mode)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/light.profile.json": evalTestPlannerJSON(
			"light", 10, "light", "primary", groups, ""),
	})
	eligible := colorStrategyTestDevice()
	if contributions := evalTestContributions(catalog, eligible); len(contributions[0].Entities) != 2 {
		t.Fatalf("color light planned %d entities, want temperature and mode",
			len(contributions[0].Entities))
	}
	broken := strategyTestDevice(colorLightExpose("", "state", "brightness",
		colorTempFeature("color_temp", 500, 150)))
	if contributions := evalTestContributions(catalog, broken); len(contributions[0].Entities) != 0 {
		t.Fatalf("broken temperature planned %d entities, want 0 without sibling evidence",
			len(contributions[0].Entities))
	}
}

// This test protects gate-first sibling gating and fails if optional
// siblings evaluate without a valid gate plan: a broken power feature must
// suppress its sibling for that root. Independent rule evaluation would
// register a sibling without eligible power.
func TestProfileEvaluatorGatesSiblingsOnGatePlan(t *testing.T) {
	t.Parallel()
	gate := evalTestRuleJSON("test.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	sibling := evalTestRuleJSON("test.extra", `{"kind":"feature","type":"binary","name":"mode"}`,
		"binary-power", `{}`, "extra", "Extra")
	groups := evalTestGroupJSON("test.roots", "switch", "", "test.power", gate+","+sibling)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/test.profile.json": evalTestPlannerJSON(
			"test", 20, "relay", "primary", groups, ""),
	})
	eligible := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{
			evalTestBinaryFeature("state", "state", "ON", "OFF"),
			evalTestBinaryFeature("mode", "mode", "AUTO", "MANUAL"),
		},
	})
	if contributions := evalTestContributions(catalog, eligible); len(contributions[0].Entities) != 2 {
		t.Fatalf("gated group planned %d entities, want gate and sibling",
			len(contributions[0].Entities))
	}
	brokenGate := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{
			evalTestBinaryFeature("state", "state", "ON", "ON"),
			evalTestBinaryFeature("mode", "mode", "AUTO", "MANUAL"),
		},
	})
	if contributions := evalTestContributions(catalog, brokenGate); len(contributions[0].Entities) != 0 {
		t.Fatalf("broken gate planned %d entities, want 0 with siblings suppressed",
			len(contributions[0].Entities))
	}
}

// This test protects duplicate scoped gate key removal and fails if two
// roots resolving to one power key keep any candidate: every duplicate
// candidate drops with all its siblings. Keeping the first duplicate would
// register ambiguous power.
func TestProfileEvaluatorDropsDuplicateGateKeysWithSiblings(t *testing.T) {
	t.Parallel()
	gate := evalTestRuleJSON("test.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	sibling := evalTestRuleJSON("test.extra", `{"kind":"feature","type":"binary","name":"mode"}`,
		"binary-power", `{}`, "extra", "Extra")
	groups := evalTestGroupJSON("test.roots", "switch", "", "test.power", gate+","+sibling)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/test.profile.json": evalTestPlannerJSON(
			"test", 20, "relay", "primary", groups, ""),
	})
	duplicate := evalTestSwitchDevice("Fixture", "EVAL", "",
		upstreamExpose{
			Type: "switch",
			Features: []upstreamExpose{
				evalTestBinaryFeature("state", "state_a", "ON", "OFF"),
				evalTestBinaryFeature("mode", "mode_a", "AUTO", "MANUAL"),
			},
		},
		upstreamExpose{
			Type: "switch",
			Features: []upstreamExpose{
				evalTestBinaryFeature("state", "state_b", "ON", "OFF"),
				evalTestBinaryFeature("mode", "mode_b", "AUTO", "MANUAL"),
			},
		},
	)
	if contributions := evalTestContributions(catalog, duplicate); len(contributions[0].Entities) != 0 {
		t.Fatalf("duplicate gate keys planned %d entities, want 0 dropped with siblings",
			len(contributions[0].Entities))
	}
	single := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{
			evalTestBinaryFeature("state", "state_a", "ON", "OFF"),
			evalTestBinaryFeature("mode", "mode_a", "AUTO", "MANUAL"),
		},
	})
	if contributions := evalTestContributions(catalog, single); len(contributions[0].Entities) != 2 {
		t.Fatalf("single root planned %d entities, want gate and sibling",
			len(contributions[0].Entities))
	}
}

// This test protects ungated sibling isolation and fails if one malformed,
// ineligible, or unresolved root suppresses valid siblings: only the
// eligible Celsius root plans.
func TestProfileEvaluatorIsolatesUngatedSiblings(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.temperature", `{"kind":"root"}`,
		"temperature", `{}`, "temperature", "Temperature")
	groups := evalTestGroupJSON("sensor.temperature-roots", "numeric", "temperature", "", rule)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
	})
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("temperature", "fahrenheit", "°F"),
		evalTestNumericRoot("temperature", "celsius", "°C"),
		upstreamExpose{
			Type: "numeric", Name: "temperature", Property: "unresolved",
			Unit: "°C", Endpoint: "9",
			Access: exposePublishAccessBit | exposeGetAccessBit,
		},
	)
	contributions := evalTestContributions(catalog, device)
	if len(contributions) != 1 || len(contributions[0].Entities) != 1 {
		t.Fatalf("expected only the eligible sibling, got %+v", contributions)
	}
	if got := evalTestStateProperties(contributions[0].Entities); len(got) != 1 || got[0] != "celsius" {
		t.Fatalf("isolated sibling properties = %v, want [celsius]", got)
	}
}

// This test protects requires_any sibling evidence and fails if a rule runs
// without a declared successful earlier plan: the dependent feature is
// valid, yet the failed dependency must still suppress it.
func TestProfileEvaluatorRequiresSuccessfulSiblings(t *testing.T) {
	t.Parallel()
	first := evalTestRuleJSON("test.first", `{"kind":"feature","type":"binary","name":"missing"}`,
		"binary-power", `{}`, "first", "First")
	second := evalTestRuleJSON("test.second", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "second", "Second", "test.first")
	groups := evalTestGroupJSON("test.roots", "switch", "", "", first+","+second)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/test.profile.json": evalTestPlannerJSON(
			"test", 20, "relay", "primary", groups, ""),
	})
	device := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"))
	if contributions := evalTestContributions(catalog, device); len(contributions[0].Entities) != 0 {
		t.Fatalf("unmet dependency planned %d entities, want 0", len(contributions[0].Entities))
	}
	met := evalTestRuleJSON("test.first", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "first", "First")
	metGroups := evalTestGroupJSON("test.roots", "switch", "", "", met+","+second)
	metCatalog := evalTestCatalog(t, map[string]string{
		"profiles/test.profile.json": evalTestPlannerJSON(
			"test", 20, "relay", "primary", metGroups, ""),
	})
	if contributions := evalTestContributions(metCatalog, device); len(contributions[0].Entities) != 2 {
		t.Fatalf("met dependency planned %d entities, want 2", len(contributions[0].Entities))
	}
}

// This test protects device entity group survivors and UniqueRoot behavior
// and fails if the power-on behavior joins without surviving power or
// survives a missing, duplicate, or unresolved expose match: only the valid
// device entity joins, and it never suppresses its gate family.
func TestProfileEvaluatorDeviceEntitiesNeedGroupSurvivors(t *testing.T) {
	t.Parallel()
	gate := evalTestRuleJSON("relay.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	groups := evalTestGroupJSON("relay.roots", "switch", "", "relay.power", gate)
	devices := evalTestDeviceRuleJSON("relay.behavior", "enum", "power_on_behavior",
		"relay.roots", "enum-setting", `{}`, "poweronbehavior", "Power-On Behavior")
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/relay.profile.json": evalTestPlannerJSON(
			"relay", 20, "relay", "primary", groups, devices),
	})
	behavior := upstreamExpose{
		Type: "enum", Name: "power_on_behavior", Property: "power_on_behavior",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
		Values: []string{"off", "on", "toggle", "previous"},
	}
	full := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"), behavior)
	if contributions := evalTestContributions(catalog, full); len(contributions[0].Entities) != 2 {
		t.Fatalf("surviving group planned %d entities, want power and behavior",
			len(contributions[0].Entities))
	}
	withoutPower := evalTestSwitchDevice("Fixture", "EVAL", "", behavior)
	if contributions := evalTestContributions(catalog, withoutPower); len(contributions[0].Entities) != 0 {
		t.Fatalf("powerless group planned %d entities, want 0 without a survivor",
			len(contributions[0].Entities))
	}
	withoutBehavior := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"))
	if contributions := evalTestContributions(catalog, withoutBehavior); len(contributions[0].Entities) != 1 {
		t.Fatalf("missing expose planned %d entities, want only power",
			len(contributions[0].Entities))
	}
	duplicated := evalTestSwitchDevice("Fixture", "EVAL", "",
		switchExpose("", "state"), behavior, behavior)
	if contributions := evalTestContributions(catalog, duplicated); len(contributions[0].Entities) != 1 {
		t.Fatalf("duplicated expose planned %d entities, want only power",
			len(contributions[0].Entities))
	}
	unresolvedBehavior := behavior
	unresolvedBehavior.Endpoint = "9"
	unresolvedBehavior.Property = "power_on_behavior_shadow"
	unresolved := evalTestSwitchDevice("Fixture", "EVAL", "",
		switchExpose("", "state"), unresolvedBehavior)
	if contributions := evalTestContributions(catalog, unresolved); len(contributions[0].Entities) != 1 {
		t.Fatalf("unresolved expose planned %d entities, want only power",
			len(contributions[0].Entities))
	}
}

// This test protects general and exact build override layering and fails if
// a general patch disables the wrong devices, an exact build patch cannot
// re-enable its rule, or a non-matching build leaks the exact patch: only
// the selected devices change.
func TestProfileEvaluatorLayersGeneralAndExactBuildOverrides(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.humidity", `{"kind":"root"}`,
		"numeric-sensor", evalPercentSensorParams, "humidity", "Humidity")
	groups := evalTestGroupJSON("sensor.humidity-roots", "numeric", "humidity", "", rule)
	docs := map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
		"profiles/overrides/disable.override.json": evalTestOverrideJSON(
			"disable-humidity", "Fixture", "EVAL", nil,
			`{"rule":"sensor.humidity","enabled":false}`),
		"profiles/overrides/reenable.override.json": evalTestOverrideJSON(
			"reenable-humidity", "Fixture", "EVAL", []string{"build-2"},
			`{"rule":"sensor.humidity","enabled":true}`),
	}
	catalog := evalTestCatalog(t, docs)
	humidity := evalTestNumericRoot("humidity", "humidity", "%")
	cases := []struct {
		name     string
		vendor   string
		model    string
		build    string
		entities int
	}{
		{"general disable applies", "Fixture", "EVAL", "", 0},
		{"general disable applies to other builds", "Fixture", "EVAL", "build-9", 0},
		{"exact build re-enables", "Fixture", "EVAL", "build-2", 1},
		{"vendor mismatch keeps base", "Other", "EVAL", "", 1},
		{"model mismatch keeps base", "Fixture", "OTHER", "", 1},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			device := evalTestSwitchDevice(testCase.vendor, testCase.model, testCase.build, humidity)
			contributions := evalTestContributions(catalog, device)
			if len(contributions[0].Entities) != testCase.entities {
				t.Fatalf("vendor=%q model=%q build=%q planned %d entities, want %d",
					testCase.vendor, testCase.model, testCase.build,
					len(contributions[0].Entities), testCase.entities)
			}
		})
	}
}

// This test protects override source replacement and fails if a patched
// candidate source is ignored: the base feature plans, yet the replacement
// feature lookup misses and omits only its candidate.
func TestProfileEvaluatorAppliesOverrideSourceReplacements(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("relay.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	groups := evalTestGroupJSON("relay.roots", "switch", "", "relay.power", rule)
	docs := map[string]string{
		"profiles/relay.profile.json": evalTestPlannerJSON(
			"relay", 20, "relay", "primary", groups, ""),
		"profiles/overrides/retarget.override.json": evalTestOverrideJSON(
			"retarget-power", "Fixture", "EVAL", nil,
			`{"rule":"relay.power","source":{"kind":"feature","type":"binary","name":"missing"}}`),
	}
	catalog := evalTestCatalog(t, docs)
	device := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"))
	if contributions := evalTestContributions(catalog, device); len(contributions[0].Entities) != 0 {
		t.Fatalf("replaced source planned %d entities, want 0", len(contributions[0].Entities))
	}
	unselected := evalTestSwitchDevice("Other", "EVAL", "", switchExpose("", "state"))
	if contributions := evalTestContributions(catalog, unselected); len(contributions[0].Entities) != 1 {
		t.Fatalf("unselected device planned %d entities, want the base source", len(contributions[0].Entities))
	}
}

// This test protects override strategy parameter replacement and fails if
// patched parameters are ignored: the base percent unit plans, yet the
// replacement accepted units omit only its candidate.
func TestProfileEvaluatorAppliesOverrideParameterReplacements(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.humidity", `{"kind":"root"}`,
		"numeric-sensor", evalPercentSensorParams, "humidity", "Humidity")
	groups := evalTestGroupJSON("sensor.humidity-roots", "numeric", "humidity", "", rule)
	replacement := `{"accepted_units":["ppm"],"unit":"%","number_format":"float",` +
		`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`
	docs := map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
		"profiles/overrides/reparam.override.json": evalTestOverrideJSON(
			"reparam-humidity", "Acme", "SENSOR", nil,
			`{"rule":"sensor.humidity","strategy_parameters":`+replacement+`}`),
	}
	catalog := evalTestCatalog(t, docs)
	humidity := evalTestNumericRoot("humidity", "humidity", "%")
	device := evalTestSwitchDevice("Acme", "SENSOR", "", humidity)
	if contributions := evalTestContributions(catalog, device); len(contributions[0].Entities) != 0 {
		t.Fatalf("replaced parameters planned %d entities, want 0", len(contributions[0].Entities))
	}
	unselected := evalTestSwitchDevice("Other", "EVAL", "", humidity)
	if contributions := evalTestContributions(catalog, unselected); len(contributions[0].Entities) != 1 {
		t.Fatalf("unselected device planned %d entities, want the base parameters", len(contributions[0].Entities))
	}
}

// This test protects override device expose replacement and fails if a
// patched device expose match is ignored: the gate family survives while
// the retargeted device entity is omitted.
func TestProfileEvaluatorAppliesOverrideExposeReplacements(t *testing.T) {
	t.Parallel()
	gate := evalTestRuleJSON("relay.power", `{"kind":"feature","type":"binary","name":"state"}`,
		"binary-power", `{}`, "power", "Power")
	groups := evalTestGroupJSON("relay.roots", "switch", "", "relay.power", gate)
	devices := evalTestDeviceRuleJSON("relay.behavior", "enum", "power_on_behavior",
		"relay.roots", "enum-setting", `{}`, "poweronbehavior", "Power-On Behavior")
	docs := map[string]string{
		"profiles/relay.profile.json": evalTestPlannerJSON(
			"relay", 20, "relay", "primary", groups, devices),
		"profiles/overrides/retarget.override.json": evalTestOverrideJSON(
			"retarget-behavior", "Fixture", "EVAL", nil,
			`{"rule":"relay.behavior","expose":{"type":"enum","name":"missing"}}`),
	}
	catalog := evalTestCatalog(t, docs)
	behavior := upstreamExpose{
		Type: "enum", Name: "power_on_behavior", Property: "power_on_behavior",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
		Values: []string{"off", "on", "toggle", "previous"},
	}
	device := evalTestSwitchDevice("Fixture", "EVAL", "", switchExpose("", "state"), behavior)
	contributions := evalTestContributions(catalog, device)
	if len(contributions) != 1 || len(contributions[0].Entities) != 1 {
		t.Fatalf("retargeted expose planned %+v, want only gate power", contributions)
	}
	if got := entityKeys(contributions[0].Entities); len(got) != 1 || got[0] != "power" {
		t.Fatalf("surviving keys = %v, want [power]", got)
	}
}

// This test protects per-profile contribution assembly and fails if
// profiles evaluate out of order or misdeclare kind and role: filenames
// never set precedence, and both sensor profiles stay supplemental.
func TestProfileEvaluatorAssemblesContributionsInOrder(t *testing.T) {
	t.Parallel()
	first := evalTestRuleJSON("sensor.temperature", `{"kind":"root"}`,
		"temperature", `{}`, "temperature", "Temperature")
	firstGroups := evalTestGroupJSON("sensor.temperature-roots", "numeric", "temperature", "", first)
	second := evalTestRuleJSON("linkquality.linkquality", `{"kind":"root"}`,
		"numeric-sensor", evalLinkqualitySensorParams, "linkquality", "Link Quality")
	secondGroups := evalTestGroupJSON("linkquality.roots", "numeric", "linkquality", "", second)
	catalog := evalTestCatalog(t, map[string]string{
		"profiles/b-linkquality.profile.json": evalTestPlannerJSON(
			"linkquality", 40, "sensor", "supplemental", secondGroups, ""),
		"profiles/a-sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", firstGroups, ""),
	})
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("temperature", "temperature", "°C"),
		upstreamExpose{
			Type: "numeric", Name: "linkquality", Property: "linkquality",
			Access: exposePublishAccessBit,
		},
	)
	contributions := evalTestContributions(catalog, device)
	if len(contributions) != 2 {
		t.Fatalf("expected 2 contributions, got %+v", contributions)
	}
	for _, contribution := range contributions {
		if contribution.Kind != upstreamDeviceKindSensor || contribution.Role != plannerRoleSupplemental {
			t.Fatalf("contribution kind=%q role=%v, want sensor supplemental", contribution.Kind, contribution.Role)
		}
	}
	if got := entityKeys(contributions[0].Entities); len(got) != 1 || got[0] != "temperature" {
		t.Fatalf("first contribution keys = %v, want [temperature] in profile order", got)
	}
	if got := entityKeys(contributions[1].Entities); len(got) != 1 || got[0] != "linkquality" {
		t.Fatalf("second contribution keys = %v, want [linkquality] in profile order", got)
	}
}
