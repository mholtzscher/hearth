package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery and translation routes.

// Fixture provenance: testdata/bridge-devices-3rsp02028bz.json and
// testdata/state-3rsp02028bz.json are handcrafted minimal sanitized shapes
// modeled on the live Third Reality 3RSP02028BZ smart plug capture from host
// wanda on 2026-09-09 (firmware software_build_id 1.01.01, friendly_name
// office-plug-fan). Sanitization changed IEEE addresses, friendly names, and
// descriptions only. Model, vendor, expose nesting, type, name, property,
// endpoint, access, unit, scalar values, numeric bounds, and the non-retained
// state payload values are retained from the live evidence. No real hardware
// is accessed.

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
)

func mustPlugDevice(testingT interface {
	Helper()
	Fatal(...any)
},
) upstreamDevice {
	testingT.Helper()
	items, err := decodeRawArray(readFixture(testingT, "bridge-devices-3rsp02028bz.json"))
	if err != nil {
		testingT.Fatal(err)
	}
	if len(items) != 1 {
		testingT.Fatal("smart-plug fixture must contain exactly one Device")
	}
	device, err := decodeUpstreamDevice(items[0])
	if err != nil {
		testingT.Fatal(err)
	}
	return device
}

func plugExposeByName(device *upstreamDevice, name string) *upstreamExpose {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == name {
			return &device.Definition.Exposes[index]
		}
	}
	return nil
}

func plugPlanByKey(testingT interface {
	Helper()
	Fatal(...any)
},
	device discoveredDevice,
	key string,
) entityPlan {
	testingT.Helper()
	for _, entity := range device.Entities {
		if entity.Descriptor.Key == key {
			return entity
		}
	}
	testingT.Fatal("Entity " + key + " missing")
	return entityPlan{}
}

func plugTranslate(
	testingT interface {
		Helper()
		Fatal(...any)
	},
	byKey map[string]runtimeEntity,
	key, operation, parameters string,
) ([]byte, plannedCommand, error) {
	testingT.Helper()
	command := testCommand("entity-"+key, parameters)
	if operation != "set" {
		command.OperationName = operation
		if operation == "trigger" {
			command = testTriggerCommand("entity-"+key, parameters)
		}
	}
	return translateCommand(
		context.Background(),
		commandRoute{entityID: "entity-" + key, entity: byKey[key]},
		command,
		newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
	)
}

// plugExpectedDescriptor is one expected smart-plug Entity descriptor in
// deterministic planner order.
type plugExpectedDescriptor struct {
	key        string
	externalID string
	name       string
	entityType string
	support    string
}

// plugExpectedRoute is the expected State, refresh, and command route for
// one smart-plug Entity key.
type plugExpectedRoute struct {
	state    []string
	get      []string
	settable bool
}

func plugExpectedDescriptors(ieee string) []plugExpectedDescriptor {
	return []plugExpectedDescriptor{
		{
			key: "power", externalID: ieee + "/root/power", name: "Power",
			entityType: "hearth.power/v1",
			support:    `{"state":{},"operations":{"set":{}}}`,
		},
		{
			key: "poweronbehavior", externalID: ieee + "/root/poweronbehavior",
			name: "Power-On Behavior", entityType: "hearth.enumsetting/v1",
			support: `{"state":{"choices":["off","previous","on"]},"operations":{"set":{}}}`,
		},
		{
			key: "acfrequency", externalID: ieee + "/root/acfrequency",
			name: "AC Frequency", entityType: "hearth.numericsensor/v1",
			support: `{"state":{"maximum":1000,"minimum":0,"unit":"Hz"},"operations":{}}`,
		},
		{
			key: "electricalpower", externalID: ieee + "/root/electricalpower",
			name: "Electrical Power", entityType: "hearth.numericsensor/v1",
			support: `{"state":{"maximum":1000000000,"minimum":0,"unit":"W"},"operations":{}}`,
		},
		{
			key: "powerfactor", externalID: ieee + "/root/powerfactor",
			name: "Power Factor", entityType: "hearth.numericsensor/v1",
			support: `{"state":{"maximum":1,"minimum":0,"unit":"ratio"},"operations":{}}`,
		},
		{
			key: "energy", externalID: ieee + "/root/energy", name: "Energy",
			entityType: "hearth.numericsensor/v1",
			support:    `{"state":{"maximum":1000000000000000,"minimum":0,"unit":"kWh"},"operations":{}}`,
		},
		{
			key: "current", externalID: ieee + "/root/current", name: "Current",
			entityType: "hearth.numericsensor/v1",
			support:    `{"state":{"maximum":1000000,"minimum":0,"unit":"A"},"operations":{}}`,
		},
		{
			key: "voltage", externalID: ieee + "/root/voltage", name: "Voltage",
			entityType: "hearth.numericsensor/v1",
			support:    `{"state":{"maximum":1000000,"minimum":0,"unit":"V"},"operations":{}}`,
		},
		{
			key: "ledbrightness", externalID: ieee + "/root/ledbrightness",
			name: "LED Brightness", entityType: "hearth.numericsetting/v1",
			support: `{"state":{"choices":[],"maximum":100,"minimum":0,"unit":"%"},"operations":{"set":{}}}`,
		},
		{
			key: "countdowntoturnoff", externalID: ieee + "/root/countdowntoturnoff",
			name: "Countdown To Turn Off", entityType: "hearth.numericsetting/v1",
			support: `{"state":{"choices":[],"maximum":65535,"minimum":0,"unit":"s"},"operations":{"set":{}}}`,
		},
		{
			key: "countdowntoturnon", externalID: ieee + "/root/countdowntoturnon",
			name: "Countdown To Turn On", entityType: "hearth.numericsetting/v1",
			support: `{"state":{"choices":[],"maximum":65535,"minimum":0,"unit":"s"},"operations":{"set":{}}}`,
		},
		{
			key: "resettotalenergy", externalID: ieee + "/root/resettotalenergy",
			name: "Reset Total Energy", entityType: "hearth.enumaction/v1",
			support: `{"state":{},"operations":{"trigger":{"values":["Reset"]}}}`,
		},
		{
			key: "linkquality", externalID: ieee + "/root/linkquality",
			name: "Link Quality", entityType: "hearth.numericsensor/v1",
			support: `{"state":{"maximum":255,"minimum":0,"unit":"lqi"},"operations":{}}`,
		},
	}
}

func plugExpectedRoutes() map[string]plugExpectedRoute {
	return map[string]plugExpectedRoute{
		"power": {state: []string{"state"}, get: []string{"state"}, settable: true},
		"poweronbehavior": {
			state:    []string{"power_on_behavior"},
			get:      []string{"power_on_behavior"},
			settable: true,
		},
		"acfrequency":     {state: []string{"ac_frequency"}, get: nil, settable: false},
		"electricalpower": {state: []string{"power"}, get: nil, settable: false},
		"powerfactor":     {state: []string{"power_factor"}, get: nil, settable: false},
		"energy":          {state: []string{"energy"}, get: nil, settable: false},
		"current":         {state: []string{"current"}, get: nil, settable: false},
		"voltage":         {state: []string{"voltage"}, get: nil, settable: false},
		"ledbrightness": {
			state:    []string{"led_brightness"},
			get:      []string{"led_brightness"},
			settable: true,
		},
		"countdowntoturnoff": {
			state:    []string{"countdown_to_turn_off"},
			get:      []string{"countdown_to_turn_off"},
			settable: true,
		},
		"countdowntoturnon": {
			state:    []string{"countdown_to_turn_on"},
			get:      []string{"countdown_to_turn_on"},
			settable: true,
		},
		"linkquality": {state: []string{"linkquality"}, get: nil, settable: false},
	}
}

func assertPlugDeviceIdentity(testingT *testing.T, device discoveredDevice) {
	testingT.Helper()
	if device.IEEEAddress != "0x00124b0024abcd03" || device.FriendlyName != "fixture-plug-3rsp" ||
		device.Registration.BindingKey != "z2m-00124b0024abcd03" {
		testingT.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.Kind != "relay" {
		testingT.Fatalf("Device kind = %q, want relay", device.Registration.Device.Kind)
	}
	if device.Model != "3RSP02028BZ" {
		testingT.Fatalf("Device model = %q, want 3RSP02028BZ", device.Model)
	}
}

func assertPlugDescriptors(testingT *testing.T, device discoveredDevice, want []plugExpectedDescriptor) {
	testingT.Helper()
	if len(device.Registration.Entities) != len(want) || len(device.Entities) != len(want) {
		testingT.Fatalf("Entity keys = %v, want 13 Entities", entityKeys(device.Entities))
	}
	for index, entity := range want {
		got := device.Registration.Entities[index]
		if got.Key != entity.key || got.ExternalID != entity.externalID || got.Name != entity.name ||
			got.Type != entity.entityType || string(got.Support) != entity.support {
			testingT.Fatalf("Entity descriptor %d = %#v, want %#v", index, got, entity)
		}
		if device.Entities[index].Descriptor.Key != entity.key {
			testingT.Fatalf(
				"plan order %d = %q, want %q",
				index,
				device.Entities[index].Descriptor.Key,
				entity.key,
			)
		}
	}
}

func assertPlugRoutes(testingT *testing.T, device discoveredDevice, routes map[string]plugExpectedRoute) {
	testingT.Helper()
	for key, route := range routes {
		plan := plugPlanByKey(testingT, device, key)
		if !reflect.DeepEqual(plan.StateProperties, route.state) ||
			!reflect.DeepEqual(plan.GetProperties, route.get) {
			testingT.Fatalf(
				"%s routes = State %v Get %v, want State %v Get %v",
				key,
				plan.StateProperties,
				plan.GetProperties,
				route.state,
				route.get,
			)
		}
		if (plan.TranslateCommand != nil) != route.settable {
			testingT.Fatalf("%s settable = %t, want %t", key, plan.TranslateCommand != nil, route.settable)
		}
	}
}

func assertPlugResetAction(testingT *testing.T, device discoveredDevice) {
	testingT.Helper()
	reset := plugPlanByKey(testingT, device, "resettotalenergy")
	if reset.StatePolicy != entityStateless || len(reset.StateProperties) != 0 ||
		reset.DecodeState != nil || len(reset.GetProperties) != 0 || reset.TranslateCommand == nil {
		testingT.Fatalf("reset plan = %#v, want stateless dispatched action", reset)
	}
}

// This test protects the captured smart-plug discovery and fails on any
// descriptor, type, support, or route drift across all 13 Entities.
func TestDiscoverCapturedSmartPlug(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	assertPlugDeviceIdentity(t, device)
	want := plugExpectedDescriptors("0x00124b0024abcd03")
	assertPlugDescriptors(t, device, want)
	assertPlugRoutes(t, device, plugExpectedRoutes())
	assertPlugResetAction(t, device)
	if err := validateEntityPlans(device.Entities); err != nil {
		t.Fatalf("merged smart-plug plans rejected: %v", err)
	}
}

// This test protects captured smart-plug State projection and fails if any
// stateful Entity ignores its live value or if sibling isolation is lost.
// The live capture carries no power_on_behavior report, so that Entity is
// proven separately with a representative value.
func TestDecodeCapturedSmartPlugState(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	states, issues, err := decodeDeviceState(
		readFixture(t, "state-3rsp02028bz.json"),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("issues = %#v", issues)
	}
	want := map[string]string{
		"entity-power":           "true",
		"entity-acfrequency":     "60",
		"entity-electricalpower": "0",
		"entity-powerfactor":     "0",
		"entity-energy":          "0.01",
		"entity-current":         "0",
		"entity-voltage":         "119.5",
		"entity-linkquality":     "42",
	}
	if len(states) != len(want)+3 {
		t.Fatalf("states = %#v, want 11 captured Entities", states)
	}
	byID := make(map[string]string, len(states))
	for _, state := range states {
		byID[state.entityID] = string(state.report.Observation.Value)
	}
	for entityID, value := range want {
		if byID[entityID] != value {
			t.Fatalf("states = %#v, want %s = %s", byID, entityID, value)
		}
	}
	for _, key := range []string{
		"entity-ledbrightness",
		"entity-countdowntoturnoff",
		"entity-countdowntoturnon",
	} {
		if _, present := byID[key]; !present {
			t.Fatalf("states = %#v, missing %s", byID, key)
		}
	}
	semantics := make(map[string]stateReport, len(states))
	for _, state := range states {
		semantics[state.entityID] = state.report
	}
	led, ok := semantics["entity-ledbrightness"].semantic.(contractnumericsettingv1.State)
	if !ok || led.Mode != "value" || led.Value == nil || *led.Value != 100 {
		t.Fatalf("led brightness semantic = %#v", semantics["entity-ledbrightness"].semantic)
	}
	off, ok := semantics["entity-countdowntoturnoff"].semantic.(contractnumericsettingv1.State)
	if !ok || off.Mode != "value" || off.Value == nil || *off.Value != 0 {
		t.Fatalf("countdown off semantic = %#v", semantics["entity-countdowntoturnoff"].semantic)
	}
	on, ok := semantics["entity-countdowntoturnon"].semantic.(contractnumericsettingv1.State)
	if !ok || on.Mode != "value" || on.Value == nil || *on.Value != 0 {
		t.Fatalf("countdown on semantic = %#v", semantics["entity-countdowntoturnon"].semantic)
	}

	behaviorStates, behaviorIssues, err := decodeDeviceState(
		[]byte(`{"power_on_behavior":"previous"}`),
		bindPlans(device.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(behaviorIssues) != 0 || len(behaviorStates) != 1 ||
		behaviorStates[0].entityID != "entity-poweronbehavior" ||
		string(behaviorStates[0].report.Observation.Value) != `"previous"` {
		t.Fatalf("power-on behavior states = %#v, issues = %#v", behaviorStates, behaviorIssues)
	}
}

// This test protects the reported relay-timeout regression path: the
// captured ON report must satisfy an ON command and reject OFF, and both
// command directions must publish the discovered scalars.
func TestCapturedSmartPlugPowerCommand(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	entities := bindPlans(device.Entities)
	byKey := make(map[string]runtimeEntity, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	route := commandRoute{entityID: "entity-power", entity: byKey["power"]}
	for _, test := range []struct {
		parameters string
		payload    string
		value      bool
	}{
		{parameters: `{"value":true}`, payload: `{"state":"ON"}`, value: true},
		{parameters: `{"value":false}`, payload: `{"state":"OFF"}`, value: false},
	} {
		recorder := &runtimeRecorder{}
		payload, planned, err := translateCommand(
			context.Background(),
			route,
			testCommand("entity-power", test.parameters),
			newFakeResponder(recorder, newFakeSession(recorder)),
		)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != test.payload {
			t.Fatalf("payload = %s, want %s", payload, test.payload)
		}
		if !planned.Matches(stateReport{semantic: test.value}) ||
			planned.Matches(stateReport{semantic: !test.value}) {
			t.Fatalf("matcher did not enforce commanded power %t", test.value)
		}
	}
	captured, _, err := decodeDeviceState(
		[]byte(`{"state":"ON"}`),
		[]runtimeEntity{byKey["power"]},
		time.Unix(1, 0).UTC(),
	)
	if err != nil || len(captured) != 1 {
		t.Fatalf("captured power report did not decode: %#v, err %v", captured, err)
	}
	recorder := &runtimeRecorder{}
	_, planned, err := translateCommand(
		context.Background(),
		route,
		testCommand("entity-power", `{"value":true}`),
		newFakeResponder(recorder, newFakeSession(recorder)),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !planned.Matches(captured[0].report) {
		t.Fatal("captured ON report did not satisfy the ON command")
	}
}

// This test protects generic observable numeric-setting translation and
// fails if payloads, refresh, or generated SetSatisfied matching drift, or
// if out-of-range values reach MQTT publication.
func TestSmartPlugNumericSettingTranslation(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	entities := bindPlans(device.Entities)
	byKey := make(map[string]runtimeEntity, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	for _, test := range []struct {
		key        string
		property   string
		parameters string
		value      float64
		other      float64
	}{
		{
			key: "ledbrightness", property: "led_brightness",
			parameters: `{"mode":"value","value":50}`, value: 50, other: 51,
		},
		{
			key: "countdowntoturnoff", property: "countdown_to_turn_off",
			parameters: `{"mode":"value","value":30}`, value: 30, other: 31,
		},
		{
			key: "countdowntoturnon", property: "countdown_to_turn_on",
			parameters: `{"mode":"value","value":30}`, value: 30, other: 31,
		},
	} {
		payload, planned, err := plugTranslate(t, byKey, test.key, "set", test.parameters)
		if err != nil {
			t.Fatal(err)
		}
		wantPayload := `{"` + test.property + `":` + jsonNumber(int(test.value)) + `}`
		if string(payload) != wantPayload {
			t.Fatalf("%s payload = %s, want %s", test.key, payload, wantPayload)
		}
		if !reflect.DeepEqual(planned.GetProperties, []string{test.property}) {
			t.Fatalf("%s refresh = %v", test.key, planned.GetProperties)
		}
		matching := contractnumericsettingv1.State{Mode: "value", Value: &test.value}
		foreign := contractnumericsettingv1.State{Mode: "value", Value: &test.other}
		if !planned.Matches(stateReport{semantic: matching}) ||
			planned.Matches(stateReport{semantic: foreign}) {
			t.Fatalf("%s matcher did not enforce the commanded value", test.key)
		}
	}
	if _, _, err := plugTranslate(t, byKey, "ledbrightness", "set", `{"mode":"value","value":101}`); err == nil {
		t.Fatal("out-of-range LED brightness was accepted")
	}
	if _, _, err := plugTranslate(
		t,
		byKey,
		"countdowntoturnoff",
		"set",
		`{"mode":"value","value":65536}`,
	); err == nil {
		t.Fatal("out-of-range countdown was accepted")
	}
	if _, _, err := plugTranslate(
		t,
		byKey,
		"ledbrightness",
		"set",
		`{"mode":"choice","choice":"previous"}`,
	); err == nil {
		t.Fatal("choice mode was accepted with empty choices")
	}
	fractional, _, err := plugTranslate(t, byKey, "ledbrightness", "set", `{"mode":"value","value":50.5}`)
	if err != nil {
		t.Fatalf("fractional setting was rejected: %v", err)
	}
	if string(fractional) != `{"led_brightness":50.5}` {
		t.Fatalf("fractional payload = %s, want decimals preserved", fractional)
	}
}

// This test protects relay power-on behavior translation through the
// generic enum-setting constructor.
func TestSmartPlugPowerOnBehaviorTranslation(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	entities := bindPlans(device.Entities)
	byKey := make(map[string]runtimeEntity, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	payload, planned, err := plugTranslate(t, byKey, "poweronbehavior", "set", `{"value":"previous"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"power_on_behavior":"previous"}` {
		t.Fatalf("payload = %s", payload)
	}
	if !reflect.DeepEqual(planned.GetProperties, []string{"power_on_behavior"}) {
		t.Fatalf("refresh = %v", planned.GetProperties)
	}
	if !planned.Matches(stateReport{semantic: contractEnumSettingState("previous")}) ||
		planned.Matches(stateReport{semantic: contractEnumSettingState("off")}) {
		t.Fatal("power-on behavior matcher did not enforce the commanded value")
	}
	if _, _, err = plugTranslate(t, byKey, "poweronbehavior", "set", `{"value":"turbo"}`); err == nil {
		t.Fatal("off-choices power-on behavior was accepted")
	}
}

// This test protects the stateless reset action and fails if the trigger
// payload diverges, the plan is not dispatched, or off-values reach MQTT.
func TestSmartPlugResetActionDispatched(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-3rsp02028bz.json")
	entities := bindPlans(device.Entities)
	byKey := make(map[string]runtimeEntity, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	payload, planned, err := plugTranslate(t, byKey, "resettotalenergy", "trigger", `{"name":"Reset"}`)
	if err != nil {
		t.Fatal(err)
	}
	if string(payload) != `{"reset_total_energy":"Reset"}` {
		t.Fatalf("payload = %s", payload)
	}
	if planned.Outcome != plannedDispatched || len(planned.GetProperties) != 0 || planned.Matches != nil {
		t.Fatalf("reset plan is not dispatched: %#v", planned)
	}
	if _, _, err = plugTranslate(t, byKey, "resettotalenergy", "trigger", `{"name":"Wipe"}`); err == nil {
		t.Fatal("off-values reset trigger was accepted")
	}
	if _, _, err = plugTranslate(t, byKey, "resettotalenergy", "set", `{"name":"Reset"}`); err == nil {
		t.Fatal("reset set operation was accepted")
	}
}

func plugPlannedKeys(testingT *testing.T, device upstreamDevice) []string {
	testingT.Helper()
	contribution := relayPlanner{}.Plan(powerPlanningInput(device))
	return entityKeys(contribution.Entities)
}

func assertPlugVoltageOmittedWithSetAccess(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "voltage").Access = 7
	keys := plugPlannedKeys(testingT, device)
	if slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with set access: %v", keys)
	}
	for _, key := range []string{"power", "acfrequency", "current"} {
		if !slices.Contains(keys, key) {
			testingT.Fatalf("%s missing after sibling omission: %v", key, keys)
		}
	}
}

func assertPlugCurrentOmittedWithWrongUnit(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "current").Unit = "mA"
	keys := plugPlannedKeys(testingT, device)
	if slices.Contains(keys, "current") {
		testingT.Fatalf("current planned with wrong unit: %v", keys)
	}
	if !slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage missing after sibling omission: %v", keys)
	}
}

func assertPlugPowerFactorRequiresEmptyUnit(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "power_factor").Unit = "%"
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "powerfactor") {
		testingT.Fatalf("power factor planned with unit: %v", keys)
	}
}

func assertPlugVoltageOmittedWithOneSidedBounds(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	maximum := 250.0
	plugExposeByName(&device, "voltage").ValueMax = &maximum
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with one-sided bounds: %v", keys)
	}
}

func assertPlugVoltageOmittedWithMalformedBounds(
	testingT *testing.T,
	minimumRaw, maximumRaw json.RawMessage,
) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	voltage := plugExposeByName(&device, "voltage")
	voltage.valueMinRaw = minimumRaw
	voltage.valueMaxRaw = maximumRaw
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with malformed bounds: %v", keys)
	}
}

func assertPlugVoltageOmittedWithInvertedBounds(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	minimum, maximum := 300.0, 100.0
	voltage := plugExposeByName(&device, "voltage")
	voltage.ValueMin = &minimum
	voltage.ValueMax = &maximum
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with inverted bounds: %v", keys)
	}
}

func assertPlugVoltagePrefersValidUpstreamBounds(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	minimum, maximum := 100.0, 250.0
	voltage := plugExposeByName(&device, "voltage")
	voltage.ValueMin = &minimum
	voltage.ValueMax = &maximum
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		testingT.Fatalf("Device rejected: %#v", rejection)
	}
	for _, entity := range discovered.Registration.Entities {
		if entity.Key == "voltage" &&
			string(entity.Support) != `{"state":{"maximum":250,"minimum":100,"unit":"V"},"operations":{}}` {
			testingT.Fatalf("voltage support = %s", entity.Support)
		}
	}
}

func assertPlugSettingOmittedWithoutBounds(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "led_brightness").ValueMax = nil
	keys := plugPlannedKeys(testingT, device)
	if slices.Contains(keys, "ledbrightness") {
		testingT.Fatalf("LED brightness planned without bounds: %v", keys)
	}
	if !slices.Contains(keys, "countdowntoturnoff") {
		testingT.Fatalf("countdown missing after sibling omission: %v", keys)
	}
}

func assertPlugSettingOmittedWithWrongUnit(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "led_brightness").Unit = "mired"
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "ledbrightness") {
		testingT.Fatalf("LED brightness planned with wrong unit: %v", keys)
	}
}

func assertPlugVoltageOmittedWithDuplicateProperty(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
		Type: "numeric", Name: "diagnostic", Property: "voltage", Access: 1, Unit: "V",
	})
	keys := plugPlannedKeys(testingT, device)
	if slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with duplicate property: %v", keys)
	}
	if !slices.Contains(keys, "current") {
		testingT.Fatalf("current missing after sibling omission: %v", keys)
	}
}

func assertPlugVoltageOmittedWithDuplicateRoots(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	duplicate := *plugExposeByName(&device, "voltage")
	device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
	keys := plugPlannedKeys(testingT, device)
	if slices.Contains(keys, "voltage") {
		testingT.Fatalf("voltage planned with duplicate roots: %v", keys)
	}
	if !slices.Contains(keys, "current") {
		testingT.Fatalf("current missing after sibling omission: %v", keys)
	}
}

func assertPlugResetOmittedWithoutSetAccess(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	plugExposeByName(&device, "reset_total_energy").Access = 1
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "resettotalenergy") {
		testingT.Fatalf("reset planned without set access: %v", keys)
	}
}

func assertPlugResetOmittedWithDuplicateRoots(testingT *testing.T) {
	testingT.Helper()
	device := mustPlugDevice(testingT)
	duplicate := *plugExposeByName(&device, "reset_total_energy")
	device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
	if keys := plugPlannedKeys(testingT, device); slices.Contains(keys, "resettotalenergy") {
		testingT.Fatalf("reset planned with duplicate roots: %v", keys)
	}
}

// This test protects electrical eligibility and fails if access, unit, or
// bound violations register, or if valid siblings are suppressed with them.
func TestSmartPlugElectricalEligibility(t *testing.T) {
	t.Parallel()
	t.Run("set access omits only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithSetAccess(t)
	})
	t.Run("wrong unit omits only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugCurrentOmittedWithWrongUnit(t)
	})
	t.Run("power factor requires empty upstream unit", func(t *testing.T) {
		t.Parallel()
		assertPlugPowerFactorRequiresEmptyUnit(t)
	})
	t.Run("one-sided upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithOneSidedBounds(t)
	})
	t.Run("two malformed upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithMalformedBounds(t, json.RawMessage(`"low"`), json.RawMessage(`"high"`))
	})
	t.Run("one malformed and one absent bound omits only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithMalformedBounds(t, json.RawMessage(`"low"`), nil)
	})
	t.Run("inverted upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithInvertedBounds(t)
	})
	t.Run("valid upstream bounds are preferred", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltagePrefersValidUpstreamBounds(t)
	})
	t.Run("missing setting bounds omit only that setting", func(t *testing.T) {
		t.Parallel()
		assertPlugSettingOmittedWithoutBounds(t)
	})
	t.Run("wrong setting unit omits only that setting", func(t *testing.T) {
		t.Parallel()
		assertPlugSettingOmittedWithWrongUnit(t)
	})
	t.Run("duplicate sensor property omits only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithDuplicateProperty(t)
	})
	t.Run("duplicate sensor roots omit only that sensor", func(t *testing.T) {
		t.Parallel()
		assertPlugVoltageOmittedWithDuplicateRoots(t)
	})
	t.Run("reset without set access is omitted", func(t *testing.T) {
		t.Parallel()
		assertPlugResetOmittedWithoutSetAccess(t)
	})
	t.Run("duplicate reset roots are omitted", func(t *testing.T) {
		t.Parallel()
		assertPlugResetOmittedWithDuplicateRoots(t)
	})
}

// This test protects relay family gating and fails if device-root
// attributes survive without eligible relay power or suppress valid relays.
func TestSmartPlugRelayFamilyGating(t *testing.T) {
	t.Parallel()
	t.Run("invalid power gates every attribute", func(t *testing.T) {
		t.Parallel()
		device := mustPlugDevice(t)
		device.Definition.Exposes[0].Features[0].Access = 3
		contribution := relayPlanner{}.Plan(powerPlanningInput(device))
		if len(contribution.Entities) != 0 {
			t.Fatalf("relay family survived invalid power: %v", entityKeys(contribution.Entities))
		}
	})
	t.Run("invalid relay root spares the valid sibling", func(t *testing.T) {
		t.Parallel()
		device := mustPlugDevice(t)
		device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}, "2": {Name: "right"}}
		invalid := switchExpose("left", "state_left")
		invalid.Features[0].Access = 3
		exposes := []upstreamExpose{invalid, switchExpose("right", "state_right")}
		exposes = append(exposes, device.Definition.Exposes[1:]...)
		device.Definition.Exposes = exposes
		contribution := relayPlanner{}.Plan(powerPlanningInput(device))
		keys := entityKeys(contribution.Entities)
		if len(keys) == 0 || keys[0] != "power-ep2" {
			t.Fatalf("relay survivor keys = %v, want power-ep2 first", keys)
		}
	})
	t.Run("duplicate power keys gate attributes", func(t *testing.T) {
		t.Parallel()
		device := mustPlugDevice(t)
		duplicate := upstreamExpose{
			Type: "switch",
			Features: []upstreamExpose{{
				Type: "binary", Name: "state", Property: "state", Access: 7,
				ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
			}},
		}
		device.Definition.Exposes = append([]upstreamExpose{duplicate}, device.Definition.Exposes...)
		contribution := relayPlanner{}.Plan(powerPlanningInput(device))
		if len(contribution.Entities) != 0 {
			t.Fatalf("attributes survived ambiguous power: %v", entityKeys(contribution.Entities))
		}
	})
}
