package zigbee2mqtt //nolint:testpackage // Catalog compiler tests exercise package-private compilation and error evidence.

// This test protects the D2 catalog loading and semantic compiler layer and
// fails if any stable error code loses its deterministic document, profile,
// rule, or JSON pointer evidence. The oracle is the approved
// zigbee2mqtt-json-profile-catalog spec: every error case below names the
// spec rule it guards and the plausible defect that would produce it.

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"strconv"
	"strings"
	"testing"
)

// testProfileStrategyRegistry returns an injected registry with test-only
// parameter compilers over the real strategy names. Files-level documents must
// use real names to pass the authoritative schema; the registry behavior stays
// generic so no production strategy semantics leak into this layer.
func testProfileStrategyRegistry() profileStrategyRegistry {
	return profileStrategyRegistry{
		profileStrategyBinaryPower: {
			Name:               profileStrategyBinaryPower,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot, profileEntitySourceFeature},
			CompileParameters:  testEmptyStrategyParameters,
		},
		profileStrategyBrightness: {
			Name:               profileStrategyBrightness,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceRoot},
			CompileParameters:  testEmptyStrategyParameters,
		},
		profileStrategyNumericSensor: {
			Name:               profileStrategyNumericSensor,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  testNumericSensorStrategyParameters,
		},
		profileStrategyEnumAction: {
			Name:               profileStrategyEnumAction,
			AllowedSourceKinds: []profileEntitySourceKind{profileEntitySourceFeature},
			CompileParameters:  testEnumActionStrategyParameters,
		},
	}
}

// testEmptyStrategyParameters accepts only the exact empty parameter object
// used by strategies without profile-controlled parameters.
func testEmptyStrategyParameters(raw json.RawMessage) (any, error) {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if len(params) != 0 {
		return nil, fmt.Errorf("test strategy expects exactly {}, got %d fields", len(params))
	}
	return struct{}{}, nil
}

// testNumericSensorStrategyParameters mirrors the generic shape check the
// production registry will own: configured bounds must satisfy minimum below
// maximum. JSON numbers are always finite, so the comparison is the semantic
// rule the schema cannot express.
func testNumericSensorStrategyParameters(raw json.RawMessage) (any, error) {
	var params struct {
		Bounds struct {
			Mode    string  `json:"mode"`
			Minimum float64 `json:"minimum"`
			Maximum float64 `json:"maximum"`
		} `json:"bounds"`
	}
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	if params.Bounds.Minimum >= params.Bounds.Maximum {
		return nil, fmt.Errorf("test strategy requires minimum %v below maximum %v",
			params.Bounds.Minimum, params.Bounds.Maximum)
	}
	return params, nil
}

// testEnumActionStrategyParameters accepts any decoded parameter object; the
// schema already restricts access to its closed enum.
func testEnumActionStrategyParameters(raw json.RawMessage) (any, error) {
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		return nil, err
	}
	return params, nil
}

// testRootRuleJSON builds one root-source rule using the parameterless test
// strategy.
func testRootRuleJSON(ruleID, key, name string) string {
	return `{"id":"` + ruleID + `","source":{"kind":"root"}` +
		`,"identity":{"key":"` + key + `","name":"` + name + `"}` +
		`,"strategy":{"name":"binary-power","parameters":{}}}`
}

// testFeatureRuleJSON builds one feature-source rule with an explicit strategy
// and raw parameter object.
func testFeatureRuleJSON(ruleID, sourceType, sourceName, strategy, params, key, name string) string {
	return `{"id":"` + ruleID + `","source":{"kind":"feature","type":"` + sourceType + `","name":"` + sourceName + `"}` +
		`,"identity":{"key":"` + key + `","name":"` + name + `"}` +
		`,"strategy":{"name":"` + strategy + `","parameters":` + params + `}}`
}

// testCandidateGroupJSON builds one candidate group fragment.
func testCandidateGroupJSON(groupID, rootType, gate, entities string) string {
	gateFragment := ""
	if gate != "" {
		gateFragment = `,"gate_rule":"` + gate + `"`
	}
	return `{"id":"` + groupID + `","root":{"type":"` + rootType + `"}` + gateFragment + `,"entities":[` + entities + `]}`
}

// testPlannerDocJSON builds one minimal planner profile with a relay primary
// contribution and no device entities.
func testPlannerDocJSON(docID string, order int, groups string) string {
	return `{"version":1,"kind":"planner-profile","id":"` + docID + `","order":` + strconv.Itoa(order) +
		`,"contribution":{"device_kind":"relay","role":"primary"},"candidate_groups":[` + groups + `]}`
}

// testPlannerDocWithDevicesJSON builds one planner profile with device entities.
func testPlannerDocWithDevicesJSON(docID string, order int, groups, devices string) string {
	return `{"version":1,"kind":"planner-profile","id":"` + docID + `","order":` + strconv.Itoa(order) +
		`,"contribution":{"device_kind":"relay","role":"primary"},"candidate_groups":[` + groups +
		`],"device_entities":[` + devices + `]}`
}

// testDeviceRuleJSON builds one device-entity rule fragment with an enum
// expose match.
func testDeviceRuleJSON(ruleID, exposeName, survivor, key, name string) string {
	return `{"id":"` + ruleID + `","expose":{"type":"enum","name":"` + exposeName + `"}` +
		`,"requires_group_survivor":"` + survivor + `"` +
		`,"identity":{"key":"` + key + `","name":"` + name + `"}` +
		`,"strategy":{"name":"binary-power","parameters":{}}}`
}

// testOverrideDocJSON builds one override document with an exact vendor and
// model selector and optional exact build IDs.
func testOverrideDocJSON(docID, vendor, model string, builds []string, patches string) string {
	buildsFragment := ""
	if len(builds) > 0 {
		quoted := make([]string, 0, len(builds))
		for _, build := range builds {
			quoted = append(quoted, `"`+build+`"`)
		}
		buildsFragment = `,"software_build_ids":[` + strings.Join(quoted, ",") + `]`
	}
	return `{"version":1,"kind":"profile-override","id":"` + docID +
		`","selector":{"vendor":"` + vendor + `","model":"` + model + `"` + buildsFragment +
		`},"patches":[` + patches + `]}`
}

// testRelayPlannerDoc is the shared baseline planner: one gated switch group
// with a power rule plus one power-on-behavior device rule.
func testRelayPlannerDoc() string {
	groups := testCandidateGroupJSON("relay.roots", "switch", "relay.power",
		testFeatureRuleJSON("relay.power", "binary", "state", "binary-power", `{}`, "power", "Power"))
	devices := testDeviceRuleJSON(
		"relay.power-on-behavior",
		"power_on_behavior",
		"relay.roots",
		"poweronbehavior",
		"Power-On Behavior",
	)
	return testPlannerDocWithDevicesJSON("relay", 20, groups, devices)
}

// compileTestProfileFiles compiles one in-memory file set with the test
// registry. Files compile in lexical path order regardless of map order.
func compileTestProfileFiles(t *testing.T, docs map[string]string) (*ProfileCatalog, error) {
	t.Helper()
	files := make([]profileCatalogFile, 0, len(docs))
	for path, body := range docs {
		files = append(files, profileCatalogFile{path: path, data: []byte(body)})
	}
	return compileProfileCatalogFiles(files, testProfileStrategyRegistry())
}

// requireProfileCatalogError asserts the exact stable error code and its
// deterministic document, profile, rule, and pointer evidence. It fails if a
// partial catalog accompanies the error.
func requireProfileCatalogError(
	t *testing.T,
	catalog *ProfileCatalog,
	err error,
	code, document, profileID, ruleID, pointer string,
) *ProfileCatalogError {
	t.Helper()
	if catalog != nil {
		t.Fatalf("expected no partial catalog with %s error, got %+v", code, catalog)
	}
	if err == nil {
		t.Fatalf("expected %s error, got nil", code)
	}
	var catalogErr *ProfileCatalogError
	if !errors.As(err, &catalogErr) {
		t.Fatalf("expected *ProfileCatalogError with code %s, got %T: %v", code, err, err)
	}
	if catalogErr.Code != code {
		t.Fatalf("expected error code %q, got %q (%v)", code, catalogErr.Code, catalogErr)
	}
	if catalogErr.Document != document {
		t.Fatalf("expected document %q, got %q (%v)", document, catalogErr.Document, catalogErr)
	}
	if catalogErr.ProfileID != profileID {
		t.Fatalf("expected profile %q, got %q (%v)", profileID, catalogErr.ProfileID, catalogErr)
	}
	if catalogErr.RuleID != ruleID {
		t.Fatalf("expected rule %q, got %q (%v)", ruleID, catalogErr.RuleID, catalogErr)
	}
	if catalogErr.JSONPointer != pointer {
		t.Fatalf("expected pointer %q, got %q (%v)", pointer, catalogErr.JSONPointer, catalogErr)
	}
	return catalogErr
}

// This test protects embedded catalog startup validity and fails if the loader
// rejects the repository-owned light, relay, sensor, and linkquality documents or
// returns a catalog that is indistinguishable from the invalid zero value.
func TestLoadEmbeddedProfileCatalogCompilesSensorProfiles(t *testing.T) {
	t.Parallel()
	catalog, err := LoadEmbeddedProfileCatalog()
	if err != nil {
		t.Fatalf("expected embedded light, relay, and sensor catalog to compile, got %v", err)
	}
	if catalog == nil {
		t.Fatal("expected non-nil catalog for the embedded set")
	}
	if !catalog.loaded {
		t.Fatal("expected loader-produced catalog to be marked loaded")
	}
	if len(catalog.profiles) != 4 || len(catalog.overrides) != 0 {
		t.Fatalf("expected 4 profiles and 0 overrides, got %d and %d", len(catalog.profiles), len(catalog.overrides))
	}
	for index, want := range []struct {
		id    string
		order int
	}{
		{"light", 10},
		{"relay", 20},
		{"ambient-sensors", 30},
		{"linkquality", 40},
	} {
		got := catalog.profiles[index].document
		if got.ID != want.id || got.Order != want.order {
			t.Fatalf("embedded profile %d = %q order %d, want %q order %d",
				index, got.ID, got.Order, want.id, want.order)
		}
	}
	var zero ProfileCatalog
	if zero.loaded {
		t.Fatal("expected zero-value ProfileCatalog to remain invalid")
	}
}

// This test protects explicit order precedence and fails if the compiled
// catalog retains file order instead of ascending profile order.
func TestCompileProfileCatalogSortsProfilesByOrder(t *testing.T) {
	t.Parallel()
	groups := func(id string) string {
		return testCandidateGroupJSON(id+".roots", "switch", "", testRootRuleJSON(id+".power", "power", "Power"))
	}
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/c-third.profile.json":  testPlannerDocJSON("c-third", 30, groups("c-third")),
		"profiles/a-first.profile.json":  testPlannerDocJSON("a-first", 10, groups("a-first")),
		"profiles/b-second.profile.json": testPlannerDocJSON("b-second", 20, groups("b-second")),
	})
	if err != nil {
		t.Fatalf("expected ordered catalog to compile, got %v", err)
	}
	if catalog == nil || !catalog.loaded {
		t.Fatal("expected loaded catalog")
	}
	want := []string{"a-first", "b-second", "c-third"}
	if len(catalog.profiles) != len(want) {
		t.Fatalf("expected %d profiles, got %d", len(want), len(catalog.profiles))
	}
	for i, id := range want {
		if catalog.profiles[i].document.ID != id {
			t.Fatalf("expected profile %d to be %q, got %q", i, id, catalog.profiles[i].document.ID)
		}
	}
	params, ok := catalog.profiles[0].ruleParameters["a-first.power"]
	if !ok {
		t.Fatal("expected compiled parameters for rule a-first.power")
	}
	if params.Strategy != profileStrategyBinaryPower {
		t.Fatalf("expected strategy binary-power, got %q", params.Strategy)
	}
}

// This test protects fail-fast compilation and fails if an invalid document
// yields a usable partial catalog alongside the error.
func TestCompileProfileCatalogFailsWithoutPartialCatalog(t *testing.T) {
	t.Parallel()
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/a-valid.profile.json": testPlannerDocJSON("a-valid", 10,
			testCandidateGroupJSON("a-valid.roots", "switch", "", testRootRuleJSON("a-valid.power", "power", "Power"))),
		"profiles/b-clash.profile.json": testPlannerDocJSON("b-clash", 10,
			testCandidateGroupJSON("b-clash.roots", "switch", "", testRootRuleJSON("b-clash.power", "power", "Power"))),
	})
	requireProfileCatalogError(t, catalog, err,
		profileCatalogErrorDuplicateProfileOrder, "profiles/b-clash.profile.json", "b-clash", "", "/order")
}

// This test protects the authoritative schema boundary and fails if a
// shape-violating document compiles or reports a different stable code.
// Oracle: profiles/profile.schema.json as approved in D1.
func TestCompileProfileCatalogSchemaBoundaries(t *testing.T) {
	t.Parallel()
	baseline := testRelayPlannerDoc
	rootBaseline := func() string {
		return testPlannerDocJSON("relay", 20,
			testCandidateGroupJSON("relay.roots", "switch", "", testRootRuleJSON("relay.power", "power", "Power")))
	}
	cases := []struct {
		name     string
		base     func() string
		mutate   func(string) string
		document string
		pointer  string
	}{
		// Plausible defect: a profile author adds an ad-hoc field that the
		// closed contract must reject.
		{name: "unknown top-level field", mutate: func(doc string) string {
			return strings.Replace(doc, `"order":20`, `"order":20,"bogus":true`, 1)
		}, pointer: ""},
		// Plausible defect: a document declares a future contract version the
		// compiler does not implement.
		{name: "wrong version", mutate: func(doc string) string {
			return strings.Replace(doc, `"version":1`, `"version":2`, 1)
		}, pointer: "/version"},
		// Plausible defect: a typo in the document discriminator.
		{name: "wrong kind", mutate: func(doc string) string {
			return strings.Replace(doc, `"kind":"planner-profile"`, `"kind":"planner"`, 1)
		}, pointer: "/kind"},
		// Plausible defect: an uppercase document ID outside the identifier contract.
		{name: "malformed id", mutate: func(doc string) string {
			return strings.Replace(doc, `"id":"relay"`, `"id":"Relay"`, 1)
		}, pointer: "/id"},
		// Plausible defect: off-by-one order bounds on either side.
		{name: "order below minimum", mutate: func(doc string) string {
			return strings.Replace(doc, `"order":20`, `"order":0`, 1)
		}, pointer: "/order"},
		{name: "order above maximum", mutate: func(doc string) string {
			return strings.Replace(doc, `"order":20`, `"order":1001`, 1)
		}, pointer: "/order"},
		// Plausible defect: a parameterless strategy gains an undocumented field.
		{name: "non-empty parameters for parameterless strategy", mutate: func(doc string) string {
			return strings.Replace(
				doc,
				`"binary-power","parameters":{}}`,
				`"binary-power","parameters":{"level":1}}}`,
				1,
			)
		}, pointer: ""},
		// Plausible defect: a feature source omits the nested expose name.
		{name: "feature source without name", mutate: func(doc string) string {
			return strings.Replace(
				doc,
				`"kind":"feature","type":"binary","name":"state"`,
				`"kind":"feature","type":"binary"`,
				1,
			)
		}, pointer: "/candidate_groups/0/entities/0/source/kind"},
		// Plausible defect: a root source carries a forbidden nested field.
		{name: "root source with name", base: rootBaseline, mutate: func(doc string) string {
			return strings.Replace(doc, `"source":{"kind":"root"}`, `"source":{"kind":"root","name":"state"}`, 1)
		}, pointer: "/candidate_groups/0/entities/0/source"},
		// Plausible defect: a derived source names a companion outside version 1.
		{name: "derived source with wrong name", base: rootBaseline, mutate: func(doc string) string {
			return strings.Replace(doc, `"source":{"kind":"root"}`, `"source":{"kind":"derived","name":"hue"}`, 1)
		}, pointer: "/candidate_groups/0/entities/0/source/kind"},
		// Plausible defect: an override patch sets both mutually exclusive fields.
		{
			name:     "override patch with source and expose",
			document: "profiles/overrides/acme-a1.override.json",
			mutate: func(_ string) string {
				return testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					`{"rule":"relay.power","source":{"kind":"root"},"expose":{"type":"switch"}}`)
			},
			pointer: "/patches/0",
		},
		// Plausible defect: a profile declares no candidate groups.
		{name: "empty candidate groups", mutate: func(doc string) string {
			start := strings.Index(doc, `"candidate_groups":[`)
			end := strings.Index(doc, `],"device_entities"`)
			prefix := doc[:start+len(`"candidate_groups":[`)]
			suffix := doc[end+1:]
			return prefix + `]` + suffix
		}, pointer: "/candidate_groups"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			document := "profiles/relay.profile.json"
			if tc.document != "" {
				document = tc.document
			}
			base := baseline
			if tc.base != nil {
				base = tc.base
			}
			docs := map[string]string{document: base()}
			if tc.document != "" {
				docs = map[string]string{
					"profiles/relay.profile.json": baseline(),
					document:                      tc.mutate(""),
				}
			} else {
				docs[document] = tc.mutate(base())
			}
			catalog, err := compileTestProfileFiles(t, docs)
			requireProfileCatalogError(t, catalog, err,
				profileCatalogErrorSchemaInvalid, document, "", "", tc.pointer)
		})
	}
}

// This test protects malformed-document isolation at decode time and fails if
// invalid JSON, an empty file, or trailing data after the first JSON value
// compiles or reports a non-schema code.
func TestCompileProfileCatalogRejectsMalformedDocuments(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		body func() string
	}{
		{name: "invalid JSON", body: func() string { return `{"version":1,` }},
		{name: "empty file", body: func() string { return `` }},
		{name: "whitespace only", body: func() string { return "  \n\t " }},
		{name: "trailing object", body: func() string { return testRelayPlannerDoc() + "\n{}" }},
		{name: "trailing scalar", body: func() string { return testRelayPlannerDoc() + " 1" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := compileTestProfileFiles(t, map[string]string{
				"profiles/relay.profile.json": tc.body(),
			})
			requireProfileCatalogError(t, catalog, err,
				profileCatalogErrorSchemaInvalid, "profiles/relay.profile.json", "", "", "")
		})
	}
}

// This test protects the 256 KiB per-document limit and fails on an off-by-one
// that accepts an oversized document or rejects one exactly at the boundary.
// JSON whitespace padding keeps the padded document schema-valid.
func TestCompileProfileCatalogDocumentSizeBoundary(t *testing.T) {
	t.Parallel()
	padded := func(size int) string {
		base := testRelayPlannerDoc()
		if size < len(base) {
			t.Fatalf("baseline document %d bytes exceeds padding target %d", len(base), size)
		}
		return base + strings.Repeat(" ", size-len(base))
	}
	t.Run("exactly at limit compiles", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/relay.profile.json": padded(profileCatalogMaxDocumentBytes),
		})
		if err != nil {
			t.Fatalf("expected boundary-size document to compile, got %v", err)
		}
		if catalog == nil {
			t.Fatal("expected non-nil catalog at the size boundary")
		}
	})
	t.Run("one byte over limit fails", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/relay.profile.json": padded(profileCatalogMaxDocumentBytes + 1),
		})
		catalogErr := requireProfileCatalogError(t, catalog, err,
			profileCatalogErrorDocumentTooLarge, "profiles/relay.profile.json", "", "", "")
		if len(catalogErr.Error()) > 1000 {
			t.Fatalf(
				"expected compact size diagnostic without document contents, got %d bytes",
				len(catalogErr.Error()),
			)
		}
	})
}

// This test protects the 64-document aggregate limit and fails if the 65th
// lexical document compiles or the overflow names the wrong document.
func TestCompileProfileCatalogDocumentCountBoundary(t *testing.T) {
	t.Parallel()
	minimalDoc := func(id string, order int) string {
		return testPlannerDocJSON(id, order,
			testCandidateGroupJSON(id+".roots", "switch", "", testRootRuleJSON(id+".power", "power", "Power")))
	}
	build := func(count int) map[string]string {
		docs := make(map[string]string, count)
		for i := range count {
			id := fmt.Sprintf("doc-%02d", i+1)
			docs["profiles/"+id+".profile.json"] = minimalDoc(id, i+1)
		}
		return docs
	}
	t.Run("64 documents compile", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestProfileFiles(t, build(64))
		if err != nil {
			t.Fatalf("expected 64 documents to compile, got %v", err)
		}
		if len(catalog.profiles) != 64 {
			t.Fatalf("expected 64 profiles, got %d", len(catalog.profiles))
		}
	})
	t.Run("65 documents fail at the overflow", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestProfileFiles(t, build(65))
		requireProfileCatalogError(t, catalog, err,
			profileCatalogErrorCatalogLimitExceeded, "profiles/doc-65.profile.json", "", "", "")
	})
}

// This test protects the 512-rule aggregate limit and fails on an off-by-one
// that miscounts candidate and device rules together.
func TestCompileProfileCatalogTotalRuleBoundary(t *testing.T) {
	t.Parallel()
	bigGroupRules := func(g int) string {
		rules := make([]string, 0, 64)
		for r := range 64 {
			ruleID := fmt.Sprintf("big.g%d.r%d", g, r)
			rules = append(rules, testRootRuleJSON(ruleID, fmt.Sprintf("k%d", g*64+r), "Entity"))
		}
		return strings.Join(rules, ",")
	}
	bigGroups := func(groups int) string {
		fragments := make([]string, 0, groups)
		for g := range groups {
			fragments = append(
				fragments,
				testCandidateGroupJSON(fmt.Sprintf("big.g%d", g), "switch", "", bigGroupRules(g)),
			)
		}
		return strings.Join(fragments, ",")
	}
	t.Run("512 rules compile", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/big.profile.json": testPlannerDocJSON("big", 10, bigGroups(8)),
		})
		if err != nil {
			t.Fatalf("expected 512 rules to compile, got %v", err)
		}
		if got := len(catalog.profiles[0].ruleParameters); got != 512 {
			t.Fatalf("expected 512 compiled rules, got %d", got)
		}
	})
	t.Run("513 rules fail at the overflow rule", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: the device rule added after 512 candidate rules is
		// excluded from the aggregate count.
		gatedGroups := testCandidateGroupJSON("big.g0", "switch", "big.g0.r0", bigGroupRules(0)) + "," + bigGroupsRest()
		devices := testDeviceRuleJSON("big.extra", "mode", "big.g0", "extra", "Extra")
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/big.profile.json": testPlannerDocWithDevicesJSON("big", 10, gatedGroups, devices),
		})
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorCatalogLimitExceeded,
			"profiles/big.profile.json",
			"big",
			"big.extra",
			"/device_entities/0/id",
		)
	})
}

// bigGroupRestRules builds the rule fragment for one 512-rule boundary group.
func bigGroupRestRules(g int) string {
	rules := make([]string, 0, 64)
	for r := range 64 {
		ruleID := fmt.Sprintf("big.g%d.r%d", g, r)
		rules = append(rules, testRootRuleJSON(ruleID, fmt.Sprintf("k%d", g*64+r), "Entity"))
	}
	return strings.Join(rules, ",")
}

// bigGroupsRest builds groups 1-7 of the 512-rule boundary fixture.
func bigGroupsRest() string {
	fragments := make([]string, 0, 7)
	for g := range 7 {
		group := g + 1
		fragments = append(
			fragments,
			testCandidateGroupJSON(fmt.Sprintf("big.g%d", group), "switch", "", bigGroupRestRules(group)),
		)
	}
	return strings.Join(fragments, ",")
}

// This test protects per-document structural limits and fails if an override
// with 65 patches compiles. The schema also bounds patches for file inputs,
// so direct compilation proves the compiler repeats the aggregate check with
// the aggregate code.
func TestCompileProfileCatalogPatchCountLimit(t *testing.T) {
	t.Parallel()
	group := profileCandidateGroup{
		ID:   "relay.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("relay.power", profileStrategyBinaryPower, "power",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	}
	planner := testPlannerDocument(20, group)
	patches := make([]profileRulePatch, 0, 65)
	for range 65 {
		patches = append(patches, profileRulePatch{Rule: "relay.power"})
	}
	override := profileOverrideDocument{
		Version:  1,
		Kind:     profileDocumentOverride,
		ID:       "acme-a1",
		Selector: profileDeviceSelector{Vendor: "Acme", Model: "A1"},
		Patches:  patches,
	}
	decoded := []decodedProfileDocument{
		{path: "profiles/relay.profile.json", planner: &planner},
		{path: "profiles/overrides/acme-a1.override.json", override: &override},
	}
	catalog, err := compileDecodedProfileCatalog(decoded, testProfileStrategyRegistry())
	requireProfileCatalogError(t, catalog, err,
		profileCatalogErrorCatalogLimitExceeded, "profiles/overrides/acme-a1.override.json", "acme-a1", "", "/patches")
}

// This test protects unique document identifiers and fails if two files share
// one ID or the error names the first document instead of the lexical second.
func TestCompileProfileCatalogDuplicateDocumentID(t *testing.T) {
	t.Parallel()
	groups := testCandidateGroupJSON("shared.roots", "switch", "", testRootRuleJSON("shared.power", "power", "Power"))
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/a-first.profile.json":  testPlannerDocJSON("shared", 10, groups),
		"profiles/b-second.profile.json": testPlannerDocJSON("shared", 20, groups),
	})
	requireProfileCatalogError(t, catalog, err,
		profileCatalogErrorDuplicateDocumentID, "profiles/b-second.profile.json", "shared", "", "/id")
}

// This test protects globally unique group and rule identifiers and fails if a
// repeated ID compiles or the evidence points away from the lexical second use.
func TestCompileProfileCatalogDuplicateIDs(t *testing.T) {
	t.Parallel()
	powerRule := func(ruleID string) string { return testRootRuleJSON(ruleID, "power", "Power") }
	cases := []struct {
		name      string
		docs      map[string]string
		code      string
		document  string
		profileID string
		ruleID    string
		pointer   string
	}{
		{
			// Plausible defect: group uniqueness checked per profile only,
			// letting two profiles claim one group ID.
			name: "duplicate group across profiles",
			docs: map[string]string{
				"profiles/a-first.profile.json": testPlannerDocJSON(
					"a-first",
					10,
					testCandidateGroupJSON("shared.roots", "switch", "", powerRule("a-first.power")),
				),
				"profiles/b-second.profile.json": testPlannerDocJSON(
					"b-second",
					20,
					testCandidateGroupJSON("shared.roots", "switch", "", powerRule("b-second.power")),
				),
			},
			code: profileCatalogErrorDuplicateGroupID, document: "profiles/b-second.profile.json",
			profileID: "b-second", pointer: "/candidate_groups/0/id",
		},
		{
			// Plausible defect: a copy-paste group duplicated inside one profile.
			name: "duplicate group in one profile",
			docs: map[string]string{
				"profiles/relay.profile.json": testPlannerDocJSON("relay", 20,
					testCandidateGroupJSON("relay.roots", "switch", "", powerRule("relay.power"))+","+
						testCandidateGroupJSON("relay.roots", "light", "", powerRule("relay.brightness"))),
			},
			code: profileCatalogErrorDuplicateGroupID, document: "profiles/relay.profile.json",
			profileID: "relay", pointer: "/candidate_groups/1/id",
		},
		{
			// Plausible defect: a rule ID reused across profiles breaks the
			// global override target contract.
			name: "duplicate rule across profiles",
			docs: map[string]string{
				"profiles/a-first.profile.json": testPlannerDocJSON(
					"a-first",
					10,
					testCandidateGroupJSON("a-first.roots", "switch", "", powerRule("shared.power")),
				),
				"profiles/b-second.profile.json": testPlannerDocJSON(
					"b-second",
					20,
					testCandidateGroupJSON("b-second.roots", "switch", "", powerRule("shared.power")),
				),
			},
			code: profileCatalogErrorDuplicateRuleID, document: "profiles/b-second.profile.json",
			profileID: "b-second", ruleID: "shared.power", pointer: "/candidate_groups/0/entities/0/id",
		},
		{
			// Plausible defect: a candidate rule and a device rule share one ID.
			name: "duplicate rule between candidate and device entities",
			docs: map[string]string{
				"profiles/relay.profile.json": testPlannerDocWithDevicesJSON(
					"relay",
					20,
					testCandidateGroupJSON("relay.roots", "switch", "", powerRule("relay.power")),
					testDeviceRuleJSON(
						"relay.power",
						"power_on_behavior",
						"relay.roots",
						"poweronbehavior",
						"Power-On Behavior",
					),
				),
			},
			// The gate-less group reference below would also fail; the duplicate
			// rule fires first in traversal order, which this expectation pins.
			code: profileCatalogErrorDuplicateRuleID, document: "profiles/relay.profile.json",
			profileID: "relay", ruleID: "relay.power", pointer: "/device_entities/0/id",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := compileTestProfileFiles(t, tc.docs)
			requireProfileCatalogError(t, catalog, err, tc.code, tc.document, tc.profileID, tc.ruleID, tc.pointer)
		})
	}
}

// This test protects per-profile base entity key uniqueness and fails if two
// rules in one profile claim one key while the same key across profiles (the
// merge contract) is wrongly rejected.
func TestCompileProfileCatalogDuplicateEntityKey(t *testing.T) {
	t.Parallel()
	t.Run("duplicate key in one profile fails", func(t *testing.T) {
		t.Parallel()
		groups := testCandidateGroupJSON("relay.roots", "switch", "relay.power",
			testFeatureRuleJSON("relay.power", "binary", "state", "binary-power", `{}`, "power", "Power"))
		devices := testDeviceRuleJSON(
			"relay.power-on-behavior",
			"power_on_behavior",
			"relay.roots",
			"power",
			"Power-On Behavior",
		)
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/relay.profile.json": testPlannerDocWithDevicesJSON("relay", 20, groups, devices),
		})
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorDuplicateEntityKey,
			"profiles/relay.profile.json",
			"relay",
			"relay.power-on-behavior",
			"/device_entities/0/identity/key",
		)
	})
	t.Run("same key across profiles compiles", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: key uniqueness enforced catalog-wide, rejecting the
		// legitimate cross-profile merge contract.
		groups := func(ruleID string) string {
			return testCandidateGroupJSON(ruleID+".roots", "switch", "", testRootRuleJSON(ruleID, "power", "Power"))
		}
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/a-first.profile.json":  testPlannerDocJSON("a-first", 10, groups("a-first")),
			"profiles/b-second.profile.json": testPlannerDocJSON("b-second", 20, groups("b-second")),
		})
		if err != nil {
			t.Fatalf("expected shared keys across profiles to compile, got %v", err)
		}
		if catalog == nil {
			t.Fatal("expected non-nil catalog")
		}
	})
}

// This test protects gate and sibling dependency references and fails if an
// unknown, misplaced, or forward dependency compiles.
func TestCompileProfileCatalogGateAndDependencyReferences(t *testing.T) {
	t.Parallel()
	gated := func(gate string, entities string) string {
		return testPlannerDocJSON("relay", 20, testCandidateGroupJSON("relay.roots", "switch", gate, entities))
	}
	power := testFeatureRuleJSON("relay.power", "binary", "state", "binary-power", `{}`, "power", "Power")
	brightness := testFeatureRuleJSON(
		"relay.brightness",
		"numeric",
		"brightness",
		"binary-power",
		`{}`,
		"brightness",
		"Brightness",
	)
	withDep := func(rule, deps string) string {
		return `{"id":"` + rule + `","source":{"kind":"feature","type":"numeric","name":"brightness"},"requires_any":[` + deps + `]` +
			`,"identity":{"key":"brightness","name":"Brightness"},"strategy":{"name":"binary-power","parameters":{}}}`
	}
	cases := []struct {
		name    string
		doc     string
		code    string
		ruleID  string
		pointer string
	}{
		// Plausible defect: a gate names a rule that does not exist.
		{
			name:    "unknown gate rule",
			doc:     gated("relay.missing", power),
			code:    profileCatalogErrorUnknownReference,
			ruleID:  "relay.missing",
			pointer: "/candidate_groups/0/gate_rule",
		},
		// Plausible defect: the gate is not the first entity rule, so sibling
		// gating would evaluate in the wrong order.
		{
			name:    "gate not first",
			doc:     gated("relay.brightness", power+","+brightness),
			code:    profileCatalogErrorInvalidDependencyOrder,
			ruleID:  "relay.brightness",
			pointer: "/candidate_groups/0/gate_rule",
		},
		// Plausible defect: requires_any names a rule outside the group.
		{
			name:    "unknown dependency",
			doc:     gated("", power+","+withDep("relay.brightness", `"relay.missing"`)),
			code:    profileCatalogErrorUnknownReference,
			ruleID:  "relay.brightness",
			pointer: "/candidate_groups/0/entities/1/requires_any/0",
		},
		// Plausible defect: requires_any points forward, so the dependency has
		// no plan yet when the rule evaluates.
		{
			name:    "forward dependency",
			doc:     gated("", withDep("relay.power", `"relay.brightness"`)+","+brightness),
			code:    profileCatalogErrorInvalidDependencyOrder,
			ruleID:  "relay.power",
			pointer: "/candidate_groups/0/entities/0/requires_any/0",
		},
		// Plausible defect: a rule depends on itself.
		{
			name:    "self dependency",
			doc:     gated("", power+","+withDep("relay.brightness", `"relay.brightness"`)),
			code:    profileCatalogErrorInvalidDependencyOrder,
			ruleID:  "relay.brightness",
			pointer: "/candidate_groups/0/entities/1/requires_any/0",
		},
		// The valid control: a gate plus a dependency on the earlier gate.
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := compileTestProfileFiles(t, map[string]string{
				"profiles/relay.profile.json": tc.doc,
			})
			requireProfileCatalogError(
				t,
				catalog,
				err,
				tc.code,
				"profiles/relay.profile.json",
				"relay",
				tc.ruleID,
				tc.pointer,
			)
		})
	}
	t.Run("gate with earlier dependency compiles", func(t *testing.T) {
		t.Parallel()
		doc := gated("relay.power", power+","+withDep("relay.brightness", `"relay.power"`))
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/relay.profile.json": doc,
		})
		if err != nil {
			t.Fatalf("expected valid gate and dependency to compile, got %v", err)
		}
		if catalog == nil {
			t.Fatal("expected non-nil catalog")
		}
	})
}

// This test protects device-entity group-survivor references and fails if a
// device rule attaches to a missing group, a foreign-profile group, or an
// ungated group that can never establish survival.
func TestCompileProfileCatalogGroupSurvivorReferences(t *testing.T) {
	t.Parallel()
	plannerWith := func(groups, devices string) string {
		return testPlannerDocWithDevicesJSON("relay", 20, groups, devices)
	}
	gated := testCandidateGroupJSON("relay.roots", "switch", "relay.power",
		testFeatureRuleJSON("relay.power", "binary", "state", "binary-power", `{}`, "power", "Power"))
	ungated := testCandidateGroupJSON("relay.ungated", "switch", "",
		testRootRuleJSON("relay.extra", "extra", "Extra"))
	cases := []struct {
		name    string
		doc     string
		code    string
		ruleID  string
		pointer string
	}{
		// Plausible defect: the survivor group was renamed but the device rule
		// still names the old group.
		{
			name: "unknown survivor group",
			doc: plannerWith(
				gated,
				testDeviceRuleJSON(
					"relay.power-on-behavior",
					"power_on_behavior",
					"relay.missing",
					"poweronbehavior",
					"Power-On Behavior",
				),
			),
			code:    profileCatalogErrorInvalidGroupReference,
			ruleID:  "relay.power-on-behavior",
			pointer: "/device_entities/0/requires_group_survivor",
		},
		// Plausible defect: the survivor group lost its gate, so survival has
		// no gate-planning meaning.
		{
			name: "survivor without gate",
			doc: plannerWith(
				gated+","+ungated,
				testDeviceRuleJSON(
					"relay.power-on-behavior",
					"power_on_behavior",
					"relay.ungated",
					"poweronbehavior",
					"Power-On Behavior",
				),
			),
			code:    profileCatalogErrorInvalidGroupReference,
			ruleID:  "relay.power-on-behavior",
			pointer: "/device_entities/0/requires_group_survivor",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			catalog, err := compileTestProfileFiles(t, map[string]string{
				"profiles/relay.profile.json": tc.doc,
			})
			requireProfileCatalogError(
				t,
				catalog,
				err,
				tc.code,
				"profiles/relay.profile.json",
				"relay",
				tc.ruleID,
				tc.pointer,
			)
		})
	}
	t.Run("survivor in another profile fails", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: survivor lookup spans profiles instead of staying
		// inside the owning profile.
		other := testPlannerDocJSON("other", 10,
			testCandidateGroupJSON("relay.roots", "switch", "relay.power",
				testFeatureRuleJSON("relay.power", "binary", "state", "binary-power", `{}`, "power", "Power")))
		mine := testPlannerDocWithDevicesJSON("mine", 20,
			testCandidateGroupJSON("mine.roots", "switch", "",
				testRootRuleJSON("mine.extra", "extra", "Extra")),
			testDeviceRuleJSON("mine.setting", "mode", "relay.roots", "setting", "Setting"))
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/a-other.profile.json": other,
			"profiles/b-mine.profile.json":  mine,
		})
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorInvalidGroupReference,
			"profiles/b-mine.profile.json",
			"mine",
			"mine.setting",
			"/device_entities/0/requires_group_survivor",
		)
	})
}

// This test protects strategy and source compatibility and fails if a
// candidate or device rule uses a source kind its strategy does not accept.
func TestCompileProfileCatalogStrategySourceMismatch(t *testing.T) {
	t.Parallel()
	t.Run("candidate feature rejected by root-only strategy", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: the compiler skips the AllowedSourceKinds check, so a
		// root-only strategy silently accepts a feature source. The brightness test
		// strategy allows only root sources.
		doc := testPlannerDocJSON("relay", 20, testCandidateGroupJSON(
			"relay.roots",
			"switch",
			"",
			testFeatureRuleJSON(
				"relay.brightness",
				"numeric",
				"brightness",
				"brightness",
				`{}`,
				"brightness",
				"Brightness",
			),
		))
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/relay.profile.json": doc,
		})
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategySourceMismatch,
			"profiles/relay.profile.json",
			"relay",
			"relay.brightness",
			"/candidate_groups/0/entities/0/source",
		)
	})
	t.Run("device expose rejected by feature-only strategy", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: device rules bypass source compatibility even though
		// UniqueRoot always supplies a root expose to their strategies.
		group := profileCandidateGroup{
			ID:       "relay.roots",
			Root:     profileCandidateRootSelector{Type: "switch"},
			GateRule: "relay.power",
			Entities: []profileCandidateEntity{
				testCandidateRule("relay.power", profileStrategyBinaryPower, "power",
					profileEntitySource{Kind: profileEntitySourceRoot}),
			},
		}
		doc := testPlannerDocument(20, group)
		doc.DeviceEntities = []profileDeviceEntity{{
			ID:                    "relay.sensor",
			Expose:                profileExposeSelector{Type: "numeric", Name: "sensor"},
			RequiresGroupSurvivor: "relay.roots",
			Identity:              profileEntityIdentity{Key: "sensor", Name: "Sensor"},
			Strategy: profileStrategyRef{
				Name:       profileStrategyNumericSensor,
				Parameters: json.RawMessage(`{}`),
			},
		}}
		catalog, err := compileTestDecodedPlanner(t, doc)
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategySourceMismatch,
			"profiles/relay.profile.json",
			"relay",
			"relay.sensor",
			"/device_entities/0/expose",
		)
	})
}

// This test protects one-time strategy parameter compilation and fails if an
// invalid parameter object compiles or a valid one is rejected.
func TestCompileProfileCatalogStrategyParameters(t *testing.T) {
	t.Parallel()
	sensorParams := func(minimum, maximum string) string {
		return `{"accepted_units":[""],"unit":"celsius","number_format":"float","bounds":{"mode":"fixed","minimum":` + minimum + `,"maximum":` + maximum + `}}`
	}
	sensorRule := func(ruleID, params string) string {
		return testFeatureRuleJSON(
			ruleID,
			"numeric",
			"temperature",
			"numeric-sensor",
			params,
			"temperature",
			"Temperature",
		)
	}
	t.Run("inverted bounds fail", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: the compiler never calls CompileParameters, so a
		// minimum above maximum reaches planning.
		doc := testPlannerDocJSON("sensors", 30, testCandidateGroupJSON("sensors.roots", "temperature", "",
			sensorRule("sensor.temperature", sensorParams("100", "-50"))))
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/sensors.profile.json": doc,
		})
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategyParamsInvalid,
			"profiles/sensors.profile.json",
			"sensors",
			"sensor.temperature",
			"/candidate_groups/0/entities/0/strategy/parameters",
		)
	})
	t.Run("valid parameters compile once", func(t *testing.T) {
		t.Parallel()
		doc := testPlannerDocJSON("sensors", 30, testCandidateGroupJSON("sensors.roots", "temperature", "",
			sensorRule("sensor.temperature", sensorParams("-50", "100"))))
		catalog, err := compileTestProfileFiles(t, map[string]string{
			"profiles/sensors.profile.json": doc,
		})
		if err != nil {
			t.Fatalf("expected valid parameters to compile, got %v", err)
		}
		params, ok := catalog.profiles[0].ruleParameters["sensor.temperature"]
		if !ok {
			t.Fatal("expected compiled parameters for sensor.temperature")
		}
		if params.Strategy != profileStrategyNumericSensor {
			t.Fatalf("expected strategy numeric-sensor, got %q", params.Strategy)
		}
	})
}

// This test protects override target and kind matching plus layered selector
// overlap and fails if a patch retargets an unknown rule, swaps the rule
// kind, replaces parameters the target strategy rejects, or overlaps another
// patch for the same target.
func TestCompileProfileCatalogOverrides(t *testing.T) {
	t.Parallel()
	relay := testRelayPlannerDoc()
	powerPatch := func(fields string) string {
		if fields == "" {
			return `{"rule":"relay.power"}`
		}
		return `{"rule":"relay.power",` + fields + `}`
	}
	numericSensorParams := `{"accepted_units":[""],"unit":"celsius","number_format":"float","bounds":{"mode":"fixed","minimum":-50,"maximum":100}}`
	cases := []struct {
		name      string
		overrides map[string]string
		code      string
		document  string
		profileID string
		ruleID    string
		pointer   string
	}{
		{
			// Plausible defect: a patch targets a rule renamed in its profile.
			name: "unknown target rule",
			overrides: map[string]string{
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON(
					"acme-a1",
					"Acme",
					"A1",
					nil,
					`{"rule":"relay.missing"}`,
				),
			},
			code: profileCatalogErrorOverrideTargetUnknown, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "relay.missing", pointer: "/patches/0/rule",
		},
		{
			// Plausible defect: a candidate source patch lands on a device rule.
			name: "source patch on device rule",
			overrides: map[string]string{
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					`{"rule":"relay.power-on-behavior","source":{"kind":"root"}}`),
			},
			code: profileCatalogErrorOverrideTargetMismatch, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "relay.power-on-behavior", pointer: "/patches/0/source",
		},
		{
			// Plausible defect: a device expose patch lands on a candidate rule.
			name: "expose patch on candidate rule",
			overrides: map[string]string{
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					`{"rule":"relay.power","expose":{"type":"switch"}}`),
			},
			code: profileCatalogErrorOverrideTargetMismatch, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "relay.power", pointer: "/patches/0/expose",
		},
		{
			// Plausible defect: two general patches claim one target, so the
			// base-to-general layer is ambiguous.
			name: "duplicate general patches",
			overrides: map[string]string{
				"profiles/overrides/acme-a-first.override.json": testOverrideDocJSON(
					"acme-a-first",
					"Acme",
					"A1",
					nil,
					powerPatch(`"enabled":false`),
				),
				"profiles/overrides/acme-b-second.override.json": testOverrideDocJSON(
					"acme-b-second",
					"Acme",
					"A1",
					nil,
					powerPatch(`"enabled":true`),
				),
			},
			code:      profileCatalogErrorOverrideSelectorConflict,
			document:  "profiles/overrides/acme-b-second.override.json",
			profileID: "acme-b-second",
			ruleID:    "relay.power",
			pointer:   "/patches/0",
		},
		{
			// Plausible defect: one document patches one target twice, hiding
			// which patch wins.
			name: "duplicate target in one document",
			overrides: map[string]string{
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					powerPatch(`"enabled":false`)+","+powerPatch(`"enabled":true`)),
			},
			code: profileCatalogErrorOverrideSelectorConflict, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "relay.power", pointer: "/patches/1",
		},
		{
			// Plausible defect: two exact-build patches share a build ID, so
			// one firmware build matches two layers.
			name: "overlapping build IDs",
			overrides: map[string]string{
				"profiles/overrides/acme-a-first.override.json": testOverrideDocJSON(
					"acme-a-first",
					"Acme",
					"A1",
					[]string{"b1", "b2"},
					powerPatch(`"enabled":false`),
				),
				"profiles/overrides/acme-b-second.override.json": testOverrideDocJSON(
					"acme-b-second",
					"Acme",
					"A1",
					[]string{"b2", "b3"},
					powerPatch(`"enabled":true`),
				),
			},
			code:      profileCatalogErrorOverrideSelectorConflict,
			document:  "profiles/overrides/acme-b-second.override.json",
			profileID: "acme-b-second",
			ruleID:    "relay.power",
			pointer:   "/patches/0",
		},
		{
			// Plausible defect: replacement parameters bypass target strategy
			// validation. The enum-action shape passes the schema union but the
			// numeric-sensor strategy must reject it.
			name: "replacement parameters rejected by target strategy",
			overrides: map[string]string{
				"profiles/sensors.profile.json": testPlannerDocJSON(
					"sensors",
					30,
					testCandidateGroupJSON(
						"sensors.roots",
						"temperature",
						"",
						testFeatureRuleJSON(
							"sensor.temperature",
							"numeric",
							"temperature",
							"numeric-sensor",
							numericSensorParams,
							"temperature",
							"Temperature",
						),
					),
				),
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					`{"rule":"sensor.temperature","strategy_parameters":{"access":"set-only"}}`),
			},
			code: profileCatalogErrorStrategyParamsInvalid, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "sensor.temperature", pointer: "/patches/0/strategy_parameters",
		},
		{
			// Plausible defect: a replacement source bypasses the target
			// strategy source compatibility check.
			name: "replacement source rejected by target strategy",
			overrides: map[string]string{
				"profiles/bright.profile.json": testPlannerDocJSON(
					"bright",
					10,
					testCandidateGroupJSON(
						"bright.roots",
						"light",
						"",
						`{"id":"bright.brightness","source":{"kind":"root"},"identity":{"key":"brightness","name":"Brightness"},"strategy":{"name":"brightness","parameters":{}}}`,
					),
				),
				"profiles/overrides/acme-a1.override.json": testOverrideDocJSON("acme-a1", "Acme", "A1", nil,
					`{"rule":"bright.brightness","source":{"kind":"feature","type":"numeric","name":"brightness"}}`),
			},
			code: profileCatalogErrorStrategySourceMismatch, document: "profiles/overrides/acme-a1.override.json",
			profileID: "acme-a1", ruleID: "bright.brightness", pointer: "/patches/0/source",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			docs := map[string]string{"profiles/relay.profile.json": relay}
			maps.Copy(docs, tc.overrides)
			catalog, err := compileTestProfileFiles(t, docs)
			requireProfileCatalogError(t, catalog, err, tc.code, tc.document, tc.profileID, tc.ruleID, tc.pointer)
		})
	}
}

// This test protects override layering storage and fails if disjoint
// exact-build patches, the general plus exact-build layering, or distinct
// vendor selectors are rejected, or if compiled layers drop their overlays.
func TestCompileProfileCatalogValidOverrideLayers(t *testing.T) {
	t.Parallel()
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/relay.profile.json": testRelayPlannerDoc(),
		"profiles/overrides/acme-general.override.json": testOverrideDocJSON("acme-general", "Acme", "A1", nil,
			`{"rule":"relay.power","enabled":false}`),
		"profiles/overrides/acme-build.override.json": testOverrideDocJSON("acme-build", "Acme", "A1", []string{"b1"},
			`{"rule":"relay.power","enabled":true}`),
		"profiles/overrides/acme-other-build.override.json": testOverrideDocJSON(
			"acme-other-build",
			"Acme",
			"A1",
			[]string{"b2"},
			`{"rule":"relay.power-on-behavior","expose":{"type":"enum","name":"mode"}}`,
		),
		"profiles/overrides/other-vendor.override.json": testOverrideDocJSON("other-vendor", "Other", "B2", nil,
			`{"rule":"relay.power","enabled":true}`),
	})
	if err != nil {
		t.Fatalf("expected layered overrides to compile, got %v", err)
	}
	if len(catalog.overrides) != 4 {
		t.Fatalf("expected 4 compiled overrides, got %d", len(catalog.overrides))
	}
	general := findCompiledOverride(t, catalog, "acme-general")
	if len(general.patches) != 1 || general.patches[0].targetRuleID != "relay.power" {
		t.Fatalf("expected one general patch for relay.power, got %+v", general.patches)
	}
	if general.patches[0].targetDeviceRule {
		t.Fatal("expected relay.power patch to target a candidate rule")
	}
	if general.patches[0].enabled == nil || *general.patches[0].enabled {
		t.Fatalf("expected general patch to disable relay.power, got %+v", general.patches[0].enabled)
	}
	if general.patches[0].parameters != nil {
		t.Fatal("expected enabled-only patch to keep base parameters")
	}
	build := findCompiledOverride(t, catalog, "acme-build")
	if len(build.patches) != 1 || build.patches[0].enabled == nil || !*build.patches[0].enabled {
		t.Fatalf("expected exact-build patch to re-enable relay.power, got %+v", build.patches)
	}
	device := findCompiledOverride(t, catalog, "acme-other-build")
	if len(device.patches) != 1 || !device.patches[0].targetDeviceRule {
		t.Fatalf("expected device-rule patch target flag, got %+v", device.patches)
	}
	if device.patches[0].expose == nil || device.patches[0].expose.Name != "mode" {
		t.Fatalf("expected replacement expose name mode, got %+v", device.patches[0].expose)
	}
}

// This test protects selector identity and fails if delimiter-bearing vendor
// or model strings make two distinct selectors collide.
func TestCompileProfileCatalogOverrideSelectorKeyIsUnambiguous(t *testing.T) {
	t.Parallel()
	group := profileCandidateGroup{
		ID:   "relay.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("relay.power", profileStrategyBinaryPower, "power",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	}
	planner := testPlannerDocument(20, group)
	overrides := []profileOverrideDocument{
		{
			Version: 1, Kind: profileDocumentOverride, ID: "first",
			Selector: profileDeviceSelector{Vendor: "A", Model: "B\x00C"},
			Patches:  []profileRulePatch{{Rule: "relay.power"}},
		},
		{
			Version: 1, Kind: profileDocumentOverride, ID: "second",
			Selector: profileDeviceSelector{Vendor: "A\x00B", Model: "C"},
			Patches:  []profileRulePatch{{Rule: "relay.power"}},
		},
	}
	decoded := []decodedProfileDocument{{path: "profiles/relay.profile.json", planner: &planner}}
	for index := range overrides {
		decoded = append(decoded, decodedProfileDocument{
			path:     fmt.Sprintf("profiles/overrides/%d.override.json", index),
			override: &overrides[index],
		})
	}
	catalog, err := compileDecodedProfileCatalog(decoded, testProfileStrategyRegistry())
	if err != nil {
		t.Fatalf("distinct delimiter-bearing selectors should compile: %v", err)
	}
	if len(catalog.overrides) != 2 {
		t.Fatalf("expected two distinct overrides, got %d", len(catalog.overrides))
	}
}

// This test protects lexical-path diagnostic order and fails if documents are
// validated in caller order instead of lexical path order.
func TestCompileProfileCatalogLexicalDiagnosticOrder(t *testing.T) {
	t.Parallel()
	groups := func(groupID, ruleID string) string {
		return testCandidateGroupJSON(groupID, "switch", "", testRootRuleJSON(ruleID, "power", "Power"))
	}
	// Plausible defect: compilation follows input slice order, so the reported
	// document depends on filesystem or map iteration order.
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/z-last.profile.json":  testPlannerDocJSON("z-last", 20, groups("z-last.roots", "shared.power")),
		"profiles/a-first.profile.json": testPlannerDocJSON("a-first", 10, groups("a-first.roots", "shared.power")),
	})
	requireProfileCatalogError(
		t,
		catalog,
		err,
		profileCatalogErrorDuplicateRuleID,
		"profiles/z-last.profile.json",
		"z-last",
		"shared.power",
		"/candidate_groups/0/entities/0/id",
	)
}

// This test protects compiler phase ordering and fails if semantic validation
// of an early profile masks a catalog-wide structural conflict in a later one.
func TestCompileProfileCatalogChecksAllIDsBeforeSemantics(t *testing.T) {
	t.Parallel()
	first := testPlannerDocument(10, profileCandidateGroup{
		ID:   "shared.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("first.unknown", "frobnicator", "first",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	})
	first.ID = "first"
	second := testPlannerDocument(20, profileCandidateGroup{
		ID:   "shared.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("second.power", profileStrategyBinaryPower, "second",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	})
	second.ID = "second"
	catalog, err := compileTestDecodedPlanner(t, first, second)
	requireProfileCatalogError(
		t,
		catalog,
		err,
		profileCatalogErrorDuplicateGroupID,
		"profiles/second.profile.json",
		"second",
		"",
		"/candidate_groups/0/id",
	)
}

// This test protects the diagnostic message contract and fails if a catalog
// error omits its stable code, location evidence, or leaks profile contents.
func TestProfileCatalogErrorMessageContract(t *testing.T) {
	t.Parallel()
	catalog, err := compileTestProfileFiles(t, map[string]string{
		"profiles/relay.profile.json": testRelayPlannerDoc() + "{}",
	})
	catalogErr := requireProfileCatalogError(t, catalog, err,
		profileCatalogErrorSchemaInvalid, "profiles/relay.profile.json", "", "", "")
	message := catalogErr.Error()
	for _, want := range []string{"schema_invalid", "profiles/relay.profile.json"} {
		if !strings.Contains(message, want) {
			t.Fatalf("expected error message to contain %q, got %q", want, message)
		}
	}
	if unwrapped := errors.Unwrap(catalogErr); unwrapped == nil {
		t.Fatal("expected ProfileCatalogError to unwrap to its cause")
	}
}

// findCompiledOverride locates one compiled override by document ID for layer
// assertions.
func findCompiledOverride(t *testing.T, catalog *ProfileCatalog, id string) compiledProfileOverride {
	t.Helper()
	for _, override := range catalog.overrides {
		if override.document.ID == id {
			return override
		}
	}
	t.Fatalf("expected compiled override %q", id)
	return compiledProfileOverride{}
}

// testCandidateRule builds one candidate rule Go value with empty parameters
// for direct document compilation below the schema layer.
func testCandidateRule(id, strategy, key string, source profileEntitySource) profileCandidateEntity {
	return profileCandidateEntity{
		ID:       id,
		Source:   source,
		Identity: profileEntityIdentity{Key: key, Name: "Name " + key},
		Strategy: profileStrategyRef{Name: strategy, Parameters: json.RawMessage(`{}`)},
	}
}

// testPlannerDocument builds one relay planner document Go value with one group.
func testPlannerDocument(order int, group profileCandidateGroup) plannerProfileDocument {
	return plannerProfileDocument{
		Version:         1,
		Kind:            profileDocumentPlanner,
		ID:              "relay",
		Order:           order,
		Contribution:    profileContribution{DeviceKind: "relay", Role: profilePlannerRolePrimary},
		CandidateGroups: []profileCandidateGroup{group},
	}
}

// compileTestDecodedPlanner compiles planner documents directly, bypassing
// schema validation to reach semantic checks the schema would mask.
func compileTestDecodedPlanner(t *testing.T, planners ...plannerProfileDocument) (*ProfileCatalog, error) {
	t.Helper()
	decoded := make([]decodedProfileDocument, 0, len(planners))
	for i := range planners {
		decoded = append(decoded, decodedProfileDocument{
			path:    "profiles/" + planners[i].ID + ".profile.json",
			planner: &planners[i],
		})
	}
	return compileDecodedProfileCatalog(decoded, testProfileStrategyRegistry())
}

// This test protects unknown-strategy rejection and fails if a rule references
// a strategy outside the injected registry. The schema masks this case for
// file inputs by rejecting unknown names first, so direct compilation proves
// the semantic check.
func TestCompileDecodedProfileCatalogUnknownStrategy(t *testing.T) {
	t.Parallel()
	group := profileCandidateGroup{
		ID:   "relay.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("relay.power", "frobnicator", "power",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	}
	catalog, err := compileTestDecodedPlanner(t, testPlannerDocument(20, group))
	requireProfileCatalogError(
		t,
		catalog,
		err,
		profileCatalogErrorUnknownStrategy,
		"profiles/relay.profile.json",
		"relay",
		"relay.power",
		"/candidate_groups/0/entities/0/strategy/name",
	)
}

// This test protects contribution validation and fails if an unknown device
// kind, planner role, or unsupported kind/role combination compiles. The
// schema masks unknown values for file inputs, while kind/role compatibility
// is a semantic rule, so direct compilation proves both checks.
func TestCompileDecodedProfileCatalogInvalidContribution(t *testing.T) {
	t.Parallel()
	group := profileCandidateGroup{
		ID:   "relay.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("relay.power", profileStrategyBinaryPower, "power",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	}
	t.Run("unknown device kind", func(t *testing.T) {
		t.Parallel()
		doc := testPlannerDocument(20, group)
		doc.Contribution.DeviceKind = "actuator"
		catalog, err := compileTestDecodedPlanner(t, doc)
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorInvalidContribution,
			"profiles/relay.profile.json",
			"relay",
			"",
			"/contribution/device_kind",
		)
	})
	t.Run("unknown planner role", func(t *testing.T) {
		t.Parallel()
		doc := testPlannerDocument(20, group)
		doc.Contribution.Role = "boss"
		catalog, err := compileTestDecodedPlanner(t, doc)
		requireProfileCatalogError(t, catalog, err,
			profileCatalogErrorInvalidContribution, "profiles/relay.profile.json", "relay", "", "/contribution/role")
	})
	for _, test := range []struct {
		name string
		kind string
		role profilePlannerRole
	}{
		{name: "light supplemental", kind: "light", role: profilePlannerRoleSupplemental},
		{name: "relay supplemental", kind: "relay", role: profilePlannerRoleSupplemental},
		{name: "sensor primary", kind: "sensor", role: profilePlannerRolePrimary},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			doc := testPlannerDocument(20, group)
			doc.Contribution = profileContribution{DeviceKind: test.kind, Role: test.role}
			catalog, err := compileTestDecodedPlanner(t, doc)
			requireProfileCatalogError(t, catalog, err,
				profileCatalogErrorInvalidContribution, "profiles/relay.profile.json", "relay", "", "/contribution")
		})
	}
	t.Run("order outside schema bounds", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: range validation lives only in the schema, so a
		// direct document with order zero compiles.
		doc := testPlannerDocument(0, group)
		catalog, err := compileTestDecodedPlanner(t, doc)
		requireProfileCatalogError(t, catalog, err,
			profileCatalogErrorSchemaInvalid, "profiles/relay.profile.json", "relay", "", "/order")
	})
	t.Run("unknown candidate root cardinality", func(t *testing.T) {
		t.Parallel()
		doc := testPlannerDocument(20, group)
		doc.CandidateGroups[0].Root.Cardinality = "first"
		catalog, err := compileTestDecodedPlanner(t, doc)
		requireProfileCatalogError(t, catalog, err,
			profileCatalogErrorSchemaInvalid, "profiles/relay.profile.json", "relay", "",
			"/candidate_groups/0/root/cardinality")
	})
}

// This test protects the closed source forms and fails if a source outside
// the root, feature, and derived shapes compiles. The schema masks these
// shapes for file inputs, so direct compilation proves the semantic check.
func TestCompileDecodedProfileCatalogClosedSourceForms(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		source profileEntitySource
	}{
		// Plausible defect: a root source smuggles a nested expose name that
		// changes strategy input selection.
		{name: "root source with type", source: profileEntitySource{Kind: profileEntitySourceRoot, Type: "binary"}},
		{name: "root source with name", source: profileEntitySource{Kind: profileEntitySourceRoot, Name: "state"}},
		// Plausible defect: a feature source omits one half of the exact
		// UniqueFeature lookup.
		{
			name:   "feature source without type",
			source: profileEntitySource{Kind: profileEntitySourceFeature, Name: "state"},
		},
		{
			name:   "feature source without name",
			source: profileEntitySource{Kind: profileEntitySourceFeature, Type: "binary"},
		},
		// Plausible defect: a derived source bypasses the version 1
		// color-mode restriction or carries an expose type.
		{
			name:   "derived source with wrong name",
			source: profileEntitySource{Kind: profileEntitySourceDerived, Name: "hue"},
		},
		{
			name:   "derived source with type",
			source: profileEntitySource{Kind: profileEntitySourceDerived, Name: "color-mode", Type: "numeric"},
		},
		{name: "unknown source kind", source: profileEntitySource{Kind: "quantum", Name: "state"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			group := profileCandidateGroup{
				ID:   "relay.roots",
				Root: profileCandidateRootSelector{Type: "switch"},
				Entities: []profileCandidateEntity{
					testCandidateRule("relay.power", profileStrategyBinaryPower, "power", tc.source),
				},
			}
			catalog, err := compileTestDecodedPlanner(t, testPlannerDocument(20, group))
			requireProfileCatalogError(
				t,
				catalog,
				err,
				profileCatalogErrorStrategySourceMismatch,
				"profiles/relay.profile.json",
				"relay",
				"relay.power",
				"/candidate_groups/0/entities/0/source",
			)
		})
	}
	t.Run("derived color-mode rejected by incompatible strategy", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: only the closed form is checked while the strategy
		// source allowlist is ignored.
		group := profileCandidateGroup{
			ID:   "relay.roots",
			Root: profileCandidateRootSelector{Type: "light"},
			Entities: []profileCandidateEntity{
				testCandidateRule("relay.mode", profileStrategyBinaryPower, "mode",
					profileEntitySource{Kind: profileEntitySourceDerived, Name: "color-mode"}),
			},
		}
		catalog, err := compileTestDecodedPlanner(t, testPlannerDocument(20, group))
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategySourceMismatch,
			"profiles/relay.profile.json",
			"relay",
			"relay.mode",
			"/candidate_groups/0/entities/0/source",
		)
	})
}

// This test protects required strategy parameters and fails if a missing or
// explicit-null parameter object compiles. The schema masks both cases for
// file inputs, so direct compilation proves the semantic check.
func TestCompileDecodedProfileCatalogRequiredParameters(t *testing.T) {
	t.Parallel()
	ruleWith := func(params json.RawMessage) profileCandidateGroup {
		return profileCandidateGroup{
			ID:   "relay.roots",
			Root: profileCandidateRootSelector{Type: "switch"},
			Entities: []profileCandidateEntity{{
				ID:       "relay.power",
				Source:   profileEntitySource{Kind: profileEntitySourceRoot},
				Identity: profileEntityIdentity{Key: "power", Name: "Power"},
				Strategy: profileStrategyRef{Name: profileStrategyBinaryPower, Parameters: params},
			}},
		}
	}
	t.Run("missing parameters", func(t *testing.T) {
		t.Parallel()
		catalog, err := compileTestDecodedPlanner(t, testPlannerDocument(20, ruleWith(nil)))
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategyParamsInvalid,
			"profiles/relay.profile.json",
			"relay",
			"relay.power",
			"/candidate_groups/0/entities/0/strategy/parameters",
		)
	})
	t.Run("explicit null parameters", func(t *testing.T) {
		t.Parallel()
		// Plausible defect: explicit null is treated as an omitted optional
		// field instead of a contract violation.
		catalog, err := compileTestDecodedPlanner(t, testPlannerDocument(20, ruleWith(json.RawMessage("null"))))
		requireProfileCatalogError(
			t,
			catalog,
			err,
			profileCatalogErrorStrategyParamsInvalid,
			"profiles/relay.profile.json",
			"relay",
			"relay.power",
			"/candidate_groups/0/entities/0/strategy/parameters",
		)
	})
}

// This test protects override patch field exclusivity below the schema layer
// and fails if one patch replaces both source and expose.
func TestCompileDecodedProfileCatalogOverrideFieldExclusivity(t *testing.T) {
	t.Parallel()
	group := profileCandidateGroup{
		ID:   "relay.roots",
		Root: profileCandidateRootSelector{Type: "switch"},
		Entities: []profileCandidateEntity{
			testCandidateRule("relay.power", profileStrategyBinaryPower, "power",
				profileEntitySource{Kind: profileEntitySourceRoot}),
		},
	}
	decoded := []decodedProfileDocument{
		{path: "profiles/relay.profile.json", planner: func() *plannerProfileDocument {
			doc := testPlannerDocument(20, group)
			return &doc
		}()},
		{path: "profiles/overrides/acme-a1.override.json", override: &profileOverrideDocument{
			Version:  1,
			Kind:     profileDocumentOverride,
			ID:       "acme-a1",
			Selector: profileDeviceSelector{Vendor: "Acme", Model: "A1"},
			Patches: []profileRulePatch{{
				Rule:   "relay.power",
				Source: &profileEntitySource{Kind: profileEntitySourceRoot},
				Expose: &profileExposeSelector{Type: "switch"},
			}},
		}},
	}
	catalog, err := compileDecodedProfileCatalog(decoded, testProfileStrategyRegistry())
	requireProfileCatalogError(
		t,
		catalog,
		err,
		profileCatalogErrorOverrideTargetMismatch,
		"profiles/overrides/acme-a1.override.json",
		"acme-a1",
		"relay.power",
		"/patches/0/source",
	)
}
