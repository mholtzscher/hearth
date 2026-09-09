package zigbee2mqtt //nolint:testpackage // Strategy tests cover the private registry and planning.

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects the closed production strategy set and fails if a
// strategy is added, removed, renamed, or stored under a mismatched
// Definition.Name. The defect would be a profile referencing an unplanned
// strategy, or catalog diagnostics naming a strategy that never plans.
func TestDefaultProfileStrategyRegistryIsClosed(t *testing.T) {
	t.Parallel()
	registry := defaultProfileStrategyRegistry()
	want := []string{
		"binary-power",
		"brightness",
		"color-temperature",
		"color-xy",
		"color-hs",
		"color-mode",
		"startup-color-temperature",
		"temperature",
		"numeric-sensor",
		"numeric-setting",
		"enum-setting",
		"enum-action",
	}
	if len(registry) != len(want) {
		t.Fatalf(
			"registry holds %d strategies, want exactly %d: %v",
			len(registry),
			len(want),
			registryKeys(registry),
		)
	}
	for _, name := range want {
		definition, ok := registry[name]
		if !ok {
			t.Fatalf("registry is missing strategy %q", name)
		}
		if definition.Name != name {
			t.Fatalf("strategy %q has Definition.Name %q", name, definition.Name)
		}
		if len(definition.AllowedSourceKinds) == 0 {
			t.Fatalf("strategy %q allows no source kind", name)
		}
		if definition.CompileParameters == nil || definition.Plan == nil {
			t.Fatalf("strategy %q has a nil compiler or planner", name)
		}
	}
}

func registryKeys(registry profileStrategyRegistry) []string {
	keys := make([]string, 0, len(registry))
	for name := range registry {
		keys = append(keys, name)
	}
	return keys
}

// This test protects the exact empty parameter contract and fails if a
// strategy without profile-controlled parameters accepts fields, arrays,
// scalars, or null. The defect would be a silently ignored profile typo
// that an operator believes is effective.
func TestCompileEmptyProfileStrategyParameters(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{}`, "  {  }  ", "{\n}"} {
		if _, err := compileEmptyProfileStrategyParameters(json.RawMessage(raw)); err != nil {
			t.Fatalf("empty parameters %q rejected: %v", raw, err)
		}
	}
	for _, raw := range []string{
		``, `null`, `[]`, `"x"`, `0`, `{} {}`, `{"unit":"%"}`, `{"a":1,"b":2}`,
	} {
		if _, err := compileEmptyProfileStrategyParameters(json.RawMessage(raw)); err == nil {
			t.Fatalf("empty parameters %q accepted, want rejection", raw)
		}
	}
}

// This test protects the generic numeric-sensor parameter contract and
// fails if unit bounds, formats, or closed-object rules drift. The oracle
// is the catalog spec: accepted_units holds 1-4 unique exact strings,
// unit is required, number_format is integer or float, bounds mode is
// fixed or upstream-or-fallback, and minimum is strictly below maximum.
func TestCompileNumericSensorProfileStrategyParameters(t *testing.T) {
	t.Parallel()
	fixed := `{"accepted_units":["","lqi"],"unit":"lqi","number_format":"integer",` +
		`"bounds":{"mode":"fixed","minimum":0,"maximum":255}}`
	parameters, err := compileNumericSensorProfileStrategyParameters(json.RawMessage(fixed))
	if err != nil {
		t.Fatalf("valid fixed parameters rejected: %v", err)
	}
	typed, ok := parameters.(numericSensorStrategyParameters)
	if !ok {
		t.Fatalf("compiled parameters have type %T, want numericSensorStrategyParameters", parameters)
	}
	if typed.Unit != "lqi" || typed.NumberFormat != "integer" || typed.Bounds.Mode != "fixed" ||
		typed.Bounds.Minimum != 0 || typed.Bounds.Maximum != 255 ||
		!reflect.DeepEqual(typed.AcceptedUnits, []string{"", "lqi"}) {
		t.Fatalf("compiled parameters lost profile values: %#v", typed)
	}
	fallback := `{"accepted_units":["Hz"],"unit":"Hz","number_format":"float",` +
		`"bounds":{"mode":"upstream-or-fallback","minimum":0,"maximum":1000}}`
	if _, err = compileNumericSensorProfileStrategyParameters(json.RawMessage(fallback)); err != nil {
		t.Fatalf("valid fallback parameters rejected: %v", err)
	}
	unicodeUnit := strings.Repeat("°", 32)
	unicodeParams := `{"accepted_units":["` + unicodeUnit + `"],"unit":"` + unicodeUnit + `",` +
		`"number_format":"float","bounds":{"mode":"fixed","minimum":0,"maximum":100}}`
	if _, err = compileNumericSensorProfileStrategyParameters(json.RawMessage(unicodeParams)); err != nil {
		t.Fatalf("32-character Unicode unit rejected: %v", err)
	}
	const sensorParamTail = `"unit":"%","number_format":"float",` +
		`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`
	invalid := []struct {
		name string
		raw  string
	}{
		{"empty accepted units", `{"accepted_units":[],` + sensorParamTail},
		{"too many accepted units", `{"accepted_units":["a","b","c","d","e"],` + sensorParamTail},
		{"duplicate accepted units", `{"accepted_units":["%","%"],` + sensorParamTail},
		{"oversize accepted unit", `{"accepted_units":["` + strings.Repeat("u", 33) + `"],` + sensorParamTail},
		{"oversize Unicode accepted unit", `{"accepted_units":["` + strings.Repeat("°", 33) + `"],` + sensorParamTail},
		{"missing unit", `{"accepted_units":["%"],"number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`},
		{"empty unit", `{"accepted_units":["%"],"unit":"",` +
			`"number_format":"float","bounds":{"mode":"fixed","minimum":0,"maximum":100}}`},
		{"oversize unit", `{"accepted_units":["%"],"unit":"` + strings.Repeat("u", 33) + `",` +
			`"number_format":"float","bounds":{"mode":"fixed","minimum":0,"maximum":100}}`},
		{"bad number format", `{"accepted_units":["%"],"unit":"%","number_format":"double",` +
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`},
		{"bad bounds mode", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"upstream","minimum":0,"maximum":100}}`},
		{"inverted bounds", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":100,"maximum":0}}`},
		{"missing minimum", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","maximum":100}}`},
		{"null minimum", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":null,"maximum":100}}`},
		{"equal bounds", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":50,"maximum":50}}`},
		{"missing bounds", `{"accepted_units":["%"],"unit":"%","number_format":"float"}`},
		{"unknown field", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100},"scale":2}`},
		{"unknown bounds field", `{"accepted_units":["%"],"unit":"%","number_format":"float",` +
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100,"step":1}}`},
		{"null", `null`},
		{"empty", ``},
		{"array", `[]`},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, compileErr := compileNumericSensorProfileStrategyParameters(json.RawMessage(tc.raw))
			if compileErr == nil {
				t.Fatalf("invalid parameters accepted: %s", tc.raw)
			}
		})
	}
}

// This test protects the generic numeric-setting parameter contract and
// fails if the optional unit handling drifts. The oracle is the catalog
// spec: accepted_units follows the numeric-sensor exact-match rules, unit
// is optional, and unknown fields are rejected.
func TestCompileNumericSettingProfileStrategyParameters(t *testing.T) {
	t.Parallel()
	withUnit, err := compileNumericSettingProfileStrategyParameters(
		json.RawMessage(`{"accepted_units":["%"],"unit":"%"}`))
	if err != nil {
		t.Fatalf("valid parameters with unit rejected: %v", err)
	}
	typedWithUnit, ok := withUnit.(numericSettingStrategyParameters)
	if !ok || typedWithUnit.Unit == nil || *typedWithUnit.Unit != "%" {
		t.Fatalf("compiled unit lost: %#v", withUnit)
	}
	unicodeUnit := strings.Repeat("°", 32)
	if _, err = compileNumericSettingProfileStrategyParameters(json.RawMessage(
		`{"accepted_units":["` + unicodeUnit + `"],"unit":"` + unicodeUnit + `"}`)); err != nil {
		t.Fatalf("32-character Unicode numeric-setting unit rejected: %v", err)
	}
	withoutUnit, err := compileNumericSettingProfileStrategyParameters(
		json.RawMessage(`{"accepted_units":[""]}`))
	if err != nil {
		t.Fatalf("valid parameters without unit rejected: %v", err)
	}
	typedNoUnit, unitFound := withoutUnit.(numericSettingStrategyParameters)
	if !unitFound || typedNoUnit.Unit != nil {
		t.Fatalf("omitted unit must stay nil: %#v", withoutUnit)
	}
	for name, raw := range map[string]string{
		"empty accepted units":      `{"accepted_units":[]}`,
		"duplicate accepted units":  `{"accepted_units":["s","s"]}`,
		"empty unit":                `{"accepted_units":["s"],"unit":""}`,
		"oversize unit":             `{"accepted_units":["s"],"unit":"` + strings.Repeat("u", 33) + `"}`,
		"unknown field":             `{"accepted_units":["s"],"scale":2}`,
		"null unit is not omission": `{"accepted_units":["s"],"unit":null}`,
		"null":                      `null`,
	} {
		if _, err = compileNumericSettingProfileStrategyParameters(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s accepted %q, want rejection", name, raw)
		}
	}
}

// This test protects the generic enum-action access contract and fails if
// an unknown access mode compiles. The oracle is the catalog spec: access
// is exactly set-only or includes-set with no other fields.
func TestCompileEnumActionProfileStrategyParameters(t *testing.T) {
	t.Parallel()
	for _, access := range []string{"set-only", "includes-set"} {
		parameters, err := compileEnumActionProfileStrategyParameters(
			json.RawMessage(`{"access":"` + access + `"}`))
		if err != nil {
			t.Fatalf("valid access %q rejected: %v", access, err)
		}
		if typed, ok := parameters.(enumActionStrategyParameters); !ok || typed.Access != access {
			t.Fatalf("compiled access lost: %#v", parameters)
		}
	}
	for name, raw := range map[string]string{
		"unknown access": `{"access":"publish-only"}`,
		"missing access": `{}`,
		"empty access":   `{"access":""}`,
		"unknown field":  `{"access":"set-only","values":["a"]}`,
		"null":           `null`,
	} {
		if _, err := compileEnumActionProfileStrategyParameters(json.RawMessage(raw)); err == nil {
			t.Fatalf("%s accepted %q, want rejection", name, raw)
		}
	}
}

// strategyTestKey derives the profile entity base key for one strategy
// test rule ID by stripping its test namespace.
func strategyTestKey(id string) string {
	return strings.ReplaceAll(strings.ReplaceAll(id, "test.", ""), "-", "")
}

// strategyTestPlannerDocument builds one planner profile document covering
// every registry strategy: feature strategies in a gated light group,
// root strategies in ungated numeric groups, and one device-entity rule
// behind the gated group survivor.
func strategyTestPlannerDocument() plannerProfileDocument {
	featureRule := func(id, kind, exposeType, exposeName, strategy string) profileCandidateEntity {
		return profileCandidateEntity{
			ID: id,
			Source: profileEntitySource{
				Kind: profileEntitySourceKind(kind), Type: exposeType, Name: exposeName,
			},
			Identity:    profileEntityIdentity{Key: strategyTestKey(id), Name: id},
			Strategy:    profileStrategyRef{Name: strategy, Parameters: json.RawMessage(`{}`)},
			RequiresAny: nil,
		}
	}
	rootRule := func(id, strategy, params, key string) profileCandidateEntity {
		return profileCandidateEntity{
			ID:       id,
			Source:   profileEntitySource{Kind: profileEntitySourceRoot},
			Identity: profileEntityIdentity{Key: key, Name: id},
			Strategy: profileStrategyRef{Name: strategy, Parameters: json.RawMessage(params)},
		}
	}
	modeRule := featureRule("test.mode", "derived", "", "color-mode", "color-mode")
	modeRule.RequiresAny = []string{"test.colortemp", "test.colorxy"}
	return plannerProfileDocument{
		Version: 1, Kind: profileDocumentPlanner, ID: "strategy-test", Order: 10,
		Contribution: profileContribution{DeviceKind: "light", Role: profilePlannerRolePrimary},
		CandidateGroups: []profileCandidateGroup{
			{
				ID: "test.lights", Root: profileCandidateRootSelector{Type: "light"}, GateRule: "test.power",
				Entities: []profileCandidateEntity{
					featureRule("test.power", "feature", "binary", "state", "binary-power"),
					featureRule("test.brightness", "feature", "numeric", "brightness", "brightness"),
					featureRule("test.colortemp", "feature", "numeric", "color_temp", "color-temperature"),
					featureRule("test.colorxy", "feature", "composite", "color_xy", "color-xy"),
					featureRule("test.colorhs", "feature", "composite", "color_hs", "color-hs"),
					modeRule,
					featureRule(
						"test.startup", "feature", "numeric", "color_temp_startup", "startup-color-temperature",
					),
				},
			},
			{
				ID: "test.sensors", Root: profileCandidateRootSelector{Type: "numeric"},
				Entities: []profileCandidateEntity{
					rootRule("test.temperature", "temperature", `{}`, "temperature"),
					rootRule(
						"test.humidity", "numeric-sensor",
						`{"accepted_units":["%"],"unit":"%","number_format":"float",`+
							`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`,
						"humidity",
					),
				},
			},
			{
				ID: "test.settings", Root: profileCandidateRootSelector{Type: "numeric", Name: "valve"},
				Entities: []profileCandidateEntity{
					rootRule(
						"test.valve", "numeric-setting", `{"accepted_units":["%"],"unit":"%"}`, "valveposition",
					),
				},
			},
			{
				ID: "test.enums", Root: profileCandidateRootSelector{Type: "enum"},
				Entities: []profileCandidateEntity{
					rootRule("test.fanmode", "enum-setting", `{}`, "fanmode"),
					rootRule("test.selftest", "enum-action", `{"access":"set-only"}`, "selftest"),
				},
			},
		},
		DeviceEntities: []profileDeviceEntity{
			{
				ID: "test.poweronbehavior", Expose: profileExposeSelector{Type: "enum", Name: "power_on_behavior"},
				RequiresGroupSurvivor: "test.lights",
				Identity:              profileEntityIdentity{Key: "poweronbehavior", Name: "test.poweronbehavior"},
				Strategy:              profileStrategyRef{Name: "enum-setting", Parameters: json.RawMessage(`{}`)},
			},
		},
	}
}

// This test protects strategy resolution at catalog compilation and fails
// if any of the 12 registry names does not resolve with its documented
// source form and parameter shape. The oracle is the closed registry: the
// same names profiles will select in production.
func TestProfileStrategyCompilerResolvesAllTwelve(t *testing.T) {
	t.Parallel()
	document := strategyTestPlannerDocument()
	catalog, err := compileDecodedProfileCatalog(
		[]decodedProfileDocument{{path: "profiles/strategy-test.profile.json", planner: &document}},
		defaultProfileStrategyRegistry(),
	)
	if err != nil {
		t.Fatalf("all-strategy document rejected: %v", err)
	}
	if len(catalog.profiles) != 1 {
		t.Fatalf("catalog holds %d profiles, want 1", len(catalog.profiles))
	}
	compiled := catalog.profiles[0].ruleParameters
	if len(compiled) != 13 {
		t.Fatalf("catalog holds %d compiled rules, want 13", len(compiled))
	}
	humidity, humidityFound := compiled["test.humidity"]
	if !humidityFound {
		t.Fatal("humidity rule missing from compiled parameters")
	}
	humidityParams, humidityTyped := humidity.Parameters.(numericSensorStrategyParameters)
	if !humidityTyped || humidityParams.Unit != "%" || humidityParams.NumberFormat != "float" {
		t.Fatalf("humidity parameters lost profile values: %#v", humidity.Parameters)
	}
	valve, valveFound := compiled["test.valve"]
	if !valveFound {
		t.Fatal("valve rule missing from compiled parameters")
	}
	if valveParams, valveTyped := valve.Parameters.(numericSettingStrategyParameters); !valveTyped ||
		valveParams.Unit == nil || *valveParams.Unit != "%" {
		t.Fatalf("valve parameters lost profile values: %#v", valve.Parameters)
	}
	action, actionFound := compiled["test.selftest"]
	if !actionFound {
		t.Fatal("self-test rule missing from compiled parameters")
	}
	if actionParams, actionTyped := action.Parameters.(enumActionStrategyParameters); !actionTyped ||
		actionParams.Access != "set-only" {
		t.Fatalf("self-test parameters lost profile values: %#v", action.Parameters)
	}
}

// This test protects catalog rejection of strategy mismatches and fails if
// an unknown strategy, a strategy/source mismatch, or invalid parameters
// compile. The defect would be a profile that loads but never plans the
// affected Entity, or that plans from the wrong evidence.
func TestProfileStrategyCompilerRejectsMismatches(t *testing.T) {
	t.Parallel()
	validEntities := func() []profileCandidateEntity {
		return []profileCandidateEntity{{
			ID:       "test.power",
			Source:   profileEntitySource{Kind: profileEntitySourceFeature, Type: "binary", Name: "state"},
			Identity: profileEntityIdentity{Key: "power", Name: "Power"},
			Strategy: profileStrategyRef{Name: "binary-power", Parameters: json.RawMessage(`{}`)},
		}}
	}
	compile := func(mutate func(*plannerProfileDocument)) error {
		document := plannerProfileDocument{
			Version: 1, Kind: profileDocumentPlanner, ID: "strategy-test", Order: 10,
			Contribution: profileContribution{DeviceKind: "light", Role: profilePlannerRolePrimary},
			CandidateGroups: []profileCandidateGroup{{
				ID:       "test.group",
				Root:     profileCandidateRootSelector{Type: "light"},
				Entities: validEntities(),
			}},
		}
		mutate(&document)
		_, err := compileDecodedProfileCatalog(
			[]decodedProfileDocument{{path: "profiles/strategy-test.profile.json", planner: &document}},
			defaultProfileStrategyRegistry(),
		)
		return err
	}
	firstRule := func(document *plannerProfileDocument) *profileCandidateEntity {
		return &document.CandidateGroups[0].Entities[0]
	}
	cases := []struct {
		name   string
		code   string
		rule   string
		mutate func(*plannerProfileDocument)
	}{
		{
			"unknown strategy",
			profileCatalogErrorUnknownStrategy,
			"test.power",
			func(document *plannerProfileDocument) {
				firstRule(document).Strategy.Name = "turbo-power"
			},
		},
		{
			"feature strategy on root source",
			profileCatalogErrorStrategySourceMismatch,
			"test.power",
			func(document *plannerProfileDocument) {
				firstRule(document).Source = profileEntitySource{Kind: profileEntitySourceRoot}
			},
		},
		{
			"root strategy on feature source",
			profileCatalogErrorStrategySourceMismatch,
			"test.power",
			func(document *plannerProfileDocument) {
				firstRule(document).Strategy.Name = "enum-setting"
			},
		},
		{
			"derived strategy on feature source",
			profileCatalogErrorStrategySourceMismatch,
			"test.power",
			func(document *plannerProfileDocument) {
				firstRule(document).Strategy.Name = "color-mode"
				firstRule(document).Source = profileEntitySource{
					Kind: profileEntitySourceFeature, Type: "enum", Name: "color_mode",
				}
			},
		},
		{
			"non-empty empty params",
			profileCatalogErrorStrategyParamsInvalid,
			"test.power",
			func(document *plannerProfileDocument) {
				firstRule(document).Strategy.Parameters = json.RawMessage(`{"debug":true}`)
			},
		},
		{
			"inverted sensor bounds",
			profileCatalogErrorStrategyParamsInvalid,
			"test.power",
			func(document *plannerProfileDocument) {
				rule := &document.CandidateGroups[0].Entities[0]
				rule.Strategy.Name = "numeric-sensor"
				rule.Source = profileEntitySource{Kind: profileEntitySourceRoot}
				rule.Strategy.Parameters = json.RawMessage(
					`{"accepted_units":["%"],"unit":"%","number_format":"float",` +
						`"bounds":{"mode":"fixed","minimum":100,"maximum":100}}`)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := compile(tc.mutate)
			if err == nil {
				t.Fatal("mismatched strategy compiled, want rejection")
			}
			var catalogErr *ProfileCatalogError
			if !errors.As(err, &catalogErr) {
				t.Fatalf("error has type %T, want *ProfileCatalogError", err)
			}
			if catalogErr.Code != tc.code {
				t.Fatalf("error code = %q, want %q (%v)", catalogErr.Code, tc.code, err)
			}
			if catalogErr.RuleID != tc.rule {
				t.Fatalf("error rule = %q, want %q", catalogErr.RuleID, tc.rule)
			}
		})
	}
}

// This test protects unresolved-endpoint isolation and fails if a
// strategy plans an Entity for a root whose endpoint did not resolve.
// The handwritten families skip such roots before planning, so an
// unresolved plan would invent an ep0 identity that never matches a device.
func TestProfileStrategiesOmitUnresolvedRoots(t *testing.T) {
	t.Parallel()
	device := strategyTestDevice(lightExpose("left", "state", "brightness"))
	device.Endpoints = map[string]upstreamEndpoint{}
	index := newExposeIndex(device)
	roots := index.Roots("light")
	if len(roots) == 0 || roots[0].resolved {
		t.Fatal("fixture root should be unresolved without endpoint evidence")
	}
	root := roots[0]
	feature, ok := index.UniqueFeature(root, featureQuery{Type: "binary", Name: "state"})
	if !ok {
		t.Fatal("power feature is not unique")
	}
	registry := defaultProfileStrategyRegistry()
	powerInput := profileStrategyInput{
		IEEE: device.IEEEAddress, Root: root, Expose: &feature, Index: index,
		Identity: profileEntityIdentity{Key: "power", Name: "Power"},
		Prior:    map[string]entityPlan{},
	}
	if _, planned := registry["binary-power"].Plan(powerInput, emptyProfileStrategyParameters{}); planned {
		t.Fatal("binary-power planned an unresolved root")
	}
	temperatureDevice := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "temperature", Property: "temperature",
		Endpoint: "left", Access: exposePublishAccessBit | exposeGetAccessBit, Unit: "°C",
	})
	temperatureDevice.Endpoints = map[string]upstreamEndpoint{}
	temperatureIndex := newExposeIndex(temperatureDevice)
	temperatureRoots := temperatureIndex.Roots("numeric")
	if len(temperatureRoots) == 0 || temperatureRoots[0].resolved {
		t.Fatal("fixture root should be unresolved without endpoint evidence")
	}
	temperatureInput := profileStrategyInput{
		IEEE: temperatureDevice.IEEEAddress, Root: temperatureRoots[0], Index: temperatureIndex,
		Identity: profileEntityIdentity{Key: "temperature", Name: "Temperature"},
		Prior:    map[string]entityPlan{},
	}
	if _, planned := registry["temperature"].Plan(
		temperatureInput,
		emptyProfileStrategyParameters{},
	); planned {
		t.Fatal("temperature planned an unresolved root")
	}
}

// strategyTestDevice builds one synthetic device with a stable IEEE address
// and the given root exposes.
func strategyTestDevice(exposes ...upstreamExpose) upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "Router", Supported: true,
		FriendlyName: "strategy-fixture", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "STRATEGY", Vendor: "Fixture", Description: "Strategy fixture", Exposes: exposes,
		},
	}
}

// strategyLightFeatureInput resolves one feature-source strategy input from
// a synthetic light root. The identity mirrors the profile rule identity
// the evaluator would pass.
func strategyLightFeatureInput(
	t *testing.T,
	device upstreamDevice,
	featureType, featureName, key, name string,
) profileStrategyInput {
	t.Helper()
	index := newExposeIndex(device)
	roots := index.Roots("light")
	if len(roots) == 0 {
		t.Fatal("no light roots in fixture")
	}
	root := roots[0]
	feature, ok := index.UniqueFeature(root, featureQuery{Type: featureType, Name: featureName})
	if !ok {
		t.Fatalf("feature %q/%q is not unique", featureType, featureName)
	}
	return profileStrategyInput{
		IEEE: device.IEEEAddress, Root: root, Expose: &feature, Index: index,
		Identity: profileEntityIdentity{Key: key, Name: name}, Prior: map[string]entityPlan{},
	}
}

// strategyRootInput resolves one root-source strategy input from a
// synthetic device by root type and name.
func strategyRootInput(
	t *testing.T,
	device upstreamDevice,
	rootType, rootName, key, name string,
) profileStrategyInput {
	t.Helper()
	index := newExposeIndex(device)
	root, ok := index.UniqueRoot(rootType, rootName)
	if !ok {
		t.Fatalf("root %q/%q is not unique", rootType, rootName)
	}
	expose := root.expose
	return profileStrategyInput{
		IEEE: device.IEEEAddress, Root: root, Expose: &expose, Index: index,
		Identity: profileEntityIdentity{Key: key, Name: name}, Prior: map[string]entityPlan{},
	}
}

// assertEntityPlanParity compares one strategy plan against the handwritten
// planner plan: identical descriptor, policy, claimed properties, and
// translator presence. The handwritten plan is the previously trusted
// implementation, so divergence names a behavior the strategy changed.
func assertEntityPlanParity(t *testing.T, want, got entityPlan) {
	t.Helper()
	if !reflect.DeepEqual(want.Descriptor, got.Descriptor) {
		t.Fatalf("descriptor diverged:\nwant %#v\ngot  %#v", want.Descriptor, got.Descriptor)
	}
	if want.StatePolicy != got.StatePolicy {
		t.Fatalf("state policy = %v, want %v", got.StatePolicy, want.StatePolicy)
	}
	if !reflect.DeepEqual(want.StateProperties, got.StateProperties) {
		t.Fatalf("state properties = %v, want %v", got.StateProperties, want.StateProperties)
	}
	if !reflect.DeepEqual(want.GetProperties, got.GetProperties) {
		t.Fatalf("get properties = %v, want %v", got.GetProperties, want.GetProperties)
	}
	if (want.TranslateCommand == nil) != (got.TranslateCommand == nil) {
		t.Fatal("command translator presence diverged")
	}
}

// decodeStrategyState decodes one property payload through one plan and
// returns its semantic value, failing the test when the property that must
// decode is absent or invalid.
func decodeStrategyState(
	t *testing.T,
	plan entityPlan,
	property, payload string,
) (stateReport, bool) {
	t.Helper()
	report, present, err := plan.DecodeState("entity-test",
		map[string]json.RawMessage{property: json.RawMessage(payload)}, time.Now().UTC())
	if err != nil {
		t.Fatalf("decode of %s failed: %v", payload, err)
	}
	return report, present
}

func strategyResponder() adapter.Responder {
	return newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{}))
}

// This test protects binary-power strategy parity and fails if the profile
// path diverges from the shared light/relay power behavior in eligibility,
// identity, state decoding, command translation, or outcome matching. The
// defect would be a light or relay whose power Entity behaves differently
// under profile planning.
func TestBinaryPowerProfileStrategyMatchesHandwrittenPower(t *testing.T) {
	t.Parallel()
	device := strategyTestDevice(lightExpose("", "state", "brightness"))
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	root := planningInput.Exposes.Roots("light")[0]
	want, ok := planPowerEntity(planningInput, root)
	if !ok {
		t.Fatal("handwritten power plan is unexpectedly ineligible")
	}
	input := strategyLightFeatureInput(t, device, "binary", "state", "power", "Power")
	definition := defaultProfileStrategyRegistry()["binary-power"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("binary-power strategy omitted the eligible power feature")
	}
	assertEntityPlanParity(t, want, got)
	if got.Descriptor.Key != "power" || got.Descriptor.ExternalID != device.IEEEAddress+"/root/power" {
		t.Fatalf("power identity = %q/%q", got.Descriptor.Key, got.Descriptor.ExternalID)
	}
	for payload, state := range map[string]bool{`"ON"`: true, `"OFF"`: false} {
		wantReport, _ := decodeStrategyState(t, want, "state", payload)
		gotReport, _ := decodeStrategyState(t, got, "state", payload)
		if wantReport.semantic != state || gotReport.semantic != state {
			t.Fatalf(
				"payload %s decoded to %v/%v, want %v",
				payload,
				wantReport.semantic,
				gotReport.semantic,
				state,
			)
		}
	}
	dimState := map[string]json.RawMessage{"state": json.RawMessage(`"DIM"`)}
	if _, _, err := want.DecodeState("e", dimState, time.Now().UTC()); err == nil {
		t.Fatal("handwritten power accepted an off-value scalar")
	}
	if _, _, err := got.DecodeState("e", dimState, time.Now().UTC()); err == nil {
		t.Fatal("strategy power accepted an off-value scalar")
	}
	powerOn := testCommand("e", `{"value":true}`)
	wantPlanned, err := want.TranslateCommand(context.Background(), "e", powerOn, strategyResponder())
	if err != nil {
		t.Fatalf("handwritten power command failed: %v", err)
	}
	gotPlanned, err := got.TranslateCommand(context.Background(), "e", powerOn, strategyResponder())
	if err != nil {
		t.Fatalf("strategy power command failed: %v", err)
	}
	if string(wantPlanned.SetValues["state"]) != string(gotPlanned.SetValues["state"]) {
		t.Fatalf("power set payload = %s, want %s", gotPlanned.SetValues["state"], wantPlanned.SetValues["state"])
	}
	report, _ := decodeStrategyState(t, got, "state", `"ON"`)
	if !gotPlanned.Matches(report) || !wantPlanned.Matches(report) {
		t.Fatal("power matcher rejected the commanded state")
	}
	// An ineligible feature (missing set access) omits only that candidate.
	denied := strategyTestDevice(lightExpose("", "state", "brightness"))
	denied.Definition.Exposes[0].Features[0].Access = exposePublishAccessBit | exposeGetAccessBit
	deniedInput := strategyLightFeatureInput(t, denied, "binary", "state", "power", "Power")
	if _, deniedPlanned := definition.Plan(deniedInput, emptyProfileStrategyParameters{}); deniedPlanned {
		t.Fatal("binary-power strategy planned a feature without set access")
	}
	// Identical on/off scalars are ambiguous and omit the candidate, matching
	// the handwritten power behavior.
	ambiguous := strategyTestDevice(lightExpose("", "state", "brightness"))
	ambiguous.Definition.Exposes[0].Features[0].ValueOff = json.RawMessage(`"ON"`)
	ambiguousInput := strategyLightFeatureInput(t, ambiguous, "binary", "state", "power", "Power")
	if _, ambiguousPlanned := definition.Plan(
		ambiguousInput,
		emptyProfileStrategyParameters{},
	); ambiguousPlanned {
		t.Fatal("binary-power strategy planned indistinguishable on/off scalars")
	}
}

// This test protects brightness strategy parity and fails if percent
// scaling, rounding, range checks, or observed matching diverge from the
// handwritten brightness behavior.
func TestBrightnessProfileStrategyMatchesHandwrittenBrightness(t *testing.T) {
	t.Parallel()
	device := strategyTestDevice(lightExpose("", "state", "brightness"))
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	root := planningInput.Exposes.Roots("light")[0]
	want := planBrightness(planningInput, root)
	if want == nil {
		t.Fatal("handwritten brightness plan is unexpectedly ineligible")
	}
	input := strategyLightFeatureInput(t, device, "numeric", "brightness", "brightness", "Brightness")
	definition := defaultProfileStrategyRegistry()["brightness"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("brightness strategy omitted the eligible feature")
	}
	assertEntityPlanParity(t, *want, got)
	wantReport, _ := decodeStrategyState(t, *want, "brightness", `254`)
	gotReport, _ := decodeStrategyState(t, got, "brightness", `254`)
	if wantReport.semantic != gotReport.semantic || gotReport.semantic != int64(100) {
		t.Fatalf("brightness 254 decoded to %v/%v, want 100", wantReport.semantic, gotReport.semantic)
	}
}

// colorStrategyTestDevice builds one light root with power, color
// temperature, XY, and HS features sharing one color property.
func colorStrategyTestDevice() upstreamDevice {
	zero := 0.0
	one := 1.0
	hueMax := float64(hueDomainMaximum)
	saturationMax := float64(saturationDomainMaximum)
	xyAxes := func() []upstreamExpose {
		return []upstreamExpose{
			colorAxisChild("x", &zero, &one),
			colorAxisChild("y", &zero, &one),
		}
	}
	hsAxes := func() []upstreamExpose {
		return []upstreamExpose{
			colorAxisChild("hue", &zero, &hueMax),
			colorAxisChild("saturation", &zero, &saturationMax),
		}
	}
	tempMin, tempMax := 150.0, 500.0
	return strategyTestDevice(colorLightExpose("", "state", "brightness",
		colorTempFeature("color_temp", tempMin, tempMax),
		colorXYComposite("color", xyAxes()...),
		colorHSComposite("color", hsAxes()...),
	))
}

// colorStrategyPlanningInput returns the shared device input plus the
// companion mode policy the handwritten light planner computes.
func colorStrategyPlanningInput(device upstreamDevice) (devicePlanningInput, indexedExpose, colorModePolicy) {
	input := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	root := input.Exposes.Roots("light")[0]
	expectations := map[string]int{colorModeProperty(root): 1}
	mode := colorModePolicy{
		property: colorModeProperty(root),
		usable: expectations[colorModeProperty(root)] == 1 &&
			!input.Exposes.propertyHasForeignClaim(colorModeProperty(root)),
	}
	return input, root, mode
}

// This test protects color-temperature strategy parity and fails if mired
// bounds, same-message mode activity, or observed matching diverge from the
// handwritten behavior.
func TestColorTemperatureProfileStrategyMatchesHandwrittenColorTemp(t *testing.T) {
	t.Parallel()
	device := colorStrategyTestDevice()
	planningInput, root, mode := colorStrategyPlanningInput(device)
	want := planColorTemp(planningInput, root, hasColorComposite(root), mode)
	if want == nil {
		t.Fatal("handwritten color-temperature plan is unexpectedly ineligible")
	}
	input := strategyLightFeatureInput(t, device, "numeric", "color_temp", "colortemp", "Color Temperature")
	definition := defaultProfileStrategyRegistry()["color-temperature"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("color-temperature strategy omitted the eligible feature")
	}
	assertEntityPlanParity(t, *want, got)
	if !reflect.DeepEqual(want.StateProperties, []string{"color_temp", "color_mode"}) {
		t.Fatalf("handwritten state properties = %v, want the mode companion claimed", want.StateProperties)
	}
}

// This test protects XY/HS strategy parity and fails if composite or axis
// validation, shared property rules, scaling, or tolerance matching diverge
// from the handwritten color behavior.
func TestColorXYAndHSProfileStrategiesMatchHandwrittenColor(t *testing.T) {
	t.Parallel()
	device := colorStrategyTestDevice()
	planningInput, root, mode := colorStrategyPlanningInput(device)
	wantXY := planColorXY(planningInput, root, mode)
	wantHS := planColorHS(planningInput, root, mode)
	if wantXY == nil || wantHS == nil {
		t.Fatal("handwritten XY/HS plans are unexpectedly ineligible")
	}
	registry := defaultProfileStrategyRegistry()
	xyInput := strategyLightFeatureInput(t, device, "composite", "color_xy", "colorxy", "Color XY")
	gotXY, planned := registry["color-xy"].Plan(xyInput, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("color-xy strategy omitted the eligible composite")
	}
	assertEntityPlanParity(t, *wantXY, gotXY)
	hsInput := strategyLightFeatureInput(t, device, "composite", "color_hs", "colorhs", "Color Hue/Saturation")
	gotHS, planned := registry["color-hs"].Plan(hsInput, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("color-hs strategy omitted the eligible composite")
	}
	assertEntityPlanParity(t, *wantHS, gotHS)
	// Both representations share one color property while staying distinct Entities.
	if gotXY.StateProperties[0] != "color" || gotHS.StateProperties[0] != "color" {
		t.Fatalf("shared color property lost: %v / %v", gotXY.StateProperties, gotHS.StateProperties)
	}
	// A complete XY report is active exactly when the same-message mode is xy.
	active, present, err := gotXY.DecodeState("entity-test", map[string]json.RawMessage{
		"color":      json.RawMessage(`{"x":0.5,"y":0.25}`),
		"color_mode": json.RawMessage(`"xy"`),
	}, time.Now().UTC())
	if err != nil || !present {
		t.Fatalf("strategy XY omitted a complete active report: %v/%v", present, err)
	}
	wantActive, wantPresent, err := wantXY.DecodeState("entity-test", map[string]json.RawMessage{
		"color":      json.RawMessage(`{"x":0.5,"y":0.25}`),
		"color_mode": json.RawMessage(`"xy"`),
	}, time.Now().UTC())
	if err != nil || !wantPresent {
		t.Fatalf("handwritten XY omitted a complete active report: %v/%v", wantPresent, err)
	}
	if !reflect.DeepEqual(wantActive.semantic, active.semantic) {
		t.Fatalf("XY semantic diverged: %#v vs %#v", wantActive.semantic, active.semantic)
	}
}

// This test protects derived color-mode strategy parity and fails if the
// companion property, foreign-claim checks, or read-only same-message mode
// state diverge from the handwritten behavior. The defect would be a mode
// Entity that guesses across colliding properties or appears without a
// surviving color capability.
func TestColorModeProfileStrategyMatchesHandwrittenColorMode(t *testing.T) {
	t.Parallel()
	device := colorStrategyTestDevice()
	planningInput, root, mode := colorStrategyPlanningInput(device)
	temperature := planColorTemp(planningInput, root, hasColorComposite(root), mode)
	if temperature == nil {
		t.Fatal("handwritten color-temperature plan is unexpectedly ineligible")
	}
	want := planColorMode(device.IEEEAddress, root, mode, true)
	if want == nil {
		t.Fatal("handwritten color-mode plan is unexpectedly ineligible")
	}
	index := newExposeIndex(device)
	input := profileStrategyInput{
		IEEE: device.IEEEAddress, Root: root, Expose: nil, Index: index,
		Identity: profileEntityIdentity{Key: "colormode", Name: "Color Mode"},
		Prior:    map[string]entityPlan{"test.colortemp": *temperature},
	}
	definition := defaultProfileStrategyRegistry()["color-mode"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("color-mode strategy omitted the eligible derived companion")
	}
	assertEntityPlanParity(t, *want, got)
	if got.TranslateCommand != nil || len(got.GetProperties) != 0 {
		t.Fatal("color mode must stay read-only with no refresh properties")
	}
	report, present := decodeStrategyState(t, got, "color_mode", `"xy"`)
	if !present || report.semantic == nil {
		t.Fatal("color mode omitted a known reported mode")
	}
	// Without a surviving color sibling the mode Entity is omitted.
	orphan := input
	orphan.Prior = map[string]entityPlan{}
	if _, orphanPlanned := definition.Plan(orphan, emptyProfileStrategyParameters{}); orphanPlanned {
		t.Fatal("color-mode strategy planned without a surviving color sibling")
	}
}

// This test protects startup color-temperature strategy parity and fails if
// the exact-integer mired bounds or the previous to 65535 sentinel mapping
// diverge from the handwritten behavior.
func TestStartupColorTemperatureProfileStrategyMatchesHandwrittenStartup(t *testing.T) {
	t.Parallel()
	startupMin, startupMax := 150.0, 500.0
	device := strategyTestDevice(colorLightExpose("", "state", "",
		upstreamExpose{
			Type: "numeric", Name: startupColorTempExposeName, Property: "color_temp_startup", Access: 7,
			ValueMin: &startupMin, ValueMax: &startupMax,
			Presets: []upstreamPreset{{Name: "previous", Value: startupPreviousWireValue}},
		},
	))
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	root := planningInput.Exposes.Roots("light")[0]
	want := planStartupColorTemp(planningInput, root)
	if want == nil {
		t.Fatal("handwritten startup plan is unexpectedly ineligible")
	}
	input := strategyLightFeatureInput(t, device, "numeric", startupColorTempExposeName,
		"startupcolortemp", "Startup Color Temperature")
	definition := defaultProfileStrategyRegistry()["startup-color-temperature"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("startup-color-temperature strategy omitted the eligible feature")
	}
	assertEntityPlanParity(t, *want, got)
	report, _ := decodeStrategyState(t, got, "color_temp_startup", `65535`)
	if report.semantic == nil {
		t.Fatal("startup sentinel did not decode")
	}
}

// This test protects temperature strategy parity and fails if the Celsius
// eligibility or exact milli-Celsius conversion diverge from the
// handwritten sensor behavior.
func TestTemperatureProfileStrategyMatchesHandwrittenTemperature(t *testing.T) {
	t.Parallel()
	device := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "temperature", Property: "temperature",
		Access: exposePublishAccessBit | exposeGetAccessBit, Unit: "°C",
	})
	input := strategyRootInput(t, device, "numeric", "temperature", "temperature", "Temperature")
	definition := defaultProfileStrategyRegistry()["temperature"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("temperature strategy omitted the eligible root")
	}
	want, err := newTemperaturePlan(adapter.EntityMetadata{
		Key: "temperature", ExternalID: device.IEEEAddress + "/root/temperature", Name: "Temperature",
	}, "temperature", true)
	if err != nil {
		t.Fatalf("handwritten temperature constructor failed: %v", err)
	}
	assertEntityPlanParity(t, want, got)
	report, _ := decodeStrategyState(t, got, "temperature", `21.5`)
	if report.semantic != int64(21500) {
		t.Fatalf("temperature 21.5 decoded to %v, want 21500 milli-Celsius", report.semantic)
	}
	if got.TranslateCommand != nil {
		t.Fatal("temperature must stay read-only")
	}
	// A Fahrenheit root is ineligible instead of converted.
	fahrenheit := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "temperature", Property: "temperature",
		Access: exposePublishAccessBit | exposeGetAccessBit, Unit: "°F",
	})
	fahrenheitInput := strategyRootInput(t, fahrenheit, "numeric", "temperature", "temperature", "Temperature")
	_, fahrenheitPlanned := definition.Plan(fahrenheitInput, emptyProfileStrategyParameters{})
	if fahrenheitPlanned {
		t.Fatal("temperature strategy planned a non-Celsius unit")
	}
}

// This test protects enum-setting strategy parity and fails if choices,
// observed matching, or full publish/set/get eligibility diverge from the
// handwritten power-on behavior.
func TestEnumSettingProfileStrategyMatchesPowerOnBehavior(t *testing.T) {
	t.Parallel()
	device := strategyTestDevice(
		lightExpose("", "state", "brightness"),
		upstreamExpose{
			Type: "enum", Name: "power_on_behavior", Property: "power_on_behavior",
			Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
			Values: []string{"off", "on", "toggle", "previous"},
		},
	)
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	want := planPowerOnBehavior(planningInput)
	if want == nil {
		t.Fatal("handwritten power-on behavior is unexpectedly ineligible")
	}
	input := strategyRootInput(t, device, "enum", "power_on_behavior", "poweronbehavior", "Power-On Behavior")
	definition := defaultProfileStrategyRegistry()["enum-setting"]
	got, planned := definition.Plan(input, emptyProfileStrategyParameters{})
	if !planned {
		t.Fatal("enum-setting strategy omitted the eligible root")
	}
	assertEntityPlanParity(t, *want, got)
	report, _ := decodeStrategyState(t, got, "power_on_behavior", `"previous"`)
	if report.semantic == nil {
		t.Fatal("enum setting omitted a discovered choice")
	}
	if _, _, err := got.DecodeState("e",
		map[string]json.RawMessage{"power_on_behavior": json.RawMessage(`"turbo"`)},
		time.Now().UTC()); err == nil {
		t.Fatal("enum setting accepted an off-choices value")
	}
}

// This test protects enum-action strategy parity and fails if the effect
// Entity diverges from the handwritten stateless dispatched behavior.
func TestEnumActionProfileStrategyMatchesEffect(t *testing.T) {
	t.Parallel()
	values := []string{"blink", "breathe", "okay"}
	device := strategyTestDevice(
		lightExpose("", "state", "brightness"),
		upstreamExpose{
			Type: "enum", Name: "effect", Property: "effect",
			Access: exposeSetAccessBit, Values: values,
		},
	)
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	want := planEffect(planningInput)
	if want == nil {
		t.Fatal("handwritten effect plan is unexpectedly ineligible")
	}
	input := strategyRootInput(t, device, "enum", "effect", "effect", "Effect")
	definition := defaultProfileStrategyRegistry()["enum-action"]
	parameters, err := definition.CompileParameters(json.RawMessage(`{"access":"set-only"}`))
	if err != nil {
		t.Fatalf("set-only parameters rejected: %v", err)
	}
	got, planned := definition.Plan(input, parameters)
	if !planned {
		t.Fatal("enum-action strategy omitted the eligible set-only root")
	}
	assertEntityPlanParity(t, *want, got)
	if got.StatePolicy != entityStateless || got.DecodeState != nil {
		t.Fatal("effect must stay stateless with no decoder")
	}
	triggered, err := got.TranslateCommand(context.Background(), "e",
		testTriggerCommand("e", `{"name":"breathe"}`), strategyResponder())
	if err != nil {
		t.Fatalf("effect trigger failed: %v", err)
	}
	if triggered.Outcome != plannedDispatched || len(triggered.GetProperties) != 0 ||
		triggered.Matches != nil {
		t.Fatal("effect trigger must complete as dispatched with no refresh or matcher")
	}
	if string(triggered.SetValues["effect"]) != `"breathe"` {
		t.Fatalf("effect payload = %s, want the exact choice", triggered.SetValues["effect"])
	}
	// Set-only rejects a root that also publishes.
	publishing := strategyTestDevice(upstreamExpose{
		Type: "enum", Name: "effect", Property: "effect",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit, Values: values,
	})
	publishingInput := strategyRootInput(t, publishing, "enum", "effect", "effect", "Effect")
	if _, publishingPlanned := definition.Plan(publishingInput, parameters); publishingPlanned {
		t.Fatal("set-only enum-action planned a root with publish access")
	}
	includes, err := definition.CompileParameters(json.RawMessage(`{"access":"includes-set"}`))
	if err != nil {
		t.Fatalf("includes-set parameters rejected: %v", err)
	}
	if _, includesPlanned := definition.Plan(publishingInput, includes); !includesPlanned {
		t.Fatal("includes-set enum-action omitted a root granting set")
	}
}

// This test protects numeric-setting strategy parity and fails if bounds,
// unit, access, value-mode state, or observed matching diverge from the
// handwritten smart-plug setting behavior.
func TestNumericSettingProfileStrategyMatchesSmartPlugSetting(t *testing.T) {
	t.Parallel()
	device := mustPlugDevice(t)
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	specs := smartPlugNumericSettings()
	want := planNumericSetting(planningInput, specs[0])
	if want == nil {
		t.Fatal("handwritten LED brightness setting is unexpectedly ineligible")
	}
	input := strategyRootInput(t, device, "numeric", "led_brightness", "ledbrightness", "LED Brightness")
	definition := defaultProfileStrategyRegistry()["numeric-setting"]
	parameters, err := definition.CompileParameters(json.RawMessage(`{"accepted_units":["%"],"unit":"%"}`))
	if err != nil {
		t.Fatalf("numeric-setting parameters rejected: %v", err)
	}
	got, planned := definition.Plan(input, parameters)
	if !planned {
		t.Fatal("numeric-setting strategy omitted the eligible setting")
	}
	assertEntityPlanParity(t, *want, got)
	report, _ := decodeStrategyState(t, got, "led_brightness", `50`)
	if report.semantic == nil {
		t.Fatal("numeric setting omitted an in-range value")
	}
	translated, err := got.TranslateCommand(context.Background(), "e",
		testCommand("e", `{"mode":"value","value":75}`), strategyResponder())
	if err != nil {
		t.Fatalf("numeric setting command failed: %v", err)
	}
	if string(translated.SetValues["led_brightness"]) != `75` {
		t.Fatalf("setting payload = %s, want 75", translated.SetValues["led_brightness"])
	}
	if len(translated.GetProperties) == 0 || translated.Matches == nil {
		t.Fatal("numeric setting command must request refresh with an observed matcher")
	}
}

// This test protects generic numeric-sensor parity with the handwritten
// electrical sensors and fails if unit gating, publish-without-set access,
// bounds, or float decoding diverge.
func TestNumericSensorProfileStrategyMatchesElectricalSensor(t *testing.T) {
	t.Parallel()
	device := mustPlugDevice(t)
	planningInput := devicePlanningInput{IEEE: device.IEEEAddress, Exposes: newExposeIndex(device)}
	var spec electricalSensorSpec
	for _, candidate := range smartPlugElectricalSensors() {
		if candidate.exposeName == "voltage" {
			spec = candidate
		}
	}
	want := planElectricalSensor(planningInput, spec)
	if want == nil {
		t.Fatal("handwritten voltage sensor is unexpectedly ineligible")
	}
	input := strategyRootInput(t, device, "numeric", "voltage", "voltage", "Voltage")
	definition := defaultProfileStrategyRegistry()["numeric-sensor"]
	parameters, err := definition.CompileParameters(json.RawMessage(
		`{"accepted_units":["V"],"unit":"V","number_format":"float",` +
			`"bounds":{"mode":"upstream-or-fallback","minimum":0,"maximum":1000000}}`))
	if err != nil {
		t.Fatalf("numeric-sensor parameters rejected: %v", err)
	}
	got, planned := definition.Plan(input, parameters)
	if !planned {
		t.Fatal("numeric-sensor strategy omitted the eligible voltage root")
	}
	assertEntityPlanParity(t, *want, got)
	report, _ := decodeStrategyState(t, got, "voltage", `230.5`)
	if report.semantic != 230.5 {
		t.Fatalf("voltage 230.5 decoded to %v, want the preserved fraction", report.semantic)
	}
	if got.TranslateCommand != nil {
		t.Fatal("voltage sensor must stay read-only")
	}
}

// syntheticProfileCatalog compiles one in-memory planner profile against
// the production registry and returns the catalog. It proves a new mapping
// needs only JSON plus a fixture: the synthetic expose names below have no
// mapping-specific Go branch.
func syntheticProfileCatalog(t *testing.T, document string) *ProfileCatalog {
	t.Helper()
	catalog, err := compileProfileCatalogFiles(
		[]profileCatalogFile{{path: "profiles/synthetic.profile.json", data: []byte(document)}},
		defaultProfileStrategyRegistry(),
	)
	if err != nil {
		t.Fatalf("synthetic profile rejected: %v", err)
	}
	return catalog
}

func syntheticPlannerDocument(rule, strategy, params, key, name string) string {
	return `{"version":1,"kind":"planner-profile","id":"synthetic","order":30,` +
		`"contribution":{"device_kind":"sensor","role":"supplemental"},` +
		`"candidate_groups":[{"id":"synthetic.group","root":{"type":"numeric"},"entities":[` +
		`{"id":"` + rule + `","source":{"kind":"root"},` +
		`"identity":{"key":"` + key + `","name":"` + name + `"},` +
		`"strategy":{"name":"` + strategy + `","parameters":` + params + `}}]}]}`
}

func syntheticStrategyPlan(
	t *testing.T,
	catalog *ProfileCatalog,
	rule, strategy string,
	input profileStrategyInput,
) entityPlan {
	t.Helper()
	compiled, ok := catalog.profiles[0].ruleParameters[rule]
	if !ok {
		t.Fatalf("rule %q missing from synthetic catalog", rule)
	}
	definition, ok := catalog.strategies[strategy]
	if !ok {
		t.Fatalf("strategy %q missing from synthetic catalog", strategy)
	}
	plan, planned := definition.Plan(input, compiled.Parameters)
	if !planned {
		t.Fatalf("synthetic %s rule omitted its eligible expose", strategy)
	}
	return plan
}

// This test protects generic numeric-sensor mapping agility and fails if a
// synthetic soil-moisture mapping needs anything beyond an in-memory JSON
// profile and fixture. Soil moisture has no Go allowlist entry, so planning
// it proves no mapping-specific Go branch is required.
func TestNumericSensorProfileStrategyPlansSyntheticMapping(t *testing.T) {
	t.Parallel()
	catalog := syntheticProfileCatalog(t, syntheticPlannerDocument("synthetic.soil", "numeric-sensor",
		`{"accepted_units":["%"],"unit":"%","number_format":"float",`+
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`,
		"soilmoisture", "Soil Moisture"))
	device := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "soil_moisture", Property: "soil_moisture",
		Access: exposePublishAccessBit | exposeGetAccessBit, Unit: "%",
	})
	input := strategyRootInput(t, device, "numeric", "soil_moisture", "soilmoisture", "Soil Moisture")
	plan := syntheticStrategyPlan(t, catalog, "synthetic.soil", "numeric-sensor", input)
	if plan.Descriptor.Key != "soilmoisture" ||
		plan.Descriptor.ExternalID != device.IEEEAddress+"/root/soilmoisture" {
		t.Fatalf("synthetic identity = %q/%q", plan.Descriptor.Key, plan.Descriptor.ExternalID)
	}
	if support := string(plan.Descriptor.Support); !strings.Contains(support, `"unit":"%"`) {
		t.Fatalf("synthetic support lost the profile unit: %s", support)
	}
	report, present := decodeStrategyState(t, plan, "soil_moisture", `42.5`)
	if !present || report.semantic != 42.5 {
		t.Fatalf("soil moisture 42.5 decoded to %v/%v", report.semantic, present)
	}
	if plan.TranslateCommand != nil {
		t.Fatal("synthetic sensor must stay read-only")
	}
	if got := plan.GetProperties; len(got) != 1 || got[0] != "soil_moisture" {
		t.Fatalf("get properties = %v, want the upstream get refresh", got)
	}
	// A mismatched unit omits only that candidate without a Go branch.
	mismatched := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "soil_moisture", Property: "soil_moisture",
		Access: exposePublishAccessBit | exposeGetAccessBit, Unit: "ppm",
	})
	mismatchedInput := strategyRootInput(
		t, mismatched, "numeric", "soil_moisture", "soilmoisture", "Soil Moisture",
	)
	definition := catalog.strategies["numeric-sensor"]
	compiled := catalog.profiles[0].ruleParameters["synthetic.soil"]
	if _, mismatchedPlanned := definition.Plan(mismatchedInput, compiled.Parameters); mismatchedPlanned {
		t.Fatal("numeric-sensor planned an unaccepted unit")
	}
}

// This test protects generic integer numeric-sensor decoding and fails if
// exact integers are not required or fixed bounds are not honored for a
// synthetic air-quality mapping.
func TestNumericSensorProfileStrategyDecodesSyntheticIntegers(t *testing.T) {
	t.Parallel()
	catalog := syntheticProfileCatalog(t, syntheticPlannerDocument("synthetic.aqi", "numeric-sensor",
		`{"accepted_units":["aqi"],"unit":"aqi","number_format":"integer",`+
			`"bounds":{"mode":"fixed","minimum":0,"maximum":500}}`,
		"airquality", "Air Quality"))
	device := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "air_quality_index", Property: "air_quality_index",
		Access: exposePublishAccessBit, Unit: "aqi",
	})
	input := strategyRootInput(t, device, "numeric", "air_quality_index", "airquality", "Air Quality")
	plan := syntheticStrategyPlan(t, catalog, "synthetic.aqi", "numeric-sensor", input)
	report, _ := decodeStrategyState(t, plan, "air_quality_index", `42`)
	if report.semantic != float64(42) {
		t.Fatalf("integer 42 decoded to %v", report.semantic)
	}
	if _, _, err := plan.DecodeState("e",
		map[string]json.RawMessage{"air_quality_index": json.RawMessage(`42.5`)},
		time.Now().UTC()); err == nil {
		t.Fatal("integer sensor accepted a fraction")
	}
	if len(plan.GetProperties) != 0 {
		t.Fatalf("publish-only sensor must not refresh, got %v", plan.GetProperties)
	}
}

// This test protects generic numeric-setting mapping agility and fails if a
// synthetic valve-position mapping needs anything beyond an in-memory JSON
// profile and fixture.
func TestNumericSettingProfileStrategyPlansSyntheticMapping(t *testing.T) {
	t.Parallel()
	catalog := syntheticProfileCatalog(t, syntheticPlannerDocument("synthetic.valve", "numeric-setting",
		`{"accepted_units":["%"],"unit":"%"}`, "valveposition", "Valve Position"))
	minimum, maximum := 0.0, 100.0
	device := strategyTestDevice(upstreamExpose{
		Type: "numeric", Name: "valve_position", Property: "valve_position",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit, Unit: "%",
		ValueMin: &minimum, ValueMax: &maximum,
	})
	input := strategyRootInput(t, device, "numeric", "valve_position", "valveposition", "Valve Position")
	plan := syntheticStrategyPlan(t, catalog, "synthetic.valve", "numeric-setting", input)
	report, _ := decodeStrategyState(t, plan, "valve_position", `62.5`)
	if report.semantic == nil {
		t.Fatal("valve position omitted an in-range fraction")
	}
	planned, err := plan.TranslateCommand(context.Background(), "e",
		testCommand("e", `{"mode":"value","value":62.5}`), strategyResponder())
	if err != nil {
		t.Fatalf("valve command failed: %v", err)
	}
	if string(planned.SetValues["valve_position"]) != `62.5` {
		t.Fatalf("valve payload = %s, want the preserved fraction", planned.SetValues["valve_position"])
	}
	if planned.Matches == nil {
		t.Fatal("valve command must install an observed matcher")
	}
}

// This test protects generic enum-setting mapping agility and fails if a
// synthetic fan-mode mapping needs anything beyond an in-memory JSON
// profile and fixture.
func TestEnumSettingProfileStrategyPlansSyntheticMapping(t *testing.T) {
	t.Parallel()
	catalog := syntheticProfileCatalog(t, syntheticPlannerDocument("synthetic.fan", "enum-setting",
		`{}`, "fanmode", "Fan Mode"))
	device := strategyTestDevice(upstreamExpose{
		Type: "enum", Name: "fan_mode", Property: "fan_mode",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
		Values: []string{"auto", "quiet", "turbo"},
	})
	input := strategyRootInput(t, device, "enum", "fan_mode", "fanmode", "Fan Mode")
	plan := syntheticStrategyPlan(t, catalog, "synthetic.fan", "enum-setting", input)
	support := string(plan.Descriptor.Support)
	if !strings.Contains(support, `"choices":["auto","quiet","turbo"]`) {
		t.Fatalf("fan support lost the expose choices: %s", support)
	}
	report, _ := decodeStrategyState(t, plan, "fan_mode", `"quiet"`)
	if report.semantic == nil {
		t.Fatal("fan mode omitted a discovered choice")
	}
	planned, err := plan.TranslateCommand(context.Background(), "e",
		testCommand("e", `{"value":"turbo"}`), strategyResponder())
	if err != nil {
		t.Fatalf("fan command failed: %v", err)
	}
	if string(planned.SetValues["fan_mode"]) != `"turbo"` {
		t.Fatalf("fan payload = %s, want the exact choice", planned.SetValues["fan_mode"])
	}
}

// This test protects generic enum-action mapping agility and fails if a
// synthetic self-test mapping needs anything beyond an in-memory JSON
// profile and fixture. The includes-set case mirrors plugs that advertise
// full access on a dispatched action.
func TestEnumActionProfileStrategyPlansSyntheticMapping(t *testing.T) {
	t.Parallel()
	catalog := syntheticProfileCatalog(t, syntheticPlannerDocument("synthetic.test", "enum-action",
		`{"access":"includes-set"}`, "selftest", "Self Test"))
	device := strategyTestDevice(upstreamExpose{
		Type: "enum", Name: "self_test", Property: "self_test",
		Access: exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
		Values: []string{"run", "cancel"},
	})
	input := strategyRootInput(t, device, "enum", "self_test", "selftest", "Self Test")
	plan := syntheticStrategyPlan(t, catalog, "synthetic.test", "enum-action", input)
	if plan.StatePolicy != entityStateless {
		t.Fatal("synthetic action must be stateless")
	}
	planned, err := plan.TranslateCommand(context.Background(), "e",
		testTriggerCommand("e", `{"name":"run"}`), strategyResponder())
	if err != nil {
		t.Fatalf("self-test trigger failed: %v", err)
	}
	if planned.Outcome != plannedDispatched {
		t.Fatal("self-test must complete as dispatched")
	}
	if string(planned.SetValues["self_test"]) != `"run"` {
		t.Fatalf("self-test payload = %s, want the exact choice", planned.SetValues["self_test"])
	}
	if _, err = plan.TranslateCommand(context.Background(), "e",
		testTriggerCommand("e", `{"name":"wipe"}`), strategyResponder()); err == nil {
		t.Fatal("self-test accepted an off-values choice")
	}
}
