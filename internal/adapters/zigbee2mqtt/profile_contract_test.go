package zigbee2mqtt //nolint:testpackage // Contract tests exercise package-private discovery and wire DTOs.

// This file provides independent literal oracles for profile-backed
// discovery, never comparisons against a handwritten planner. Together with
// the focused discovery and runtime suites, these tests pin captured device
// kinds, ordered keys, representative descriptors, route classes, decoded
// values, command payloads, refresh behavior, and outcome policy after the
// differential harness is removed at cutover.

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	contractcolorhsv1 "github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	contractcolorxyv1 "github.com/mholtzscher/hearth/entitytypes/colorxyv1"
	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
)

// contractContributions evaluates every embedded profile for one device
// through the production evaluator and merges them with the production
// merge, so contract tests exercise the exact production planning path.
func contractContributions(t *testing.T, device upstreamDevice) devicePlan {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	plan, err := planDevice(
		planProfileContributions(catalog, profilePlanningInput(device, device.IEEEAddress)))
	if err != nil {
		t.Fatalf("profile planning rejected the fixture: %v", err)
	}
	return plan
}

// contractProfileContribution evaluates one embedded profile for one device
// through the production evaluator.
func contractProfileContribution(t *testing.T, profileID string, device upstreamDevice) plannerContribution {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	profile := evalTestProfile(t, catalog, profileID)
	return evaluatePlannerProfile(
		profile, profilePlanningInput(device, device.IEEEAddress), catalog.overrides, catalog.strategies)
}

// contractPlansByKey indexes plans by their already-validated unique key.
func contractPlansByKey(plans []entityPlan) map[string]entityPlan {
	indexed := make(map[string]entityPlan, len(plans))
	for _, plan := range plans {
		indexed[plan.Descriptor.Key] = plan
	}
	return indexed
}

// contractDecode decodes one property payload through one plan and returns
// its semantic value, failing the test when the property that must decode
// is absent or invalid.
func contractDecode(t *testing.T, plan entityPlan, property, payload string) stateReport {
	t.Helper()
	report, present, err := plan.DecodeState("entity-test",
		map[string]json.RawMessage{property: json.RawMessage(payload)}, time.Unix(1, 0).UTC())
	if err != nil {
		t.Fatalf("decode of %s failed: %v", payload, err)
	}
	if !present {
		t.Fatalf("decode of %s was absent, want a report", payload)
	}
	return report
}

// contractTranslate runs one command through one plan with a stable entity
// ID and returns the wire payload with the planned command.
func contractTranslate(t *testing.T, plan entityPlan, operation, parameters string) ([]byte, plannedCommand) {
	t.Helper()
	command := testCommand("entity-test", parameters)
	if operation == "trigger" {
		command = testTriggerCommand("entity-test", parameters)
	} else if operation != "set" {
		t.Fatalf("unknown contract operation %q", operation)
	}
	route := commandRoute{entityID: "entity-test", entity: runtimeEntity{plan: plan, entityID: "entity-test"}}
	payload, planned, err := translateCommand(
		context.Background(), route, command,
		newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
	)
	if err != nil {
		t.Fatalf("contract translate %s %s failed: %v", operation, parameters, err)
	}
	return payload, planned
}

// requireContractCommand asserts that one command produces the expected wire
// payload, refresh routes, outcome policy, and matcher behavior. The defect
// would be a profile that publishes different MQTT or satisfies different
// reports than the captured contract.
func requireContractCommand(
	t *testing.T,
	key, operation, parameters string,
	plan entityPlan,
	wantPayload string,
	wantRefresh []string,
	wantOutcome plannedOutcome,
	matchSemantic, foreignSemantic any,
) {
	t.Helper()
	payload, planned := contractTranslate(t, plan, operation, parameters)
	if wantPayload != "" && string(payload) != wantPayload {
		t.Fatalf("%s command payload = %s, want %s", key, payload, wantPayload)
	}
	if !reflect.DeepEqual(planned.GetProperties, wantRefresh) {
		t.Fatalf("%s refresh = %v, want %v", key, planned.GetProperties, wantRefresh)
	}
	if planned.Outcome != wantOutcome {
		t.Fatalf("%s outcome = %v, want %v", key, planned.Outcome, wantOutcome)
	}
	if matchSemantic == nil {
		if planned.Matches != nil {
			t.Fatalf("%s installed a matcher, want dispatched completion", key)
		}
		return
	}
	if planned.Matches == nil {
		t.Fatalf("%s has no matcher, want observed completion", key)
	}
	if !planned.Matches(stateReport{semantic: matchSemantic}) ||
		planned.Matches(stateReport{semantic: foreignSemantic}) {
		t.Fatalf("%s matcher did not enforce the commanded value", key)
	}
	if planned.Deadline.IsZero() {
		t.Fatalf("%s command has no deadline", key)
	}
}

// fixtureDevices decodes every device in one fixture, mirroring
// discoverInventory isolation for malformed array elements.
func fixtureDevices(t *testing.T, fixture string) []upstreamDevice {
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

// mustWandaDevice decodes the synthetic Wanda-shaped bulb inventory used as
// the captured light contract oracle.
func mustWandaDevice(t *testing.T) upstreamDevice {
	t.Helper()
	devices := fixtureDevices(t, "bridge-devices-wanda-synthetic.json")
	if len(devices) != 1 {
		t.Fatalf("wanda fixture must contain exactly one device, got %d", len(devices))
	}
	return devices[0]
}

// This test protects the embedded catalog shape and fails if a profile is
// added, removed, reordered, or changes its device-kind contribution: the
// planner order and kind/role combinations below are the observable family
// precedence.
func TestProfileContractCatalogKeepsFamilyOrderAndRoles(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	want := []struct {
		id    string
		order int
		kind  string
		role  plannerRole
	}{
		{id: "light", order: 10, kind: upstreamDeviceKindLight, role: plannerRolePrimary},
		{id: "relay", order: 20, kind: upstreamDeviceKindRelay, role: plannerRolePrimary},
		{id: "ambient-sensors", order: 30, kind: upstreamDeviceKindSensor, role: plannerRoleSupplemental},
		{id: "linkquality", order: 40, kind: upstreamDeviceKindSensor, role: plannerRoleSupplemental},
	}
	if len(catalog.profiles) != len(want) {
		t.Fatalf("catalog holds %d profiles, want %d", len(catalog.profiles), len(want))
	}
	for index, profile := range catalog.profiles {
		if profile.document.ID != want[index].id || profile.document.Order != want[index].order {
			t.Fatalf("profile %d = %q/%d, want %q/%d",
				index, profile.document.ID, profile.document.Order, want[index].id, want[index].order)
		}
		contribution := evaluatePlannerProfile(
			profile, profilePlanningInput(
				eligibleSensorDevice("temperature", 1), "0x00124b0024abcdef"),
			catalog.overrides, catalog.strategies)
		if contribution.Kind != want[index].kind || contribution.Role != want[index].role {
			t.Fatalf("profile %q contribution = %q/%v, want %q/%v",
				profile.document.ID, contribution.Kind, contribution.Role, want[index].kind, want[index].role)
		}
	}
}

// requireLightCapturedRouteShapes checks the Wanda fixture behavioral route
// classes: power, brightness, temperature, startup, and power-on behavior
// stay controllable with refresh; color mode stays read-only without a
// translator; effect stays a stateless dispatched action.
func requireLightCapturedRouteShapes(t *testing.T, plans map[string]entityPlan) {
	t.Helper()
	for _, key := range []string{
		"power", "brightness", "colortemp", "startupcolortemp", "poweronbehavior",
	} {
		if len(plans[key].GetProperties) != 1 || plans[key].TranslateCommand == nil {
			t.Fatalf("%s must stay controllable with refresh: get=%v translator=%v",
				key, plans[key].GetProperties, plans[key].TranslateCommand != nil)
		}
	}
	mode := plans["colormode"]
	if mode.StatePolicy == entityStateless || len(mode.StateProperties) != 1 ||
		mode.DecodeState == nil || len(mode.GetProperties) != 0 || mode.TranslateCommand != nil {
		t.Fatalf("colormode plan diverged: %#v", mode)
	}
	effect := plans["effect"]
	if effect.StatePolicy != entityStateless || len(effect.StateProperties) != 0 ||
		effect.DecodeState != nil || len(effect.GetProperties) != 0 || effect.TranslateCommand == nil {
		t.Fatalf("effect plan diverged: %#v", effect)
	}
}

// This test protects the captured Wanda bulb key order, representative power
// identity, and route classes across the mapped bulb entities.
func TestProfileContractWandaBulbKeysOrderAndRoutes(t *testing.T) {
	t.Parallel()
	plan := contractContributions(t, mustWandaDevice(t))
	if plan.Kind != upstreamDeviceKindLight {
		t.Fatalf("wanda device kind = %q, want light", plan.Kind)
	}
	wantKeys := []string{
		"power", "brightness", "colortemp", "colormode",
		"startupcolortemp", "poweronbehavior", "effect", "linkquality",
	}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("wanda keys = %v, want %v", got, wantKeys)
	}
	byKey := contractPlansByKey(plan.Entities)
	power := byKey["power"]
	if power.Descriptor.ExternalID != "0x00124b0022a9b101/root/power" ||
		power.Descriptor.Name != "Power" || power.Descriptor.Type != "hearth.power/v1" {
		t.Fatalf("wanda power descriptor = %#v", power.Descriptor)
	}
	for _, key := range wantKeys {
		if byKey[key].Descriptor.Key == "" || byKey[key].Descriptor.ExternalID == "" ||
			byKey[key].Descriptor.Name == "" || byKey[key].Descriptor.Type == "" {
			t.Fatalf("wanda entity %q has an incomplete descriptor: %#v", key, byKey[key].Descriptor)
		}
	}
	requireLightCapturedRouteShapes(t, byKey)
}

// This test protects exact color representation discovery and fails if any
// representation combination discovers different entities: XY-only, HS-only,
// dual, and multi-endpoint devices must keep the captured key order.
func TestProfileContractColorRepresentationKeysAndOrder(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		fixture string
		want    []string
	}{
		{
			fixture: "bridge-devices-color-dual.json",
			want:    []string{"power", "brightness", "colortemp", "colorxy", "colorhs", "colormode", "linkquality"},
		},
		{
			fixture: "bridge-devices-color-xy-only.json",
			want:    []string{"power", "brightness", "colorxy", "colormode"},
		},
		{
			fixture: "bridge-devices-color-hs-only.json",
			want:    []string{"power", "colorhs", "colormode"},
		},
		{
			fixture: "bridge-devices-color-endpoints.json",
			want: []string{
				"power-ep1", "brightness-ep1", "colortemp-ep1", "colorxy-ep1", "colormode-ep1",
				"power-ep2", "colortemp-ep2", "colorhs-ep2", "colormode-ep2",
			},
		},
	} {
		t.Run(test.fixture, func(t *testing.T) {
			t.Parallel()
			devices := fixtureDevices(t, test.fixture)
			if len(devices) != 1 {
				t.Fatalf("fixture %s must contain exactly one device", test.fixture)
			}
			plan := contractContributions(t, devices[0])
			if plan.Kind != upstreamDeviceKindLight {
				t.Fatalf("%s kind = %q, want light", test.fixture, plan.Kind)
			}
			if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("%s keys = %v, want %v", test.fixture, got, test.want)
			}
		})
	}
}

// requireContractSameMessageColorObservations decodes representative
// same-message payloads through the dual-color entities and fails unless the
// active representation carries values with exact observation literals while
// siblings go inactive and the mode companion reports the mode.
func requireContractSameMessageColorObservations(t *testing.T, device upstreamDevice) {
	t.Helper()
	entities := bindPlans(contractContributions(t, device).Entities)
	receivedAt := time.Unix(1, 0).UTC()
	decodeObservations := func(payload string) map[string]string {
		t.Helper()
		states, issues, err := decodeDeviceState([]byte(payload), entities, receivedAt)
		if err != nil || len(issues) != 0 {
			t.Fatalf("payload %s: err=%v issues=%v, want clean assembly", payload, err, issues)
		}
		values := make(map[string]string, len(states))
		for _, state := range states {
			values[state.entityID] = string(state.report.Observation.Value)
		}
		return values
	}
	xyValues := decodeObservations(
		`{"state":"ON","brightness":254,"color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},"color_mode":"xy"}`)
	if xyValues["entity-power"] != "true" ||
		xyValues["entity-colorxy"] != `{"active":true,"x":3125,"y":3291}` ||
		xyValues["entity-colorhs"] != `{"active":false,"hue":120,"saturation":80}` ||
		xyValues["entity-colormode"] != `"xy"` {
		t.Fatalf("xy observations = %v", xyValues)
	}
	hsValues := decodeObservations(
		`{"state":"ON","brightness":254,"color":{"x":0.3125,"y":0.3291,"hue":120,"saturation":80},"color_mode":"hs"}`)
	if hsValues["entity-colorxy"] != `{"active":false,"x":3125,"y":3291}` ||
		hsValues["entity-colorhs"] != `{"active":true,"hue":120,"saturation":80}` ||
		hsValues["entity-colormode"] != `"hs"` {
		t.Fatalf("hs observations = %v", hsValues)
	}
	tempValues := decodeObservations(`{"state":"ON","color_temp":370,"color_mode":"color_temp"}`)
	if tempValues["entity-colortemp"] != `{"active":true,"value":370}` ||
		tempValues["entity-colormode"] != `"color_temp"` {
		t.Fatalf("temperature observations = %v", tempValues)
	}
	inactiveValues := decodeObservations(`{"state":"ON","color_temp":370,"color_mode":"xy"}`)
	if inactiveValues["entity-colortemp"] != `{"active":false,"value":370}` {
		t.Fatalf("inactive temperature observations = %v", inactiveValues)
	}
}

// This test protects exact light state conversions and fails if profile
// planning changes any decoded value: discovered power scalars, percent
// brightness scaling with rounding, the startup sentinel mapping to the
// previous choice, or power-on behavior choices. Off-choice behavior and
// fractional startup stay rejected.
func TestProfileContractLightStateConversions(t *testing.T) {
	t.Parallel()
	devices := fixtureDevices(t, "bridge-devices-color-dual.json")
	if len(devices) != 1 {
		t.Fatal("dual fixture must contain exactly one device")
	}
	byKey := contractPlansByKey(contractContributions(t, devices[0]).Entities)
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
		semantic any
	}{
		{"power", "state", `"ON"`, true},
		{"power", "state", `"OFF"`, false},
		{"brightness", "brightness", `254`, int64(100)},
		{"brightness", "brightness", `0`, int64(0)},
		{"brightness", "brightness", `127`, int64(50)},
	} {
		report := contractDecode(t, byKey[testCase.key], testCase.property, testCase.payload)
		if report.semantic != testCase.semantic {
			t.Fatalf("%s decode of %s = %v, want %v",
				testCase.key, testCase.payload, report.semantic, testCase.semantic)
		}
	}
	requireContractSameMessageColorObservations(t, devices[0])
	// Startup and power-on behavior live on the bulb device that carries
	// the sentinel preset and the full choice set.
	bulbByKey := contractPlansByKey(contractContributions(t, bulbTestDevice()).Entities)
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
	}{
		{"startupcolortemp", "color_temp_startup", `250`},
		{"startupcolortemp", "color_temp_startup", `65535`},
		{"poweronbehavior", "power_on_behavior", `"previous"`},
	} {
		contractDecode(t, bulbByKey[testCase.key], testCase.property, testCase.payload)
	}
	sentinel := contractDecode(t, bulbByKey["startupcolortemp"], "color_temp_startup", `65535`)
	choice := "previous"
	if state, ok := sentinel.semantic.(contractnumericsettingv1.State); !ok ||
		state.Mode != "choice" || state.Choice == nil || *state.Choice != choice {
		t.Fatalf("startup 65535 semantic = %#v, want choice previous", sentinel.semantic)
	}
	for _, testCase := range []struct {
		name     string
		key      string
		property string
		payload  string
	}{
		{"off-choice behavior", "poweronbehavior", "power_on_behavior", `"turbo"`},
		{"fractional startup", "startupcolortemp", "color_temp_startup", `250.5`},
	} {
		if _, _, err := bulbByKey[testCase.key].DecodeState("entity-test",
			map[string]json.RawMessage{testCase.property: json.RawMessage(testCase.payload)},
			time.Now().UTC()); err == nil {
			t.Fatalf("%s accepted %s, want rejection", testCase.name, testCase.payload)
		}
	}
}

// This test protects light command plans and fails if any light command
// publishes different MQTT, refreshes different properties, uses a
// different outcome policy, or satisfies different reports: power in both
// directions with the discovered scalars, brightness percent, color
// temperature, XY, HS, startup value and previous, power-on behavior, and
// the dispatched effect trigger.
func TestProfileContractLightCommands(t *testing.T) {
	t.Parallel()
	devices := fixtureDevices(t, "bridge-devices-color-dual.json")
	if len(devices) != 1 {
		t.Fatal("dual fixture must contain exactly one device")
	}
	byKey := contractPlansByKey(contractContributions(t, devices[0]).Entities)
	requireContractCommand(t, "power", "set", `{"value":true}`,
		byKey["power"], `{"state":"ON"}`, []string{"state"}, plannedObserved, true, false)
	requireContractCommand(t, "power", "set", `{"value":false}`,
		byKey["power"], `{"state":"OFF"}`, []string{"state"}, plannedObserved, false, true)
	brightnessPayload, brightnessPlanned := contractTranslate(t, byKey["brightness"], "set", `{"value":50}`)
	if string(brightnessPayload) != `{"brightness":127}` && string(brightnessPayload) != `{"brightness":128}` {
		t.Fatalf("brightness 50 payload = %s, want the scaled value", brightnessPayload)
	}
	if brightnessPlanned.Outcome != plannedObserved || brightnessPlanned.Matches == nil {
		t.Fatalf("brightness command must complete observed with a matcher: %#v", brightnessPlanned)
	}
	if !brightnessPlanned.Matches(stateReport{semantic: int64(50)}) ||
		brightnessPlanned.Matches(stateReport{semantic: int64(51)}) {
		t.Fatal("brightness matcher did not enforce the commanded percent")
	}
	requireContractCommand(t, "colortemp", "set", `{"value":370}`,
		byKey["colortemp"], "", []string{"color_temp"}, plannedObserved,
		contractcolortempv1.State{Active: true, Value: 370},
		contractcolortempv1.State{Active: true, Value: 371})
	requireContractCommand(t, "colorxy", "set", `{"x":3125,"y":3291}`,
		byKey["colorxy"], "", []string{"color"}, plannedObserved,
		contractcolorxyv1.State{Active: true, X: 3125, Y: 3291},
		contractcolorxyv1.State{Active: true, X: 4000, Y: 1000})
	requireContractCommand(t, "colorhs", "set", `{"hue":120,"saturation":80}`,
		byKey["colorhs"], "", []string{"color"}, plannedObserved,
		contractcolorhsv1.State{Active: true, Hue: 120, Saturation: 80},
		contractcolorhsv1.State{Active: true, Hue: 200, Saturation: 20})
	// Startup, behavior, and effect live on the bulb device that carries
	// the sentinel preset and the full choice sets.
	bulbByKey := contractPlansByKey(contractContributions(t, bulbTestDevice()).Entities)
	value := 250.0
	other := 251.0
	previous := "previous"
	requireContractCommand(t, "startupcolortemp", "set", `{"mode":"value","value":250}`,
		bulbByKey["startupcolortemp"], "", []string{"color_temp_startup"}, plannedObserved,
		contractnumericsettingv1.State{Mode: "value", Value: &value},
		contractnumericsettingv1.State{Mode: "value", Value: &other})
	requireContractCommand(t, "startupcolortemp", "set", `{"mode":"choice","choice":"previous"}`,
		bulbByKey["startupcolortemp"], "", []string{"color_temp_startup"}, plannedObserved,
		contractnumericsettingv1.State{Mode: "choice", Choice: &previous},
		contractnumericsettingv1.State{Mode: "value", Value: &value})
	previousValue := contractEnumSettingState("previous")
	offChoice := contractEnumSettingState("off")
	requireContractCommand(t, "poweronbehavior", "set", `{"value":"previous"}`,
		bulbByKey["poweronbehavior"], "", []string{"power_on_behavior"}, plannedObserved,
		previousValue, offChoice)
	requireContractCommand(t, "effect", "trigger", `{"name":"breathe"}`,
		bulbByKey["effect"], `{"effect":"breathe"}`, nil, plannedDispatched, nil, nil)
	// Out-of-range values, off-choice options, fractional startup, and the
	// wrong effect operation are rejected before MQTT.
	for _, testCase := range []struct {
		name       string
		key        string
		operation  string
		parameters string
		bulb       bool
	}{
		{"brightness out of range", "brightness", "set", `{"value":101}`, false},
		{"colortemp out of range", "colortemp", "set", `{"value":1}`, false},
		{"xy out of range", "colorxy", "set", `{"x":10001,"y":3291}`, false},
		{"hs out of range", "colorhs", "set", `{"hue":400,"saturation":80}`, false},
		{"startup fractional", "startupcolortemp", "set", `{"mode":"value","value":250.5}`, true},
		{"startup out of range", "startupcolortemp", "set", `{"mode":"value","value":141}`, true},
		{"behavior off choices", "poweronbehavior", "set", `{"value":"turbo"}`, true},
		{"effect off values", "effect", "trigger", `{"name":"party"}`, true},
		{"effect wrong operation", "effect", "set", `{"name":"breathe"}`, true},
	} {
		plans := byKey
		if testCase.bulb {
			plans = bulbByKey
		}
		route := commandRoute{
			entityID: "entity-test",
			entity:   runtimeEntity{plan: plans[testCase.key], entityID: "entity-test"},
		}
		command := testCommand("entity-test", testCase.parameters)
		if testCase.operation == "trigger" {
			command = testTriggerCommand("entity-test", testCase.parameters)
		}
		_, _, translateErr := translateCommand(context.Background(), route, command,
			newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})))
		if translateErr == nil {
			t.Fatalf("contract accepted %s, want rejection", testCase.name)
		}
	}
}

// requireRelayCapturedRouteShapes checks the plug fixture's behavioral route
// classes: relay power stays controllable with refresh; electrical sensors
// stay publish-only reads without refresh; settings stay controllable with
// refresh; reset stays a stateless dispatched action.
func requireRelayCapturedRouteShapes(t *testing.T, plans map[string]entityPlan) {
	t.Helper()
	for _, key := range []string{
		"power", "poweronbehavior", "ledbrightness", "countdowntoturnoff", "countdowntoturnon",
	} {
		if len(plans[key].GetProperties) != 1 || plans[key].TranslateCommand == nil {
			t.Fatalf("%s must stay controllable with refresh: get=%v translator=%v",
				key, plans[key].GetProperties, plans[key].TranslateCommand != nil)
		}
	}
	for _, key := range []string{
		"acfrequency", "electricalpower", "powerfactor", "energy", "current", "voltage",
	} {
		if len(plans[key].GetProperties) != 0 || plans[key].TranslateCommand != nil {
			t.Fatalf("%s must stay a publish-only read: get=%v translator=%v",
				key, plans[key].GetProperties, plans[key].TranslateCommand != nil)
		}
	}
	reset := plans["resettotalenergy"]
	if reset.StatePolicy != entityStateless || len(reset.StateProperties) != 0 ||
		reset.DecodeState != nil || len(reset.GetProperties) != 0 || reset.TranslateCommand == nil {
		t.Fatalf("reset plan diverged: %#v", reset)
	}
}

// This test protects the captured smart-plug contract and fails on any
// descriptor, type, support, route, or order drift across all plug entities.
func TestProfileContractPlugKeysOrderAndRoutes(t *testing.T) {
	t.Parallel()
	discovered, rejection := discoverDevice(mustPlugDevice(t), mustEmbeddedProfileCatalog(t))
	if rejection != nil {
		t.Fatalf("plug rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != upstreamDeviceKindRelay {
		t.Fatalf("plug kind = %q, want relay", discovered.Registration.Device.Kind)
	}
	wantKeys := []string{
		"power", "poweronbehavior", "acfrequency", "electricalpower", "powerfactor",
		"energy", "current", "voltage", "ledbrightness", "countdowntoturnoff",
		"countdowntoturnon", "resettotalenergy", "linkquality",
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("plug keys = %v, want %v", got, wantKeys)
	}
	byKey := contractPlansByKey(discovered.Entities)
	power := byKey["power"]
	if power.Descriptor.ExternalID != "0x00124b0024abcd03/root/power" ||
		power.Descriptor.Name != "Power" || power.Descriptor.Type != "hearth.power/v1" {
		t.Fatalf("plug power descriptor = %#v", power.Descriptor)
	}
	for _, key := range wantKeys {
		if byKey[key].Descriptor.Key == "" || byKey[key].Descriptor.ExternalID == "" ||
			byKey[key].Descriptor.Name == "" || byKey[key].Descriptor.Type == "" {
			t.Fatalf("plug entity %q has an incomplete descriptor: %#v", key, byKey[key].Descriptor)
		}
	}
	requireRelayCapturedRouteShapes(t, byKey)
}

// This test protects exact plug state conversions and fails if profile
// planning changes any decoded value: discovered power scalars, power-on
// behavior choices, every electrical reading, or every numeric setting.
// Fractions survive on float electrical sensors; the LED setting keeps
// value-mode semantics. Off-choice power-on behavior stays rejected.
func TestProfileContractPlugStateConversions(t *testing.T) {
	t.Parallel()
	byKey := contractPlansByKey(contractContributions(t, mustPlugDevice(t)).Entities)
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
	}{
		{"power", "state", `"ON"`},
		{"power", "state", `"OFF"`},
		{"poweronbehavior", "power_on_behavior", `"previous"`},
		{"acfrequency", "ac_frequency", `60`},
		{"electricalpower", "power", `0`},
		{"powerfactor", "power_factor", `0`},
		{"energy", "energy", `0.01`},
		{"current", "current", `0`},
		{"voltage", "voltage", `119.5`},
		{"ledbrightness", "led_brightness", `100`},
		{"countdowntoturnoff", "countdown_to_turn_off", `0`},
		{"countdowntoturnon", "countdown_to_turn_on", `0`},
	} {
		contractDecode(t, byKey[testCase.key], testCase.property, testCase.payload)
	}
	if report := contractDecode(t, byKey["voltage"], "voltage", `230.5`); report.semantic != 230.5 {
		t.Fatalf("voltage 230.5 decoded to %v, want the preserved fraction", report.semantic)
	}
	led := contractDecode(t, byKey["ledbrightness"], "led_brightness", `50.5`)
	state, ok := led.semantic.(contractnumericsettingv1.State)
	if !ok || state.Mode != "value" || state.Value == nil || *state.Value != 50.5 {
		t.Fatalf("LED 50.5 semantic = %#v, want value-mode 50.5", led.semantic)
	}
	if _, _, err := byKey["poweronbehavior"].DecodeState("entity-test",
		map[string]json.RawMessage{"power_on_behavior": json.RawMessage(`"turbo"`)},
		time.Now().UTC()); err == nil {
		t.Fatal("power-on behavior accepted off-choices turbo")
	}
}

// This test protects plug command plans and fails if any relay command
// publishes different MQTT, refreshes different properties, uses a
// different outcome policy, or satisfies different reports: power in both
// directions with the discovered scalars, one enum setting, every numeric
// setting including fractions, and the dispatched reset action.
func TestProfileContractPlugCommands(t *testing.T) {
	t.Parallel()
	byKey := contractPlansByKey(contractContributions(t, mustPlugDevice(t)).Entities)
	requireContractCommand(t, "power", "set", `{"value":true}`,
		byKey["power"], `{"state":"ON"}`, []string{"state"}, plannedObserved, true, false)
	requireContractCommand(t, "power", "set", `{"value":false}`,
		byKey["power"], `{"state":"OFF"}`, []string{"state"}, plannedObserved, false, true)
	previousValue := contractEnumSettingState("previous")
	offChoice := contractEnumSettingState("off")
	requireContractCommand(t, "poweronbehavior", "set", `{"value":"previous"}`,
		byKey["poweronbehavior"], "", []string{"power_on_behavior"}, plannedObserved,
		previousValue, offChoice)
	for _, testCase := range []struct {
		key        string
		property   string
		parameters string
		value      float64
		other      float64
	}{
		{"ledbrightness", "led_brightness", `{"mode":"value","value":50}`, 50, 51},
		{"countdowntoturnoff", "countdown_to_turn_off", `{"mode":"value","value":30}`, 30, 31},
		{"countdowntoturnon", "countdown_to_turn_on", `{"mode":"value","value":30}`, 30, 31},
	} {
		match := contractnumericsettingv1.State{Mode: "value", Value: &testCase.value}
		foreign := contractnumericsettingv1.State{Mode: "value", Value: &testCase.other}
		requireContractCommand(t, testCase.key, "set", testCase.parameters,
			byKey[testCase.key], "", []string{testCase.property}, plannedObserved,
			match, foreign)
	}
	fraction := 50.5
	fractionMatch := contractnumericsettingv1.State{Mode: "value", Value: &fraction}
	fractionForeign := contractnumericsettingv1.State{Mode: "value"}
	requireContractCommand(t, "ledbrightness", "set", `{"mode":"value","value":50.5}`,
		byKey["ledbrightness"], "", []string{"led_brightness"}, plannedObserved,
		fractionMatch, fractionForeign)
	requireContractCommand(t, "resettotalenergy", "trigger", `{"name":"Reset"}`,
		byKey["resettotalenergy"], `{"reset_total_energy":"Reset"}`, nil, plannedDispatched, nil, nil)
	// Out-of-range settings and off-values triggers are rejected before MQTT.
	for _, testCase := range []struct {
		name       string
		key        string
		operation  string
		parameters string
	}{
		{"led out of range", "ledbrightness", "set", `{"mode":"value","value":101}`},
		{"countdown out of range", "countdowntoturnoff", "set", `{"mode":"value","value":65536}`},
		{"behavior off choices", "poweronbehavior", "set", `{"value":"turbo"}`},
		{"reset off values", "resettotalenergy", "trigger", `{"name":"Wipe"}`},
	} {
		route := commandRoute{
			entityID: "entity-test",
			entity:   runtimeEntity{plan: byKey[testCase.key], entityID: "entity-test"},
		}
		command := testCommand("entity-test", testCase.parameters)
		if testCase.operation == "trigger" {
			command = testTriggerCommand("entity-test", testCase.parameters)
		}
		_, _, translateErr := translateCommand(context.Background(), route, command,
			newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})))
		if translateErr == nil {
			t.Fatalf("contract accepted %s, want rejection", testCase.name)
		}
	}
}

// This test protects sensor-only device behavior and fails if the
// temperature fixture device loses its supplemental sensor kind, entity
// order, or get behavior: temperature, humidity, and battery stay
// publish-only reads with startup refresh, while publish-only linkquality
// carries no get route.
func TestProfileContractSensorDeviceKeysUnitsAndGetBehavior(t *testing.T) {
	t.Parallel()
	devices := fixtureDevices(t, "bridge-devices-temperature.json")
	if len(devices) != 1 {
		t.Fatal("temperature fixture must contain exactly one device")
	}
	plan := contractContributions(t, devices[0])
	if plan.Kind != upstreamDeviceKindSensor {
		t.Fatalf("temperature device kind = %q, want sensor", plan.Kind)
	}
	wantSensorKeys := []string{"temperature", "humidity", "battery", "linkquality"}
	if got := entityKeys(plan.Entities); !reflect.DeepEqual(got, wantSensorKeys) {
		t.Fatalf("temperature entity keys = %v, want %v", got, wantSensorKeys)
	}
	byKey := contractPlansByKey(plan.Entities)
	for _, key := range []string{"temperature", "humidity", "battery"} {
		if len(byKey[key].GetProperties) != 1 || byKey[key].TranslateCommand != nil {
			t.Fatalf("%s must stay a refreshed read-only sensor: get=%v translator=%v",
				key, byKey[key].GetProperties, byKey[key].TranslateCommand != nil)
		}
	}
	if linkquality := byKey["linkquality"]; len(linkquality.GetProperties) != 0 ||
		linkquality.TranslateCommand != nil {
		t.Fatalf("publish-only linkquality must carry no get route: get=%v translator=%v",
			linkquality.GetProperties, linkquality.TranslateCommand != nil)
	}
}

// This test protects exact sensor conversions and fails if profile planning
// changes any decoded value: milli-Celsius temperature, fractional percent
// humidity, or exact-integer linkquality. The defect would be a profile
// number format that silently rescales or truncates reports.
func TestProfileContractSensorExactConversions(t *testing.T) {
	t.Parallel()
	devices := fixtureDevices(t, "bridge-devices-temperature.json")
	if len(devices) != 1 {
		t.Fatal("temperature fixture must contain exactly one device")
	}
	byKey := contractPlansByKey(contractContributions(t, devices[0]).Entities)
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
		semantic any
	}{
		{"temperature", "temperature", `21.5`, int64(21500)},
		{"humidity", "humidity", `50.5`, 50.5},
		{"battery", "battery", `99.25`, 99.25},
		{"linkquality", "linkquality", `42`, 42.0},
	} {
		if report := contractDecode(t, byKey[testCase.key], testCase.property, testCase.payload); !reflect.DeepEqual(
			report.semantic, testCase.semantic,
		) {
			t.Fatalf("%s decode of %s = %v, want %v",
				testCase.key, testCase.payload, report.semantic, testCase.semantic)
		}
	}
	// Exact-integer linkquality rejects fractions as a per-property issue.
	if _, _, err := byKey["linkquality"].DecodeState("entity-test",
		map[string]json.RawMessage{"linkquality": json.RawMessage(`42.5`)},
		time.Now().UTC()); err == nil {
		t.Fatal("linkquality accepted fractional 42.5, want exact-integer rejection")
	}
}

// This test protects link-quality device-wide exact-one selection and fails
// if the profile evaluates each duplicate root independently. Two resolved
// endpoint roots are ambiguous, and one valid root plus one unresolved
// duplicate remains ambiguous: both must omit link quality entirely.
func TestProfileContractLinkqualityUniqueRootAmbiguity(t *testing.T) {
	t.Parallel()
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
			contribution := contractProfileContribution(t, "linkquality", device)
			if len(contribution.Entities) != 0 {
				t.Fatalf("duplicate linkquality roots planned %d entities, want none", len(contribution.Entities))
			}
		})
	}
}

// This test protects malformed sibling isolation and fails if one malformed
// sensor root suppresses valid siblings: the duplicate property omits only
// its candidate while the valid battery survives.
func TestProfileContractSensorIsolatesMalformedSiblings(t *testing.T) {
	t.Parallel()
	device := evalTestSwitchDevice("Fixture", "EVAL", "",
		evalTestNumericRoot("humidity", "shared", "%"),
		evalTestNumericRoot("battery", "shared", "%"),
		evalTestNumericRoot("battery", "battery", "%"),
	)
	contribution := contractProfileContribution(t, "ambient-sensors", device)
	if got := entityKeys(contribution.Entities); len(got) != 1 || got[0] != "battery" {
		t.Fatalf("isolated entities = %v, want only the valid [battery]", got)
	}
}
