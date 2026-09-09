package zigbee2mqtt //nolint:testpackage // Parity tests exercise package-private planners, discovery, and wire DTOs.

// This test protects D5 relay and smart-plug differential parity and fails
// if the embedded relay profile diverges from the handwritten relayPlanner
// over any bridge-devices fixture: contribution kind and role, ordered
// entity keys, external IDs, names, types, support documents, state and get
// properties, translator presence, decoded values, command payloads and
// outcome policy, device kind, or plan rejection code. Production discovery
// stays on handwritten planners; the profile path here is evaluation only,
// so every comparison also proves no production path changed. The final
// sensitivity cases mutate a profile copy to prove the harness itself
// detects drift.

import (
	"context"
	"encoding/json"
	"reflect"
	"slices"
	"testing"
	"time"

	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
)

// parityRelayProfilePlanners returns the hybrid planner assembly used for
// the profile path: handwritten light, sensor, and link-quality families
// with the profile-backed relay contribution. It isolates relay drift from
// the D4 sensor migration.
func parityRelayProfilePlanners(t *testing.T) []devicePlanner {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	relay := evalTestProfile(t, catalog, "relay")
	return []devicePlanner{
		lightPlanner{},
		profileParityPlanner{
			profile: relay, overrides: catalog.overrides, strategies: catalog.strategies,
		},
		sensorPlanner{},
		linkqualityPlanner{},
	}
}

// requireRelayMergedParity asserts that the profile-backed relay
// contribution merges exactly like the handwritten merge for one fixture
// device: device kind, ordered entities, or the stable plan rejection
// code. The defect would be a relay profile whose contribution merges
// differently than production.
func requireRelayMergedParity(
	t *testing.T,
	fixture string,
	device upstreamDevice,
	relayPlanners []devicePlanner,
) {
	t.Helper()
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, wantErr := planDevice(input, defaultDevicePlanners())
	gotPlan, gotErr := planDevice(input, relayPlanners)
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
		t.Fatalf("%s device %s entity count = %d, want %d (want %v got %v)",
			fixture, device.IEEEAddress, len(gotPlan.Entities), len(wantPlan.Entities),
			entityKeys(gotPlan.Entities), entityKeys(wantPlan.Entities))
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

// relayParityTranslate runs one command through one plan with a stable
// entity ID so handwritten and profile command plans can be compared
// byte-for-byte.
func relayParityTranslate(
	t *testing.T,
	plan entityPlan,
	operation, parameters string,
) ([]byte, plannedCommand) {
	t.Helper()
	command := testCommand("entity-test", parameters)
	if operation == "trigger" {
		command = testTriggerCommand("entity-test", parameters)
	} else if operation != "set" {
		t.Fatalf("unknown relay parity operation %q", operation)
	}
	route := commandRoute{entityID: "entity-test", entity: runtimeEntity{plan: plan, entityID: "entity-test"}}
	payload, planned, err := translateCommand(
		context.Background(), route, command,
		newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
	)
	if err != nil {
		t.Fatalf("relay parity translate %s %s failed: %v", operation, parameters, err)
	}
	return payload, planned
}

// requireRelayCommandParity asserts that one command produces identical
// payloads, refresh routes, outcome policy, and matcher behavior on both
// plans. The defect would be a relay profile that publishes different MQTT
// or satisfies different reports than production.
func requireRelayCommandParity(
	t *testing.T,
	key, operation, parameters string,
	want, got entityPlan,
	matchSemantic, foreignSemantic any,
) {
	t.Helper()
	wantPayload, wantPlanned := relayParityTranslate(t, want, operation, parameters)
	gotPayload, gotPlanned := relayParityTranslate(t, got, operation, parameters)
	if string(wantPayload) != string(gotPayload) {
		t.Fatalf("%s command payload = %s, want %s", key, gotPayload, wantPayload)
	}
	if !reflect.DeepEqual(gotPlanned.GetProperties, wantPlanned.GetProperties) {
		t.Fatalf("%s refresh = %v, want %v", key, gotPlanned.GetProperties, wantPlanned.GetProperties)
	}
	if gotPlanned.Outcome != wantPlanned.Outcome {
		t.Fatalf("%s outcome = %v, want %v", key, gotPlanned.Outcome, wantPlanned.Outcome)
	}
	if (gotPlanned.Matches == nil) != (wantPlanned.Matches == nil) {
		t.Fatalf("%s matcher presence diverged", key)
	}
	if wantPlanned.Matches != nil {
		if !gotPlanned.Matches(stateReport{semantic: matchSemantic}) ||
			gotPlanned.Matches(stateReport{semantic: foreignSemantic}) {
			t.Fatalf("%s profile matcher did not enforce the commanded value", key)
		}
		if !wantPlanned.Matches(stateReport{semantic: matchSemantic}) ||
			wantPlanned.Matches(stateReport{semantic: foreignSemantic}) {
			t.Fatalf("%s handwritten matcher did not enforce the commanded value", key)
		}
	}
	if !wantPlanned.Deadline.IsZero() != !gotPlanned.Deadline.IsZero() {
		t.Fatalf("%s deadline presence diverged", key)
	}
}

// requireRelayDecodeParity asserts that one state payload decodes
// identically on both plans: presence, error shape, semantic value, and
// observation bytes. The defect would be a relay profile that projects
// different Core state than production.
func requireRelayDecodeParity(
	t *testing.T,
	key, property, payload string,
	want, got entityPlan,
) stateReport {
	t.Helper()
	receivedAt := time.Unix(1, 0).UTC()
	wantReport, wantPresent, wantErr := want.DecodeState("entity-test",
		map[string]json.RawMessage{property: json.RawMessage(payload)}, receivedAt)
	gotReport, gotPresent, gotErr := got.DecodeState("entity-test",
		map[string]json.RawMessage{property: json.RawMessage(payload)}, receivedAt)
	if wantPresent != gotPresent || (wantErr == nil) != (gotErr == nil) {
		t.Fatalf("%s decode of %s: want (%v,%v) got (%v,%v)",
			key, payload, wantPresent, wantErr, gotPresent, gotErr)
	}
	if wantErr != nil {
		return wantReport
	}
	if !reflect.DeepEqual(gotReport.semantic, wantReport.semantic) {
		t.Fatalf("%s decode of %s semantic diverged: want %v got %v",
			key, payload, wantReport.semantic, gotReport.semantic)
	}
	if string(gotReport.Observation.Value) != string(wantReport.Observation.Value) {
		t.Fatalf("%s observation of %s diverged: want %s got %s",
			key, payload, wantReport.Observation.Value, gotReport.Observation.Value)
	}
	return gotReport
}

// This test protects relay contribution parity and fails if the embedded
// relay profile diverges from the handwritten relayPlanner over any
// fixture device: kind, role, ordered entities, or any plan field. The
// defect would be a relay profile that registers different smart-plug
// entities than production.
func TestRelayProfileContributionsMatchHandwritten(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	relay := evalTestProfile(t, catalog, "relay")
	if relay.document.Order != 20 || relay.document.Contribution.DeviceKind != upstreamDeviceKindRelay {
		t.Fatalf("relay profile order/kind = %d/%q, want 20/relay",
			relay.document.Order, relay.document.Contribution.DeviceKind)
	}
	for _, fixture := range parityBridgeDevicesFixtures(t) {
		for _, device := range parityFixtureDevices(t, fixture) {
			input := profilePlanningInput(device, device.IEEEAddress)
			want := relayPlanner{}.Plan(input)
			got := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
			if difference := parityContributionDifference(want, got); difference != "" {
				t.Fatalf("%s device %s relay %s: want %v got %v",
					fixture, device.IEEEAddress, difference,
					entityKeys(want.Entities), entityKeys(got.Entities))
			}
		}
	}
}

// This test protects merged device parity and fails if the profile-backed
// relay contribution changes any merged plan or rejection code: device
// kind, ordered entities, or the stable plan error must match the
// handwritten merge over every fixture device. The defect would be a relay
// profile whose contribution merges differently than production.
func TestRelayMergedPlansMatchHandwritten(t *testing.T) {
	t.Parallel()
	relayPlanners := parityRelayProfilePlanners(t)
	for _, fixture := range parityBridgeDevicesFixtures(t) {
		for _, device := range parityFixtureDevices(t, fixture) {
			requireRelayMergedParity(t, fixture, device, relayPlanners)
		}
	}
}

// relayParityPlansByKey indexes plans by their already-validated unique key.
func relayParityPlansByKey(plans []entityPlan) map[string]entityPlan {
	indexed := make(map[string]entityPlan, len(plans))
	for _, plan := range plans {
		indexed[plan.Descriptor.Key] = plan
	}
	return indexed
}

// requireRelayCapturedRouteShapes checks the plug fixture's behavioral route
// classes independently of full handwritten/profile plan equality.
func requireRelayCapturedRouteShapes(t *testing.T, plans map[string]entityPlan) {
	t.Helper()
	// Relay power stays controllable with refresh; electrical sensors stay
	// publish-only reads without refresh; settings stay controllable with
	// refresh; reset stays a stateless dispatched action.
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

// This test protects the captured smart-plug contract under profile
// planning and fails on any descriptor, type, support, route, or order
// drift across all plug entities: the merged profile plan must equal the
// handwritten plug expectations byte-for-byte.
func TestRelayPlugKeepsCapturedDescriptorsRoutesAndOrder(t *testing.T) {
	t.Parallel()
	relayPlanners := parityRelayProfilePlanners(t)
	device := mustPlugDevice(t)
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the plug fixture: %v", err)
	}
	gotPlan, err := planDevice(input, relayPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the plug fixture: %v", err)
	}
	if wantPlan.Kind != upstreamDeviceKindRelay || gotPlan.Kind != upstreamDeviceKindRelay {
		t.Fatalf("plug device kinds = %q/%q, want relay/relay", wantPlan.Kind, gotPlan.Kind)
	}
	wantKeys := []string{
		"power", "poweronbehavior", "acfrequency", "electricalpower", "powerfactor",
		"energy", "current", "voltage", "ledbrightness", "countdowntoturnoff",
		"countdowntoturnon", "resettotalenergy", "linkquality",
	}
	if got := entityKeys(gotPlan.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("profile plug keys = %v, want %v", got, wantKeys)
	}
	if got := entityKeys(wantPlan.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("handwritten plug keys = %v, want %v", got, wantKeys)
	}
	want := relayParityPlansByKey(wantPlan.Entities)
	got := relayParityPlansByKey(gotPlan.Entities)
	for _, key := range wantKeys {
		if difference := parityPlanDifference(want[key], got[key]); difference != "" {
			t.Fatalf("plug entity %s %s", key, difference)
		}
	}
	requireRelayCapturedRouteShapes(t, got)
}

// This test protects exact plug state conversions and fails if profile
// planning changes any decoded value or observation: discovered power
// scalars, power-on behavior choices, every electrical reading, every
// numeric setting, or the dispatched reset shape. The defect would be a
// relay profile number format, unit, or bound that silently rescales,
// truncates, or rewords reports.
func TestRelayPlugPreservesExactStateConversions(t *testing.T) {
	t.Parallel()
	relayPlanners := parityRelayProfilePlanners(t)
	device := mustPlugDevice(t)
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the plug fixture: %v", err)
	}
	gotPlan, err := planDevice(input, relayPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the plug fixture: %v", err)
	}
	want := relayParityPlansByKey(wantPlan.Entities)
	got := relayParityPlansByKey(gotPlan.Entities)
	cases := []struct {
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
	}
	for _, testCase := range cases {
		requireRelayDecodeParity(
			t, testCase.key, testCase.property, testCase.payload,
			want[testCase.key], got[testCase.key],
		)
	}
	// Fractions survive on float electrical sensors; the LED setting keeps
	// value-mode semantics.
	report := requireRelayDecodeParity(
		t, "voltage", "voltage", `230.5`, want["voltage"], got["voltage"],
	)
	if report.semantic != 230.5 {
		t.Fatalf("voltage 230.5 decoded to %v, want the preserved fraction", report.semantic)
	}
	led := requireRelayDecodeParity(
		t, "ledbrightness", "led_brightness", `50.5`, want["ledbrightness"], got["ledbrightness"],
	)
	state, ok := led.semantic.(contractnumericsettingv1.State)
	if !ok || state.Mode != "value" || state.Value == nil || *state.Value != 50.5 {
		t.Fatalf("LED 50.5 semantic = %#v, want value-mode 50.5", led.semantic)
	}
	// Off-choice power-on behavior is rejected on both paths without
	// suppressing the valid comparison above.
	for name, plans := range map[string]map[string]entityPlan{"handwritten": want, "profile": got} {
		_, _, decodeErr := plans["poweronbehavior"].DecodeState("entity-test",
			map[string]json.RawMessage{"power_on_behavior": json.RawMessage(`"turbo"`)}, time.Now().UTC())
		if decodeErr == nil {
			t.Fatalf("%s power-on behavior accepted off-choices turbo", name)
		}
	}
}

// This test protects plug command-plan parity and fails if any relay
// command publishes different MQTT, refreshes different properties, uses
// a different outcome policy, or satisfies different reports: power in
// both directions with the discovered scalars, one enum setting, every
// numeric setting including fractions, and the dispatched reset action.
func TestRelayPlugPreservesCommandPlans(t *testing.T) {
	t.Parallel()
	relayPlanners := parityRelayProfilePlanners(t)
	device := mustPlugDevice(t)
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the plug fixture: %v", err)
	}
	gotPlan, err := planDevice(input, relayPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the plug fixture: %v", err)
	}
	byKey := func(plans []entityPlan) map[string]entityPlan {
		indexed := make(map[string]entityPlan, len(plans))
		for _, plan := range plans {
			indexed[plan.Descriptor.Key] = plan
		}
		return indexed
	}
	want, got := byKey(wantPlan.Entities), byKey(gotPlan.Entities)
	requireRelayCommandParity(t, "power", "set", `{"value":true}`, want["power"], got["power"], true, false)
	requireRelayCommandParity(t, "power", "set", `{"value":false}`, want["power"], got["power"], false, true)
	onValue, offValue := true, false
	_ = onValue
	_ = offValue
	previousValue := contractEnumSettingState("previous")
	offChoice := contractEnumSettingState("off")
	requireRelayCommandParity(t, "poweronbehavior", "set", `{"value":"previous"}`,
		want["poweronbehavior"], got["poweronbehavior"], previousValue, offChoice)
	for _, testCase := range []struct {
		key        string
		parameters string
		value      float64
		other      float64
	}{
		{"ledbrightness", `{"mode":"value","value":50}`, 50, 51},
		{"countdowntoturnoff", `{"mode":"value","value":30}`, 30, 31},
		{"countdowntoturnon", `{"mode":"value","value":30}`, 30, 31},
	} {
		match := contractnumericsettingv1.State{Mode: "value", Value: &testCase.value}
		foreign := contractnumericsettingv1.State{Mode: "value", Value: &testCase.other}
		requireRelayCommandParity(t, testCase.key, "set", testCase.parameters,
			want[testCase.key], got[testCase.key], match, foreign)
	}
	fraction := 50.5
	fractionMatch := contractnumericsettingv1.State{Mode: "value", Value: &fraction}
	fractionForeign := contractnumericsettingv1.State{Mode: "value"}
	requireRelayCommandParity(t, "ledbrightness", "set", `{"mode":"value","value":50.5}`,
		want["ledbrightness"], got["ledbrightness"], fractionMatch, fractionForeign)
	requireRelayCommandParity(t, "resettotalenergy", "trigger", `{"name":"Reset"}`,
		want["resettotalenergy"], got["resettotalenergy"], nil, nil)
	// Out-of-range settings and off-values triggers are rejected before
	// MQTT on both paths.
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
		for name, plans := range map[string]map[string]entityPlan{"handwritten": want, "profile": got} {
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
				t.Fatalf("%s path accepted %s, want rejection", name, testCase.name)
			}
		}
	}
}

// relayParityMutation applies one device mutation to a fresh plug fixture
// so each differential case isolates exactly one eligibility dimension.
func relayParityMutation(t *testing.T, mutate func(*upstreamDevice)) upstreamDevice {
	t.Helper()
	device := mustPlugDevice(t)
	mutate(&device)
	return device
}

// requireRelayContributionParity asserts that one mutated plug device
// produces identical relay contributions on both paths with exactly the
// expected entity keys. The defect would be a relay profile that admits
// or drops a different set than production for that eligibility rule.
func requireRelayContributionParity(t *testing.T, name string, device upstreamDevice, wantKeys []string) {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	relay := evalTestProfile(t, catalog, "relay")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := relayPlanner{}.Plan(input)
	got := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("%s %s: want %v got %v", name, difference, entityKeys(want.Entities), entityKeys(got.Entities))
	}
	gotKeys := entityKeys(got.Entities)
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("%s keys = %v, want %v", name, gotKeys, wantKeys)
	}
	for index := range gotKeys {
		if gotKeys[index] != wantKeys[index] {
			t.Fatalf("%s keys = %v, want %v", name, gotKeys, wantKeys)
		}
	}
}

// relayPlugKeysWithout returns the full merged plug key list minus omitted
// relay keys. Link quality is supplemental and never affected by relay
// mutations, so it always survives here.
func relayPlugKeysWithout(omitted ...string) []string {
	full := []string{
		"power", "poweronbehavior", "acfrequency", "electricalpower", "powerfactor",
		"energy", "current", "voltage", "ledbrightness", "countdowntoturnoff",
		"countdowntoturnon", "resettotalenergy",
	}
	var keys []string
	for _, key := range full {
		if !slices.Contains(omitted, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// This test protects electrical, setting, and action eligibility parity
// and fails if any access, unit, bound, property, duplicate-root, or
// unresolved-endpoint violation registers on one path but not the other,
// or if a valid sibling is suppressed with it. Each case mutates exactly
// one dimension of the captured plug.
func TestRelayProfilesIsolateMalformedSiblings(t *testing.T) {
	t.Parallel()
	t.Run("set access omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "voltage").Access = 7
		})
		requireRelayContributionParity(t, "voltage set access", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("wrong unit omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "current").Unit = "mA"
		})
		requireRelayContributionParity(t, "current unit", device,
			relayPlugKeysWithout("current"))
	})
	t.Run("power factor requires empty upstream unit", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "power_factor").Unit = "%"
		})
		requireRelayContributionParity(t, "power factor unit", device,
			relayPlugKeysWithout("powerfactor"))
	})
	t.Run("one-sided upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			maximum := 250.0
			plugExposeByName(device, "voltage").ValueMax = &maximum
		})
		requireRelayContributionParity(t, "voltage one-sided bounds", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("two malformed upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			voltage := plugExposeByName(device, "voltage")
			voltage.valueMinRaw = json.RawMessage(`"low"`)
			voltage.valueMaxRaw = json.RawMessage(`"high"`)
		})
		requireRelayContributionParity(t, "voltage malformed bounds", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("inverted upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			minimum, maximum := 300.0, 100.0
			voltage := plugExposeByName(device, "voltage")
			voltage.ValueMin = &minimum
			voltage.ValueMax = &maximum
		})
		requireRelayContributionParity(t, "voltage inverted bounds", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("missing setting bounds omit only that setting", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "led_brightness").ValueMax = nil
		})
		requireRelayContributionParity(t, "LED bounds", device,
			relayPlugKeysWithout("ledbrightness"))
	})
	t.Run("wrong setting unit omits only that setting", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "led_brightness").Unit = "mired"
		})
		requireRelayContributionParity(t, "LED unit", device,
			relayPlugKeysWithout("ledbrightness"))
	})
	t.Run("duplicate sensor property omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
				Type: "numeric", Name: "diagnostic", Property: "voltage", Access: 1, Unit: "V",
			})
		})
		requireRelayContributionParity(t, "voltage duplicate property", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("duplicate sensor roots omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "voltage")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireRelayContributionParity(t, "voltage duplicate roots", device,
			relayPlugKeysWithout("voltage"))
	})
	t.Run("duplicate behavior roots omit only behavior", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "power_on_behavior")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireRelayContributionParity(t, "behavior duplicate roots", device,
			relayPlugKeysWithout("poweronbehavior"))
	})
	t.Run("reset without set access is omitted", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "reset_total_energy").Access = 1
		})
		requireRelayContributionParity(t, "reset access", device,
			relayPlugKeysWithout("resettotalenergy"))
	})
	t.Run("duplicate reset roots are omitted", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "reset_total_energy")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireRelayContributionParity(t, "reset duplicate roots", device,
			relayPlugKeysWithout("resettotalenergy"))
	})
	t.Run("unresolved relay endpoint omits only that root", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			device.Definition.Exposes[0].Endpoint = "missing"
		})
		catalog := mustEmbeddedProfileCatalog(t)
		relay := evalTestProfile(t, catalog, "relay")
		input := profilePlanningInput(device, device.IEEEAddress)
		want := relayPlanner{}.Plan(input)
		got := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
		if difference := parityContributionDifference(want, got); difference != "" {
			t.Fatalf("unresolved endpoint %s", difference)
		}
		if len(got.Entities) != 0 {
			t.Fatalf("unresolved relay planned %v, want no relay family", entityKeys(got.Entities))
		}
	})
}

// This test protects relay family gating parity and fails if device-root
// attributes survive without eligible relay power on one path, if valid
// siblings are suppressed with a broken root, or if duplicate power keys
// keep an ambiguous route. The defect would be a relay profile with
// different gate or deduplication behavior than production.
func TestRelayProfilesPreserveFamilyGating(t *testing.T) {
	t.Parallel()
	t.Run("invalid power gates every attribute", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			device.Definition.Exposes[0].Features[0].Access = 3
		})
		requireRelayContributionParity(t, "invalid power", device, nil)
	})
	t.Run("duplicate power keys gate attributes", func(t *testing.T) {
		t.Parallel()
		device := relayParityMutation(t, func(device *upstreamDevice) {
			duplicate := upstreamExpose{
				Type: "switch",
				Features: []upstreamExpose{{
					Type: "binary", Name: "state", Property: "state", Access: 7,
					ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
				}},
			}
			device.Definition.Exposes = append([]upstreamExpose{duplicate}, device.Definition.Exposes...)
		})
		requireRelayContributionParity(t, "duplicate power", device, nil)
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
		catalog := mustEmbeddedProfileCatalog(t)
		relay := evalTestProfile(t, catalog, "relay")
		input := profilePlanningInput(device, device.IEEEAddress)
		want := relayPlanner{}.Plan(input)
		got := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
		if difference := parityContributionDifference(want, got); difference != "" {
			t.Fatalf("sibling survivor %s", difference)
		}
		keys := entityKeys(got.Entities)
		if len(keys) == 0 || keys[0] != "power-ep2" {
			t.Fatalf("relay survivor keys = %v, want power-ep2 first", keys)
		}
	})
}

// This test protects discovered-scalar parity and fails if the relay
// profile hard-codes ON/OFF instead of translating its expose values: a
// synthetic switch with custom scalars must decode and publish those
// scalars identically on both paths.
func TestRelayProfileUsesDiscoveredScalars(t *testing.T) {
	t.Parallel()
	device := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{{
			Type: "binary", Name: "state", Property: "state",
			Access:  exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
			ValueOn: json.RawMessage(`"POWER_ON"`), ValueOff: json.RawMessage(`"POWER_OFF"`),
		}},
	})
	catalog := mustEmbeddedProfileCatalog(t)
	relay := evalTestProfile(t, catalog, "relay")
	input := profilePlanningInput(device, device.IEEEAddress)
	wantContribution := relayPlanner{}.Plan(input)
	gotContribution := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(wantContribution, gotContribution); difference != "" {
		t.Fatalf("custom scalars %s", difference)
	}
	if len(gotContribution.Entities) != 1 {
		t.Fatalf("custom scalars planned %d entities, want 1", len(gotContribution.Entities))
	}
	want, got := wantContribution.Entities[0], gotContribution.Entities[0]
	requireRelayDecodeParity(t, "power", "state", `"POWER_ON"`, want, got)
	requireRelayCommandParity(t, "power", "set", `{"value":true}`, want, got, true, false)
	wantPayload, _ := relayParityTranslate(t, want, "set", `{"value":true}`)
	if string(wantPayload) != `{"state":"POWER_ON"}` {
		t.Fatalf("custom ON payload = %s, want the discovered scalar", wantPayload)
	}
}

// This test protects valid upstream bound preference and fails if the
// profile ignores both present valid finite upstream bounds: voltage with
// 100-250 bounds must carry that support identically on both paths.
func TestRelayProfilePrefersValidUpstreamBounds(t *testing.T) {
	t.Parallel()
	device := relayParityMutation(t, func(device *upstreamDevice) {
		minimum, maximum := 100.0, 250.0
		voltage := plugExposeByName(device, "voltage")
		voltage.ValueMin = &minimum
		voltage.ValueMax = &maximum
	})
	catalog := mustEmbeddedProfileCatalog(t)
	relay := evalTestProfile(t, catalog, "relay")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := relayPlanner{}.Plan(input)
	got := evaluatePlannerProfile(relay, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("upstream bounds %s", difference)
	}
	const expectedVoltageSupport = `{"state":{"maximum":250,"minimum":100,"unit":"V"},"operations":{}}`
	for _, contribution := range []plannerContribution{want, got} {
		for _, plan := range contribution.Entities {
			if plan.Descriptor.Key == "voltage" &&
				string(plan.Descriptor.Support) != expectedVoltageSupport {
				t.Fatalf("voltage support = %s, want the upstream bounds", plan.Descriptor.Support)
			}
		}
	}
}

// This test protects the differential harness sensitivity and fails if a
// mutated relay profile escapes detection: restricting electrical power
// to parts-per-million and disabling power must both diverge from the
// handwritten oracle on the plug fixture. A passing mutation would prove
// the parity comparisons are blind.
func TestRelayParityHarnessDetectsProfileDrift(t *testing.T) {
	t.Parallel()
	device := mustPlugDevice(t)
	input := profilePlanningInput(device, device.IEEEAddress)
	want := relayPlanner{}.Plan(input)
	t.Run("unit restriction diverges", func(t *testing.T) {
		t.Parallel()
		rule := evalTestRuleJSON("relay.electrical-power", `{"kind":"root"}`,
			"numeric-sensor",
			`{"accepted_units":["ppm"],"unit":"W","number_format":"float",`+
				`"bounds":{"mode":"upstream-or-fallback","minimum":0,"maximum":1000000000}}`,
			"electricalpower", "Electrical Power")
		groups := evalTestGroupJSON("relay.roots", "switch", "", "relay.power",
			evalTestRuleJSON("relay.power", `{"kind":"feature","type":"binary","name":"state"}`,
				"binary-power", `{}`, "power", "Power")+","+rule)
		mutated := evalTestCatalog(t, map[string]string{
			"profiles/relay.profile.json": evalTestPlannerJSON(
				"relay", 20, "relay", "primary", groups, ""),
		})
		got := evaluatePlannerProfile(
			mutated.profiles[0], input, mutated.overrides, mutated.strategies)
		if difference := parityContributionDifference(want, got); difference == "" {
			t.Fatal("mutated electrical-power units escaped the parity comparison, want detected drift")
		}
	})
	t.Run("disabled gate diverges", func(t *testing.T) {
		t.Parallel()
		catalog := mustEmbeddedProfileCatalog(t)
		relay := evalTestProfile(t, catalog, "relay")
		docs := map[string]string{
			"profiles/relay.profile.json": mustRelayProfileJSON(t),
			"profiles/overrides/disable-power.override.json": evalTestOverrideJSON(
				"disable-power", "Third Reality", "3RSP02028BZ", nil,
				`{"rule":"relay.power","enabled":false}`),
		}
		disabled := evalTestCatalog(t, docs)
		var disabledRelay compiledPlannerProfile
		for _, profile := range disabled.profiles {
			if profile.document.ID == "relay" {
				disabledRelay = profile
			}
		}
		got := evaluatePlannerProfile(disabledRelay, input, disabled.overrides, disabled.strategies)
		if difference := parityContributionDifference(want, got); difference == "" {
			t.Fatal("disabled relay power escaped the parity comparison, want detected drift")
		}
		_ = relay
	})
}

// mustRelayProfileJSON returns the embedded relay profile document so the
// override sensitivity case layers a general vendor/model patch over the
// exact production rule set instead of a hand-built subset.
func mustRelayProfileJSON(t *testing.T) string {
	t.Helper()
	raw, err := readEmbeddedProfileDocuments(embeddedProfileFiles)
	if err != nil {
		t.Fatalf("list embedded profile documents: %v", err)
	}
	for _, file := range raw {
		if file.path == "profiles/relay.profile.json" {
			return string(file.data)
		}
	}
	t.Fatal("embedded relay profile not found")
	return ""
}
