package zigbee2mqtt //nolint:testpackage // Parity tests exercise package-private planners, discovery, and wire DTOs.

// This test protects D4 sensor and link-quality differential parity and
// fails if the embedded ambient-sensors or linkquality profiles diverge
// from the handwritten sensorPlanner and linkqualityPlanner over any
// bridge-devices fixture: contribution kind and role, ordered entity keys,
// external IDs, names, types, support documents, state and get properties,
// translator presence, decoded values, device kind, or plan rejection code.
// Production discovery stays on handwritten planners; the profile path here
// is evaluation only, so every comparison also proves no production path
// changed. The final sensitivity case mutates a profile copy to prove the
// harness itself detects drift.

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// profileParityPlanner adapts one compiled planner profile to the
// devicePlanner interface so the profile path flows through the existing
// deterministic planDevice merge and validation.
type profileParityPlanner struct {
	profile    compiledPlannerProfile
	overrides  []compiledProfileOverride
	strategies profileStrategyRegistry
}

// Plan evaluates one profile into one contribution for the shared merge.
func (planner profileParityPlanner) Plan(input devicePlanningInput) plannerContribution {
	return evaluatePlannerProfile(planner.profile, input, planner.overrides, planner.strategies)
}

// parityPlanDifference reports how one profile plan differs from its
// handwritten oracle: descriptor, policy, claimed properties, or command
// translator presence. It returns "" when the plans are identical.
func parityPlanDifference(want, got entityPlan) string {
	if !reflect.DeepEqual(want.Descriptor, got.Descriptor) {
		return "descriptor diverged"
	}
	if want.StatePolicy != got.StatePolicy {
		return "state policy diverged"
	}
	if !reflect.DeepEqual(want.StateProperties, got.StateProperties) {
		return "state properties diverged"
	}
	if !reflect.DeepEqual(want.GetProperties, got.GetProperties) {
		return "get properties diverged"
	}
	if (want.TranslateCommand == nil) != (got.TranslateCommand == nil) {
		return "command translator presence diverged"
	}
	return ""
}

// parityContributionDifference reports how one profile contribution differs
// from its handwritten oracle.
func parityContributionDifference(want, got plannerContribution) string {
	if want.Kind != got.Kind {
		return "device kind diverged"
	}
	if want.Role != got.Role {
		return "planner role diverged"
	}
	if len(want.Entities) != len(got.Entities) {
		return "entity count diverged"
	}
	for index := range want.Entities {
		if difference := parityPlanDifference(want.Entities[index], got.Entities[index]); difference != "" {
			return difference + " at entity position"
		}
	}
	return ""
}

// parityDeviceErrorCode reports the stable plan rejection code for one
// planDevice failure.
func parityDeviceErrorCode(err error) string {
	if err == nil {
		return ""
	}
	if planErr, ok := errors.AsType[*devicePlanError](err); ok {
		return planErr.code
	}
	return "unknown:" + err.Error()
}

// parityBridgeDevicesFixtures lists every bridge-devices fixture in lexical
// order.
func parityBridgeDevicesFixtures(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("testdata")
	if err != nil {
		t.Fatalf("list testdata fixtures: %v", err)
	}
	var fixtures []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasPrefix(entry.Name(), "bridge-devices-") &&
			strings.HasSuffix(entry.Name(), ".json") {
			fixtures = append(fixtures, entry.Name())
		}
	}
	sort.Strings(fixtures)
	if len(fixtures) == 0 {
		t.Fatal("no bridge-devices fixtures found")
	}
	return fixtures
}

// parityFixtureDevices decodes every device in one fixture, mirroring
// discoverInventory isolation for malformed array elements.
func parityFixtureDevices(t *testing.T, fixture string) []upstreamDevice {
	t.Helper()
	items, err := decodeRawArray(readFixture(t, fixture))
	if err != nil {
		t.Fatalf("fixture %s is not a JSON array: %v", fixture, err)
	}
	var devices []upstreamDevice
	for _, item := range items {
		device, deviceErr := decodeUpstreamDevice(item)
		if deviceErr != nil {
			continue
		}
		devices = append(devices, device)
	}
	return devices
}

// parityProfilePlanners returns the hybrid planner assembly used for the
// profile path: handwritten light and relay families with profile-backed
// sensor and link-quality contributions.
func parityProfilePlanners(t *testing.T) []devicePlanner {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	var sensors, linkquality *compiledPlannerProfile
	for index := range catalog.profiles {
		switch catalog.profiles[index].document.ID {
		case "ambient-sensors":
			sensors = &catalog.profiles[index]
		case "linkquality":
			linkquality = &catalog.profiles[index]
		}
	}
	if sensors == nil || linkquality == nil {
		t.Fatal("embedded catalog is missing the ambient-sensors or linkquality profile")
	}
	profilePlanner := func(profile *compiledPlannerProfile) profileParityPlanner {
		return profileParityPlanner{
			profile: *profile, overrides: catalog.overrides, strategies: catalog.strategies,
		}
	}
	return []devicePlanner{
		lightPlanner{},
		relayPlanner{},
		profilePlanner(sensors),
		profilePlanner(linkquality),
	}
}

// This test protects sensor and link-quality contribution parity and fails
// if either embedded profile diverges from its handwritten planner over any
// fixture device: kind, role, ordered entities, or any plan field. The
// defect would be a profile that registers different sensors than production.
func TestSensorLinkqualityProfileContributionsMatchHandwritten(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	sensors := evalTestProfile(t, catalog, "ambient-sensors")
	linkquality := evalTestProfile(t, catalog, "linkquality")
	for _, fixture := range parityBridgeDevicesFixtures(t) {
		for _, device := range parityFixtureDevices(t, fixture) {
			input := profilePlanningInput(device, device.IEEEAddress)
			wantSensor := sensorPlanner{}.Plan(input)
			gotSensor := evaluatePlannerProfile(
				sensors, input, catalog.overrides, catalog.strategies)
			if difference := parityContributionDifference(wantSensor, gotSensor); difference != "" {
				t.Fatalf("%s device %s sensor %s: contribution %+v vs %+v",
					fixture, device.IEEEAddress, difference, wantSensor, gotSensor)
			}
			wantLinkquality := linkqualityPlanner{}.Plan(input)
			gotLinkquality := evaluatePlannerProfile(
				linkquality, input, catalog.overrides, catalog.strategies)
			if difference := parityContributionDifference(wantLinkquality, gotLinkquality); difference != "" {
				t.Fatalf("%s device %s linkquality %s: contribution %+v vs %+v",
					fixture, device.IEEEAddress, difference, wantLinkquality, gotLinkquality)
			}
		}
	}
}

// This test protects merged device parity and fails if profile-backed
// sensor contributions change any merged plan or rejection code: device
// kind, ordered entities, or the stable plan error must match the
// handwritten merge over every fixture device. The defect would be a
// profile whose contribution merges differently than production.
func TestSensorLinkqualityMergedPlansMatchHandwritten(t *testing.T) {
	t.Parallel()
	profilePlanners := parityProfilePlanners(t)
	for _, fixture := range parityBridgeDevicesFixtures(t) {
		for _, device := range parityFixtureDevices(t, fixture) {
			requireMergedParity(t, fixture, device, profilePlanners)
		}
	}
}

// requireMergedParity asserts that profile-backed sensor contributions
// merge exactly like the handwritten merge for one fixture device: device
// kind, ordered entities, or the stable plan rejection code. The defect
// would be a profile whose contribution merges differently than
// production.
func requireMergedParity(
	t *testing.T,
	fixture string,
	device upstreamDevice,
	profilePlanners []devicePlanner,
) {
	t.Helper()
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, wantErr := planDevice(input, defaultDevicePlanners())
	gotPlan, gotErr := planDevice(input, profilePlanners)
	if parityDeviceErrorCode(wantErr) != parityDeviceErrorCode(gotErr) {
		t.Fatalf("%s device %s rejection = %q, want %q",
			fixture, device.IEEEAddress,
			parityDeviceErrorCode(gotErr), parityDeviceErrorCode(wantErr))
	}
	if wantErr != nil {
		return
	}
	if wantPlan.Kind != gotPlan.Kind {
		t.Fatalf("%s device %s kind = %q, want %q",
			fixture, device.IEEEAddress, gotPlan.Kind, wantPlan.Kind)
	}
	if len(wantPlan.Entities) != len(gotPlan.Entities) {
		t.Fatalf("%s device %s entity count = %d, want %d",
			fixture, device.IEEEAddress, len(gotPlan.Entities), len(wantPlan.Entities))
	}
	for index := range wantPlan.Entities {
		difference := parityPlanDifference(wantPlan.Entities[index], gotPlan.Entities[index])
		if difference != "" {
			t.Fatalf("%s device %s entity %d %s: want key %q got key %q",
				fixture, device.IEEEAddress, index, difference,
				wantPlan.Entities[index].Descriptor.Key, gotPlan.Entities[index].Descriptor.Key)
		}
	}
}

// This test protects sensor-only device behavior and fails if the
// temperature fixture device loses its supplemental sensor kind, entity
// order, or get behavior under profile planning: temperature, humidity,
// and battery stay publish-only reads with startup refresh, while
// publish-only linkquality carries no get route.
func TestSensorOnlyDeviceKeepsSupplementalKindUnitsAndGetBehavior(t *testing.T) {
	t.Parallel()
	profilePlanners := parityProfilePlanners(t)
	var temperatureDevice upstreamDevice
	for _, device := range parityFixtureDevices(t, "bridge-devices-temperature.json") {
		temperatureDevice = device
	}
	input := profilePlanningInput(temperatureDevice, temperatureDevice.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the temperature fixture: %v", err)
	}
	gotPlan, err := planDevice(input, profilePlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the temperature fixture: %v", err)
	}
	if wantPlan.Kind != upstreamDeviceKindSensor || gotPlan.Kind != upstreamDeviceKindSensor {
		t.Fatalf("temperature device kinds = %q/%q, want sensor/sensor", wantPlan.Kind, gotPlan.Kind)
	}
	wantKeys := entityKeys(wantPlan.Entities)
	gotKeys := entityKeys(gotPlan.Entities)
	if !reflect.DeepEqual(gotKeys, []string{"temperature", "humidity", "battery", "linkquality"}) {
		t.Fatalf("profile entity keys = %v, want [temperature humidity battery linkquality]", gotKeys)
	}
	if !reflect.DeepEqual(gotKeys, wantKeys) {
		t.Fatalf("profile entity keys = %v, want handwritten %v", gotKeys, wantKeys)
	}
	for _, plan := range gotPlan.Entities {
		switch plan.Descriptor.Key {
		case "temperature", "humidity", "battery":
			if len(plan.GetProperties) != 1 || plan.TranslateCommand != nil {
				t.Fatalf("%s must stay a refreshed read-only sensor: get=%v translator=%v",
					plan.Descriptor.Key, plan.GetProperties, plan.TranslateCommand != nil)
			}
		case "linkquality":
			if len(plan.GetProperties) != 0 || plan.TranslateCommand != nil {
				t.Fatalf("publish-only linkquality must carry no get route: get=%v translator=%v",
					plan.GetProperties, plan.TranslateCommand != nil)
			}
		default:
			t.Fatalf("unexpected temperature fixture entity %q", plan.Descriptor.Key)
		}
	}
}

// This test protects exact sensor conversions and fails if profile planning
// changes any decoded value: milli-Celsius temperature, fractional percent
// humidity, or exact-integer linkquality. The defect would be a profile
// number format that silently rescales or truncates reports.
func TestSensorLinkqualityProfilesPreserveExactConversions(t *testing.T) {
	t.Parallel()
	profilePlanners := parityProfilePlanners(t)
	var temperatureDevice upstreamDevice
	for _, device := range parityFixtureDevices(t, "bridge-devices-temperature.json") {
		temperatureDevice = device
	}
	input := profilePlanningInput(temperatureDevice, temperatureDevice.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the temperature fixture: %v", err)
	}
	gotPlan, err := planDevice(input, profilePlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the temperature fixture: %v", err)
	}
	byKey := func(plans []entityPlan) map[string]entityPlan {
		indexed := make(map[string]entityPlan, len(plans))
		for _, plan := range plans {
			indexed[plan.Descriptor.Key] = plan
		}
		return indexed
	}
	want, got := byKey(wantPlan.Entities), byKey(gotPlan.Entities)
	cases := []struct {
		key      string
		property string
		payload  string
		semantic any
	}{
		{"temperature", "temperature", `21.5`, int64(21500)},
		{"humidity", "humidity", `50.5`, 50.5},
		{"battery", "battery", `99.25`, 99.25},
		{"linkquality", "linkquality", `42`, 42.0},
	}
	for _, testCase := range cases {
		wantReport, wantPresent, wantErr := want[testCase.key].DecodeState("entity-test",
			map[string]json.RawMessage{testCase.property: json.RawMessage(testCase.payload)},
			time.Now().UTC())
		gotReport, gotPresent, gotErr := got[testCase.key].DecodeState("entity-test",
			map[string]json.RawMessage{testCase.property: json.RawMessage(testCase.payload)},
			time.Now().UTC())
		if wantErr != nil || gotErr != nil || !wantPresent || !gotPresent {
			t.Fatalf("%s decode of %s: want (%v,%v) got (%v,%v)",
				testCase.key, testCase.payload, wantPresent, wantErr, gotPresent, gotErr)
		}
		if !reflect.DeepEqual(gotReport.semantic, testCase.semantic) ||
			!reflect.DeepEqual(wantReport.semantic, testCase.semantic) {
			t.Fatalf("%s decode of %s: want %v got %v, expected %v",
				testCase.key, testCase.payload, wantReport.semantic, gotReport.semantic, testCase.semantic)
		}
	}
	// Exact-integer linkquality rejects fractions on both paths without
	// suppressing the valid sibling comparison above.
	for name, plans := range map[string]map[string]entityPlan{"handwritten": want, "profile": got} {
		_, _, decodeErr := plans["linkquality"].DecodeState("entity-test",
			map[string]json.RawMessage{"linkquality": json.RawMessage(`42.5`)}, time.Now().UTC())
		if decodeErr == nil {
			t.Fatalf("%s linkquality accepted fractional 42.5, want exact-integer rejection", name)
		}
	}
}

// This test protects link-quality device-wide exact-one selection and fails
// if the profile evaluates each duplicate root independently. Two resolved
// endpoint roots are ambiguous, and one valid root plus one unresolved
// duplicate remains ambiguous: both paths must omit link quality entirely.
func TestLinkqualityProfilePreservesUniqueRootAmbiguity(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	linkquality := evalTestProfile(t, catalog, "linkquality")
	resolvedLeft := evalTestNumericRoot("linkquality", "linkquality_left", "")
	resolvedLeft.Endpoint = "left"
	resolvedRight := evalTestNumericRoot("linkquality", "linkquality_right", "")
	resolvedRight.Endpoint = "right"
	unresolved := evalTestNumericRoot("linkquality", "linkquality_broken", "")
	unresolved.Endpoint = "missing"
	resolvedDuplicates := evalTestSwitchDevice(
		"Fixture", "EVAL", "", resolvedLeft, resolvedRight)
	resolvedDuplicates.Endpoints = map[string]upstreamEndpoint{
		"1": {Name: "left"},
		"2": {Name: "right"},
	}
	devices := map[string]upstreamDevice{
		"resolved endpoint duplicates": resolvedDuplicates,
		"valid plus unresolved duplicate": evalTestSwitchDevice(
			"Fixture", "EVAL", "", evalTestNumericRoot("linkquality", "linkquality", ""), unresolved),
	}
	for name, device := range devices {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			input := profilePlanningInput(device, device.IEEEAddress)
			want := linkqualityPlanner{}.Plan(input)
			got := evaluatePlannerProfile(linkquality, input, catalog.overrides, catalog.strategies)
			if difference := parityContributionDifference(want, got); difference != "" {
				t.Fatalf("duplicate-root parity %s: want %+v got %+v", difference, want, got)
			}
			if len(got.Entities) != 0 {
				t.Fatalf("duplicate linkquality roots planned %d entities, want none", len(got.Entities))
			}
		})
	}
}

// This test protects malformed sibling isolation and fails if one malformed
// sensor root suppresses valid siblings under either planner: the duplicate
// property omits only its candidate while the valid battery survives on
// both paths.
func TestSensorProfilesIsolateMalformedSiblings(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	sensors := evalTestProfile(t, catalog, "ambient-sensors")
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("humidity", "shared", "%"),
		evalTestNumericRoot("battery", "shared", "%"),
		evalTestNumericRoot("battery", "battery", "%"),
	)
	input := profilePlanningInput(device, device.IEEEAddress)
	want := sensorPlanner{}.Plan(input)
	got := evaluatePlannerProfile(sensors, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("malformed sibling isolation %s", difference)
	}
	if got := entityKeys(got.Entities); len(got) != 1 || got[0] != "battery" {
		t.Fatalf("isolated entities = %v, want only the valid [battery]", got)
	}
}

// This test protects the differential harness sensitivity and fails if a
// mutated profile escapes detection: restricting humidity to parts-per-million
// must diverge from the handwritten oracle on the temperature fixture. A
// passing mutation would prove the parity comparisons are blind.
func TestSensorParityHarnessDetectsProfileDrift(t *testing.T) {
	t.Parallel()
	rule := evalTestRuleJSON("sensor.humidity", `{"kind":"root"}`,
		"numeric-sensor",
		`{"accepted_units":["ppm"],"unit":"%","number_format":"float",`+
			`"bounds":{"mode":"fixed","minimum":0,"maximum":100}}`,
		"humidity", "Humidity")
	groups := evalTestGroupJSON("sensor.humidity-roots", "numeric", "humidity", "", rule)
	mutated := evalTestCatalog(t, map[string]string{
		"profiles/sensors.profile.json": evalTestPlannerJSON(
			"ambient-sensors", 30, "sensor", "supplemental", groups, ""),
	})
	var temperatureDevice upstreamDevice
	for _, device := range parityFixtureDevices(t, "bridge-devices-temperature.json") {
		temperatureDevice = device
	}
	input := profilePlanningInput(temperatureDevice, temperatureDevice.IEEEAddress)
	want := sensorPlanner{}.Plan(input)
	got := evaluatePlannerProfile(
		mutated.profiles[0], input, mutated.overrides, mutated.strategies)
	if difference := parityContributionDifference(want, got); difference == "" {
		t.Fatal("mutated humidity units escaped the parity comparison, want detected drift")
	}
}

// This test protects tolerant top-level software build id decoding and fails
// if any JSON shape rejects the device: absent, null, and non-string values
// decode as absent while strings are retained for override selection.
func TestUpstreamDeviceDecodesSoftwareBuildIDTolerantly(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		field string
		want  string
	}{
		{"absent", ``, ""},
		{"string", `"software_build_id":"1.01.01"`, "1.01.01"},
		{"empty string", `"software_build_id":""`, ""},
		{"null", `"software_build_id":null`, ""},
		{"number", `"software_build_id":1.01`, ""},
		{"boolean", `"software_build_id":true`, ""},
		{"object", `"software_build_id":{"build":"1"}`, ""},
		{"array", `"software_build_id":["1"]`, ""},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			payload := `{"ieee_address":"0x00124b0024abcdef","type":"Router","supported":true,` +
				`"friendly_name":"build-fixture","interview_state":"SUCCESSFUL"`
			if testCase.field != "" {
				payload += "," + testCase.field
			}
			payload += `,"endpoints":{},"definition":{"model":"M","vendor":"V","exposes":[]}}`
			device, err := decodeUpstreamDevice([]byte(payload))
			if err != nil {
				t.Fatalf("software_build_id shape %s rejected the device: %v", testCase.name, err)
			}
			if string(device.SoftwareBuildID) != testCase.want {
				t.Fatalf("software_build_id = %q, want %q", string(device.SoftwareBuildID), testCase.want)
			}
		})
	}
}

// This test protects identity stability across firmware evidence and fails
// if the software build id leaks into any canonical identity: the same
// device with and without firmware evidence must register byte-identical
// binding, device, and entity descriptors.
func TestSoftwareBuildIDNeverEntersDiscoveryIdentity(t *testing.T) {
	t.Parallel()
	withoutBuild := eligibleSensorDevice("temperature",
		exposePublishAccessBit|exposeGetAccessBit)
	withBuild := withoutBuild
	withBuild.SoftwareBuildID = "1.01.01"
	want, wantRejection := discoverDevice(withoutBuild)
	got, gotRejection := discoverDevice(withBuild)
	if wantRejection != nil || gotRejection != nil {
		t.Fatalf("rejections = %+v/%+v, want none", wantRejection, gotRejection)
	}
	if !reflect.DeepEqual(want.Registration, got.Registration) {
		t.Fatalf("registration diverged with firmware evidence:\nwant %#v\ngot  %#v",
			want.Registration, got.Registration)
	}
	if len(want.Entities) != len(got.Entities) {
		t.Fatalf("entity count = %d, want %d", len(got.Entities), len(want.Entities))
	}
	for index := range want.Entities {
		if difference := parityPlanDifference(want.Entities[index], got.Entities[index]); difference != "" {
			t.Fatalf("entity %d %s with firmware evidence", index, difference)
		}
	}
	input := profilePlanningInput(withBuild, withBuild.IEEEAddress)
	if input.Vendor != "Fixture" || input.Model != "TEST" || input.SoftwareBuildID != "1.01.01" {
		t.Fatalf("evaluator input = %+v, want vendor/model/build evidence carried", input)
	}
}
