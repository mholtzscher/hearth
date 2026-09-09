package zigbee2mqtt //nolint:testpackage // Parity tests exercise package-private planners, discovery, and wire DTOs.

// This test protects D6 light and color differential parity and fails if
// the embedded light profile diverges from the handwritten lightPlanner
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

	contractcolorhsv1 "github.com/mholtzscher/hearth/entitytypes/colorhsv1"
	contractcolortempv1 "github.com/mholtzscher/hearth/entitytypes/colortempv1"
	contractcolorxyv1 "github.com/mholtzscher/hearth/entitytypes/colorxyv1"
	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
)

// parityLightProfilePlanners returns the hybrid planner assembly used for
// the profile path: handwritten relay, sensor, and link-quality families
// with the profile-backed light contribution. It isolates light drift from
// the D4 sensor and D5 relay migrations.
func parityLightProfilePlanners(t *testing.T) []devicePlanner {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	return []devicePlanner{
		profileParityPlanner{
			profile: light, overrides: catalog.overrides, strategies: catalog.strategies,
		},
		relayPlanner{},
		sensorPlanner{},
		linkqualityPlanner{},
	}
}

// requireLightMergedParity asserts that the profile-backed light
// contribution merges exactly like the handwritten merge for one fixture
// device: device kind, ordered entities, or the stable plan rejection
// code. The defect would be a light profile whose contribution merges
// differently than production.
func requireLightMergedParity(
	t *testing.T,
	fixture string,
	device upstreamDevice,
	lightPlanners []devicePlanner,
) {
	t.Helper()
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, wantErr := planDevice(input, defaultDevicePlanners())
	gotPlan, gotErr := planDevice(input, lightPlanners)
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

// lightParityFixtures lists every bridge-devices fixture plus the two
// non-bridge light shape fixtures, so color representation coverage is not
// limited to the bridge prefix.
func lightParityFixtures(t *testing.T) []string {
	t.Helper()
	fixtures := parityBridgeDevicesFixtures(t)
	return append(fixtures, "color-temp-only-light.json", "multi-endpoint-light.json")
}

// lightParityDevices decodes every device in one fixture, mirroring
// discoverInventory isolation for malformed array elements.
func lightParityDevices(t *testing.T, fixture string) []upstreamDevice {
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

// lightParityPlansByKey indexes plans by their already-validated unique key.
func lightParityPlansByKey(plans []entityPlan) map[string]entityPlan {
	indexed := make(map[string]entityPlan, len(plans))
	for _, plan := range plans {
		indexed[plan.Descriptor.Key] = plan
	}
	return indexed
}

// This test protects light contribution parity and fails if the embedded
// light profile diverges from the handwritten lightPlanner over any
// fixture device: kind, role, ordered entities, or any plan field. The
// defect would be a light profile that registers different bulb entities
// than production.
func TestLightProfileContributionsMatchHandwritten(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	if light.document.Order != 10 || light.document.Contribution.DeviceKind != upstreamDeviceKindLight {
		t.Fatalf("light profile order/kind = %d/%q, want 10/light",
			light.document.Order, light.document.Contribution.DeviceKind)
	}
	if light.document.CandidateGroups[0].GateRule != "light.power" {
		t.Fatalf("light gate = %q, want light.power", light.document.CandidateGroups[0].GateRule)
	}
	for _, fixture := range lightParityFixtures(t) {
		for _, device := range lightParityDevices(t, fixture) {
			input := profilePlanningInput(device, device.IEEEAddress)
			want := lightPlanner{}.Plan(input)
			got := evaluatePlannerProfile(light, input, catalog.overrides, catalog.strategies)
			if difference := parityContributionDifference(want, got); difference != "" {
				t.Fatalf("%s device %s light %s: want %v got %v",
					fixture, device.IEEEAddress, difference,
					entityKeys(want.Entities), entityKeys(got.Entities))
			}
		}
	}
}

// This test protects merged device parity and fails if the profile-backed
// light contribution changes any merged plan or rejection code: device
// kind, ordered entities, or the stable plan error must match the
// handwritten merge over every fixture device. The defect would be a light
// profile whose contribution merges differently than production.
func TestLightMergedPlansMatchHandwritten(t *testing.T) {
	t.Parallel()
	lightPlanners := parityLightProfilePlanners(t)
	for _, fixture := range lightParityFixtures(t) {
		for _, device := range lightParityDevices(t, fixture) {
			requireLightMergedParity(t, fixture, device, lightPlanners)
		}
	}
}

// mustWandaDevice decodes the synthetic Wanda-shaped bulb inventory used as
// the captured light contract oracle.
func mustWandaDevice(t *testing.T) upstreamDevice {
	t.Helper()
	items, err := decodeRawArray(readFixture(t, "bridge-devices-wanda-synthetic.json"))
	if err != nil {
		t.Fatalf("wanda fixture is not a JSON array: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("wanda fixture must contain exactly one device, got %d", len(items))
	}
	device, err := decodeUpstreamDevice(items[0])
	if err != nil {
		t.Fatalf("wanda fixture device failed to decode: %v", err)
	}
	return device
}

// requireLightCapturedRouteShapes checks the Wanda fixture behavioral route
// classes independently of full handwritten/profile plan equality.
func requireLightCapturedRouteShapes(t *testing.T, plans map[string]entityPlan) {
	t.Helper()
	// Power, brightness, temperature, startup, and power-on behavior stay
	// controllable with refresh; color mode stays read-only without a
	// translator; effect stays a stateless dispatched action.
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

// This test protects the captured Wanda bulb contract under profile
// planning and fails on any descriptor, type, support, route, or order
// drift across all bulb entities: the merged profile plan must equal the
// handwritten Wanda expectations byte-for-byte.
func TestLightBulbKeepsCapturedDescriptorsRoutesAndOrder(t *testing.T) {
	t.Parallel()
	lightPlanners := parityLightProfilePlanners(t)
	device := mustWandaDevice(t)
	input := profilePlanningInput(device, device.IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the wanda fixture: %v", err)
	}
	gotPlan, err := planDevice(input, lightPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the wanda fixture: %v", err)
	}
	if wantPlan.Kind != upstreamDeviceKindLight || gotPlan.Kind != upstreamDeviceKindLight {
		t.Fatalf("wanda device kinds = %q/%q, want light/light", wantPlan.Kind, gotPlan.Kind)
	}
	wantKeys := []string{
		"power", "brightness", "colortemp", "colormode",
		"startupcolortemp", "poweronbehavior", "effect", "linkquality",
	}
	if got := entityKeys(gotPlan.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("profile wanda keys = %v, want %v", got, wantKeys)
	}
	if got := entityKeys(wantPlan.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("handwritten wanda keys = %v, want %v", got, wantKeys)
	}
	want := lightParityPlansByKey(wantPlan.Entities)
	got := lightParityPlansByKey(gotPlan.Entities)
	for _, key := range wantKeys {
		if difference := parityPlanDifference(want[key], got[key]); difference != "" {
			t.Fatalf("wanda entity %s %s", key, difference)
		}
	}
	requireLightCapturedRouteShapes(t, got)
}

// This test protects exact color representation discovery and fails if any
// representation combination discovers different entities on either path:
// XY-only, HS-only, dual, and multi-endpoint devices must match the
// handwritten key order exactly.
func TestLightColorRepresentationsKeepKeysAndOrder(t *testing.T) {
	t.Parallel()
	lightPlanners := parityLightProfilePlanners(t)
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
			devices := lightParityDevices(t, test.fixture)
			if len(devices) != 1 {
				t.Fatalf("fixture %s must contain exactly one device", test.fixture)
			}
			input := profilePlanningInput(devices[0], devices[0].IEEEAddress)
			wantPlan, err := planDevice(input, defaultDevicePlanners())
			if err != nil {
				t.Fatalf("handwritten merge rejected %s: %v", test.fixture, err)
			}
			gotPlan, err := planDevice(input, lightPlanners)
			if err != nil {
				t.Fatalf("profile merge rejected %s: %v", test.fixture, err)
			}
			if got := entityKeys(gotPlan.Entities); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("profile %s keys = %v, want %v", test.fixture, got, test.want)
			}
			if got := entityKeys(wantPlan.Entities); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("handwritten %s keys = %v, want %v", test.fixture, got, test.want)
			}
		})
	}
}

// lightDecodeObservations decodes one full payload through both entity sets
// and returns the observation values by entity ID for comparison.
func lightDecodeObservations(
	t *testing.T,
	payload string,
	want, got []entityPlan,
) (map[string]string, map[string]string) {
	t.Helper()
	receivedAt := time.Unix(1, 0).UTC()
	wantStates, wantIssues, wantErr := decodeDeviceState([]byte(payload), bindPlans(want), receivedAt)
	gotStates, gotIssues, gotErr := decodeDeviceState([]byte(payload), bindPlans(got), receivedAt)
	if (wantErr == nil) != (gotErr == nil) || len(wantIssues) != len(gotIssues) {
		t.Fatalf("payload %s: want (err=%v issues=%d) got (err=%v issues=%d)",
			payload, wantErr, len(wantIssues), gotErr, len(gotIssues))
	}
	if wantErr != nil {
		return nil, nil
	}
	wantValues := make(map[string]string, len(wantStates))
	for _, state := range wantStates {
		wantValues[state.entityID] = string(state.report.Observation.Value)
	}
	gotValues := make(map[string]string, len(gotStates))
	for _, state := range gotStates {
		gotValues[state.entityID] = string(state.report.Observation.Value)
	}
	return wantValues, gotValues
}

// This test protects exact light state conversions and fails if profile
// planning changes any decoded value or observation: discovered power
// scalars, percent brightness scaling with rounding, mired temperature,
// XY/HS coordinates with same-message activity, read-only mode, the
// startup sentinel, or power-on behavior choices. The defect would be a
// light profile number format, bound, or property that silently rescales,
// truncates, or rewords reports.
func TestLightPreservesExactStateConversions(t *testing.T) {
	t.Parallel()
	lightPlanners := parityLightProfilePlanners(t)
	devices := lightParityDevices(t, "bridge-devices-color-dual.json")
	input := profilePlanningInput(devices[0], devices[0].IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the dual fixture: %v", err)
	}
	gotPlan, err := planDevice(input, lightPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the dual fixture: %v", err)
	}
	want := lightParityPlansByKey(wantPlan.Entities)
	got := lightParityPlansByKey(gotPlan.Entities)
	// Single-property entities compare through the shared decode helper:
	// presence, error shape, semantic value, and observation bytes.
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
	}{
		{"power", "state", `"ON"`},
		{"power", "state", `"OFF"`},
		{"brightness", "brightness", `254`},
		{"brightness", "brightness", `0`},
	} {
		requireRelayDecodeParity(
			t, testCase.key, testCase.property, testCase.payload,
			want[testCase.key], got[testCase.key],
		)
	}
	// Brightness rounding follows the handwritten percent scaling: half
	// steps round up through the shared offset.
	report := requireRelayDecodeParity(
		t, "brightness", "brightness", `127`, want["brightness"], got["brightness"],
	)
	if report.semantic != int64(50) {
		t.Fatalf("brightness 127 decoded to %v, want percent 50", report.semantic)
	}
	// Same-message color activity compares through full payloads because
	// XY, HS, temperature, and mode assemble from two properties at once.
	for _, payload := range []string{
		`{"state":"ON","brightness":254,"color":{"x":0.3125,"y":0.3291},"color_mode":"xy"}`,
		`{"state":"ON","brightness":254,"color":{"hue":120,"saturation":80},"color_mode":"hs"}`,
		`{"state":"ON","color_temp":370,"color_mode":"color_temp"}`,
		`{"state":"ON","color_temp":370,"color_mode":"xy"}`,
	} {
		wantValues, gotValues := lightDecodeObservations(t, payload, wantPlan.Entities, gotPlan.Entities)
		if !reflect.DeepEqual(gotValues, wantValues) {
			t.Fatalf("payload %s observations diverged:\nwant %v\ngot  %v", payload, wantValues, gotValues)
		}
	}
	// Startup and power-on behavior compare on the bulb device that carries
	// the sentinel preset and the full choice set.
	bulb := bulbTestDevice()
	bulbInput := profilePlanningInput(bulb, bulb.IEEEAddress)
	wantBulb, err := planDevice(bulbInput, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the bulb device: %v", err)
	}
	gotBulb, err := planDevice(bulbInput, lightPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the bulb device: %v", err)
	}
	wantByKey := lightParityPlansByKey(wantBulb.Entities)
	gotByKey := lightParityPlansByKey(gotBulb.Entities)
	for _, testCase := range []struct {
		key      string
		property string
		payload  string
	}{
		{"startupcolortemp", "color_temp_startup", `250`},
		{"startupcolortemp", "color_temp_startup", `65535`},
		{"poweronbehavior", "power_on_behavior", `"previous"`},
	} {
		requireRelayDecodeParity(
			t, testCase.key, testCase.property, testCase.payload,
			wantByKey[testCase.key], gotByKey[testCase.key],
		)
	}
	sentinel := requireRelayDecodeParity(
		t, "startupcolortemp", "color_temp_startup", `65535`,
		wantByKey["startupcolortemp"], gotByKey["startupcolortemp"],
	)
	choice := "previous"
	if state, ok := sentinel.semantic.(contractnumericsettingv1.State); !ok ||
		state.Mode != "choice" || state.Choice == nil || *state.Choice != choice {
		t.Fatalf("startup 65535 semantic = %#v, want choice previous", sentinel.semantic)
	}
	// Off-choices behavior and fractional startup are rejected on both
	// paths without suppressing the valid comparisons above.
	for name, plans := range map[string]map[string]entityPlan{"handwritten": wantByKey, "profile": gotByKey} {
		_, _, decodeErr := plans["poweronbehavior"].DecodeState("entity-test",
			map[string]json.RawMessage{"power_on_behavior": json.RawMessage(`"turbo"`)}, time.Now().UTC())
		if decodeErr == nil {
			t.Fatalf("%s power-on behavior accepted off-choices turbo", name)
		}
		_, _, decodeErr = plans["startupcolortemp"].DecodeState("entity-test",
			map[string]json.RawMessage{"color_temp_startup": json.RawMessage(`250.5`)}, time.Now().UTC())
		if decodeErr == nil {
			t.Fatalf("%s startup accepted fractional 250.5", name)
		}
	}
}

// This test protects light command-plan parity and fails if any light
// command publishes different MQTT, refreshes different properties, uses
// a different outcome policy, or satisfies different reports: power in
// both directions with the discovered scalars, brightness percent, color
// temperature, XY, HS, startup value and previous, power-on behavior, and
// the dispatched effect trigger.
func TestLightPreservesCommandPlans(t *testing.T) {
	t.Parallel()
	lightPlanners := parityLightProfilePlanners(t)
	devices := lightParityDevices(t, "bridge-devices-color-dual.json")
	input := profilePlanningInput(devices[0], devices[0].IEEEAddress)
	wantPlan, err := planDevice(input, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the dual fixture: %v", err)
	}
	gotPlan, err := planDevice(input, lightPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the dual fixture: %v", err)
	}
	want, got := lightParityPlansByKey(wantPlan.Entities), lightParityPlansByKey(gotPlan.Entities)
	requireRelayCommandParity(t, "power", "set", `{"value":true}`, want["power"], got["power"], true, false)
	requireRelayCommandParity(t, "power", "set", `{"value":false}`, want["power"], got["power"], false, true)
	requireRelayCommandParity(t, "brightness", "set", `{"value":50}`,
		want["brightness"], got["brightness"], int64(50), int64(51))
	requireRelayCommandParity(t, "colortemp", "set", `{"value":370}`,
		want["colortemp"], got["colortemp"],
		contractcolortempv1.State{Active: true, Value: 370},
		contractcolortempv1.State{Active: true, Value: 371})
	requireRelayCommandParity(t, "colorxy", "set", `{"x":3125,"y":3291}`,
		want["colorxy"], got["colorxy"],
		contractcolorxyv1.State{Active: true, X: 3125, Y: 3291},
		contractcolorxyv1.State{Active: true, X: 4000, Y: 1000})
	requireRelayCommandParity(t, "colorhs", "set", `{"hue":120,"saturation":80}`,
		want["colorhs"], got["colorhs"],
		contractcolorhsv1.State{Active: true, Hue: 120, Saturation: 80},
		contractcolorhsv1.State{Active: true, Hue: 200, Saturation: 20})
	// Startup, behavior, and effect live on the bulb device that carries
	// the sentinel preset and the full choice sets.
	bulb := bulbTestDevice()
	bulbInput := profilePlanningInput(bulb, bulb.IEEEAddress)
	wantBulb, err := planDevice(bulbInput, defaultDevicePlanners())
	if err != nil {
		t.Fatalf("handwritten merge rejected the bulb device: %v", err)
	}
	gotBulb, err := planDevice(bulbInput, lightPlanners)
	if err != nil {
		t.Fatalf("profile merge rejected the bulb device: %v", err)
	}
	wantByKey, gotByKey := lightParityPlansByKey(wantBulb.Entities), lightParityPlansByKey(gotBulb.Entities)
	value := 250.0
	other := 251.0
	previous := "previous"
	requireRelayCommandParity(t, "startupcolortemp", "set", `{"mode":"value","value":250}`,
		wantByKey["startupcolortemp"], gotByKey["startupcolortemp"],
		contractnumericsettingv1.State{Mode: "value", Value: &value},
		contractnumericsettingv1.State{Mode: "value", Value: &other})
	requireRelayCommandParity(t, "startupcolortemp", "set", `{"mode":"choice","choice":"previous"}`,
		wantByKey["startupcolortemp"], gotByKey["startupcolortemp"],
		contractnumericsettingv1.State{Mode: "choice", Choice: &previous},
		contractnumericsettingv1.State{Mode: "value", Value: &value})
	previousValue := contractEnumSettingState("previous")
	offChoice := contractEnumSettingState("off")
	requireRelayCommandParity(t, "poweronbehavior", "set", `{"value":"previous"}`,
		wantByKey["poweronbehavior"], gotByKey["poweronbehavior"], previousValue, offChoice)
	requireRelayCommandParity(t, "effect", "trigger", `{"name":"breathe"}`,
		wantByKey["effect"], gotByKey["effect"], nil, nil)
	// Exact wire payloads prove the discovered properties flow through:
	// brightness scales to the discovered maximum and effect uses its
	// device-unique property.
	wantPayload, _ := relayParityTranslate(t, want["brightness"], "set", `{"value":50}`)
	if string(wantPayload) != `{"brightness":127}` && string(wantPayload) != `{"brightness":128}` {
		t.Fatalf("brightness 50 payload = %s, want the scaled value", wantPayload)
	}
	effectPayload, _ := relayParityTranslate(t, wantByKey["effect"], "trigger", `{"name":"breathe"}`)
	if string(effectPayload) != `{"effect":"breathe"}` {
		t.Fatalf("effect payload = %s, want the named trigger", effectPayload)
	}
	// Out-of-range values, off-choices options, fractional startup, and the
	// wrong effect operation are rejected before MQTT on both paths.
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
		plans := map[string]map[string]entityPlan{"handwritten": want, "profile": got}
		if testCase.bulb {
			plans = map[string]map[string]entityPlan{"handwritten": wantByKey, "profile": gotByKey}
		}
		for name, byKey := range plans {
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
				t.Fatalf("%s path accepted %s, want rejection", name, testCase.name)
			}
		}
	}
}

// lightTestFeature returns the named feature of the first light root, or
// nil when the device shape changed under the test.
func lightTestFeature(device *upstreamDevice, name string) *upstreamExpose {
	for rootIndex := range device.Definition.Exposes {
		root := &device.Definition.Exposes[rootIndex]
		if root.Type != upstreamDeviceKindLight {
			continue
		}
		for featureIndex := range root.Features {
			if root.Features[featureIndex].Name == name {
				return &root.Features[featureIndex]
			}
		}
	}
	return nil
}

// lightParityMutation applies one device mutation to a fresh dual-color
// fixture so each differential case isolates exactly one eligibility
// dimension.
func lightParityMutation(t *testing.T, mutate func(*upstreamDevice)) upstreamDevice {
	t.Helper()
	devices := lightParityDevices(t, "bridge-devices-color-dual.json")
	if len(devices) != 1 {
		t.Fatal("dual fixture must contain exactly one device")
	}
	device := devices[0]
	mutate(&device)
	return device
}

// requireLightContributionParity asserts that one mutated device produces
// identical light contributions on both paths with exactly the expected
// entity keys. The defect would be a light profile that admits or drops a
// different set than production for that eligibility rule.
func requireLightContributionParity(t *testing.T, name string, device upstreamDevice, wantKeys []string) {
	t.Helper()
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := lightPlanner{}.Plan(input)
	got := evaluatePlannerProfile(light, input, catalog.overrides, catalog.strategies)
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

// dualLightKeysWithout returns the dual-fixture light contribution key list
// minus omitted light keys. The dual device carries no device entities, so
// only candidate keys appear here.
func dualLightKeysWithout(omitted ...string) []string {
	full := []string{"power", "brightness", "colortemp", "colorxy", "colorhs", "colormode"}
	var keys []string
	for _, key := range full {
		if !slices.Contains(omitted, key) {
			keys = append(keys, key)
		}
	}
	return keys
}

// lightContributionParityCase describes one single-dimension eligibility
// mutation: how to build the device and the exact contribution keys
// expected identically on both the handwritten and profile paths.
type lightContributionParityCase struct {
	name string
	// parityName preserves the original requireLightContributionParity
	// label so failure messages stay byte-identical after the table
	// refactor.
	parityName string
	build      func(t *testing.T) upstreamDevice
	wantKeys   []string
}

// runLightContributionParityCases executes one eligibility case per
// subtest through the shared contribution comparison. Tables keep
// each top-level test linear while the per-case builders isolate exactly
// one eligibility dimension.
func runLightContributionParityCases(t *testing.T, cases []lightContributionParityCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			device := testCase.build(t)
			requireLightContributionParity(t, testCase.parityName, device, testCase.wantKeys)
		})
	}
}

// mutateLightBrightnessWithoutSetAccess drops brightness set access so the
// brightness candidate is ineligible while its siblings survive.
func mutateLightBrightnessWithoutSetAccess(device *upstreamDevice) {
	lightTestFeature(device, "brightness").Access = 1
}

// buildLightBrightnessWithoutSetAccess returns the dual fixture with
// brightness set access removed.
func buildLightBrightnessWithoutSetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightBrightnessWithoutSetAccess)
}

// mutateLightInvertedTemperatureBounds inverts both wire layers of the
// temperature bounds. colorTempRange prefers the raw wire bounds when
// present: decoded fixtures carry both, so both layers invert together.
func mutateLightInvertedTemperatureBounds(device *upstreamDevice) {
	feature := lightTestFeature(device, "color_temp")
	feature.valueMinRaw = json.RawMessage(`500`)
	feature.valueMaxRaw = json.RawMessage(`150`)
	minimum, maximum := 500.0, 150.0
	feature.ValueMin = &minimum
	feature.ValueMax = &maximum
}

// buildLightInvertedTemperatureBounds returns the dual fixture with
// inverted temperature bounds.
func buildLightInvertedTemperatureBounds(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightInvertedTemperatureBounds)
}

// mutateLightDuplicateBrightnessFeatures adds a shadow-property brightness
// duplicate so the brightness key is ambiguous while siblings survive.
func mutateLightDuplicateBrightnessFeatures(device *upstreamDevice) {
	duplicate := *lightTestFeature(device, "brightness")
	duplicate.Property = "brightness_shadow"
	for rootIndex := range device.Definition.Exposes {
		if device.Definition.Exposes[rootIndex].Type == upstreamDeviceKindLight {
			device.Definition.Exposes[rootIndex].Features = append(
				device.Definition.Exposes[rootIndex].Features, duplicate)
		}
	}
}

// buildLightDuplicateBrightnessFeatures returns the dual fixture with a
// duplicated brightness feature.
func buildLightDuplicateBrightnessFeatures(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightDuplicateBrightnessFeatures)
}

// mutateLightXYAxisWithoutSetAccess drops set access on the x axis so the
// XY composite is ineligible while mode survives on HS.
func mutateLightXYAxisWithoutSetAccess(device *upstreamDevice) {
	feature := lightTestFeature(device, "color_xy")
	for axisIndex := range feature.Features {
		if feature.Features[axisIndex].Name == "x" {
			feature.Features[axisIndex].Access = 1
		}
	}
}

// buildLightXYAxisWithoutSetAccess returns the dual fixture with the x
// axis unreadable for set.
func buildLightXYAxisWithoutSetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightXYAxisWithoutSetAccess)
}

// mutateLightHSAxisWithWrongProperty retargets the hue axis property so
// the HS composite is ineligible while mode survives on XY.
func mutateLightHSAxisWithWrongProperty(device *upstreamDevice) {
	feature := lightTestFeature(device, "color_hs")
	for axisIndex := range feature.Features {
		if feature.Features[axisIndex].Name == "hue" {
			feature.Features[axisIndex].Property = "hue_shadow"
		}
	}
}

// buildLightHSAxisWithWrongProperty returns the dual fixture with the hue
// axis property retargeted.
func buildLightHSAxisWithWrongProperty(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightHSAxisWithWrongProperty)
}

// mutateLightDuplicateColorProperty duplicates the XY composite so both
// color composites share one property while mode survives.
func mutateLightDuplicateColorProperty(device *upstreamDevice) {
	for rootIndex := range device.Definition.Exposes {
		if device.Definition.Exposes[rootIndex].Type == upstreamDeviceKindLight {
			duplicate := *lightTestFeature(device, "color_xy")
			device.Definition.Exposes[rootIndex].Features = append(
				device.Definition.Exposes[rootIndex].Features, duplicate)
		}
	}
}

// buildLightDuplicateColorProperty returns the dual fixture with a
// duplicated XY composite.
func buildLightDuplicateColorProperty(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightDuplicateColorProperty)
}

// mutateLightForeignColorModeClaim adds a non-light numeric expose that
// claims the color_mode property.
func mutateLightForeignColorModeClaim(device *upstreamDevice) {
	device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
		Type: "numeric", Name: "diagnostic", Property: "color_mode", Access: 1,
	})
}

// buildLightForeignColorModeClaim returns the dual fixture with a foreign
// color_mode claim.
func buildLightForeignColorModeClaim(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateLightForeignColorModeClaim)
}

// mutateLightUnresolvedEndpoint points the first root at a missing
// endpoint so no light family survives.
func mutateLightUnresolvedEndpoint(device *upstreamDevice) {
	device.Definition.Exposes[0].Endpoint = "missing"
}

// requireLightUnresolvedEndpointParity asserts that an unresolved light
// endpoint plans no light family identically on both paths.
func requireLightUnresolvedEndpointParity(t *testing.T) {
	t.Helper()
	device := lightParityMutation(t, mutateLightUnresolvedEndpoint)
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := lightPlanner{}.Plan(input)
	got := evaluatePlannerProfile(light, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("unresolved endpoint %s", difference)
	}
	if len(got.Entities) != 0 {
		t.Fatalf("unresolved light planned %v, want no light family", entityKeys(got.Entities))
	}
}

// This test protects color representation, dependency, and duplicate-gate
// eligibility parity and fails if any access, bound, axis, property,
// foreign-claim, duplicate-root, or unresolved-endpoint violation registers
// on one path but not the other, or if a valid sibling is suppressed with
// it. Each case mutates exactly one dimension of the captured dual light.
func TestLightProfilesIsolateMalformedSiblings(t *testing.T) {
	t.Parallel()
	runLightContributionParityCases(t, []lightContributionParityCase{
		{
			name:       "brightness without set access is omitted",
			parityName: "brightness access",
			build:      buildLightBrightnessWithoutSetAccess,
			wantKeys:   dualLightKeysWithout("brightness"),
		},
		{
			// Mode survives on the remaining XY and HS siblings.
			name:       "inverted temperature bounds omit temperature but keep mode",
			parityName: "temperature inverted bounds",
			build:      buildLightInvertedTemperatureBounds,
			wantKeys:   dualLightKeysWithout("colortemp"),
		},
		{
			name:       "duplicate brightness features omit only brightness",
			parityName: "brightness duplicate features",
			build:      buildLightDuplicateBrightnessFeatures,
			wantKeys:   dualLightKeysWithout("brightness"),
		},
		{
			name:       "xy axis without set access omits xy but keeps mode",
			parityName: "xy axis access",
			build:      buildLightXYAxisWithoutSetAccess,
			wantKeys:   dualLightKeysWithout("colorxy"),
		},
		{
			name:       "hs axis with wrong property omits hs but keeps mode",
			parityName: "hs axis property",
			build:      buildLightHSAxisWithWrongProperty,
			wantKeys:   dualLightKeysWithout("colorhs"),
		},
		{
			name:       "duplicate color property omits both composites but keeps mode",
			parityName: "duplicate color property",
			build:      buildLightDuplicateColorProperty,
			wantKeys:   dualLightKeysWithout("colorxy", "colorhs"),
		},
		{
			name:       "foreign color mode claim omits color, mode, and temperature",
			parityName: "foreign mode claim",
			build:      buildLightForeignColorModeClaim,
			wantKeys:   []string{"power", "brightness"},
		},
	})
	t.Run("unresolved light endpoint omits only that root", func(t *testing.T) {
		t.Parallel()
		requireLightUnresolvedEndpointParity(t)
	})
}

// mutateBulbBehaviorWithoutGetAccess drops get access on power-on behavior
// so only that device entity is ineligible.
func mutateBulbBehaviorWithoutGetAccess(device *upstreamDevice) {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == powerOnBehaviorExposeName {
			device.Definition.Exposes[index].Access = 3
		}
	}
}

// buildBulbBehaviorWithoutGetAccess returns the bulb with power-on behavior
// get access removed.
func buildBulbBehaviorWithoutGetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbBehaviorWithoutGetAccess(&device)
	return device
}

// mutateBulbDuplicateBehaviorRoots duplicates the power-on behavior root so
// only that key is ambiguous.
func mutateBulbDuplicateBehaviorRoots(device *upstreamDevice) {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == powerOnBehaviorExposeName {
			device.Definition.Exposes = append(device.Definition.Exposes, device.Definition.Exposes[index])
		}
	}
}

// buildBulbDuplicateBehaviorRoots returns the bulb with a duplicated
// power-on behavior root.
func buildBulbDuplicateBehaviorRoots(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbDuplicateBehaviorRoots(&device)
	return device
}

// mutateBulbEffectWithGetAccess widens effect access so it is no longer
// set-only.
func mutateBulbEffectWithGetAccess(device *upstreamDevice) {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == effectExposeName {
			device.Definition.Exposes[index].Access = 7
		}
	}
}

// buildBulbEffectWithGetAccess returns the bulb with effect get access
// added.
func buildBulbEffectWithGetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbEffectWithGetAccess(&device)
	return device
}

// mutateBulbEffectEmptyValues clears the effect value list so the effect
// candidate is ineligible.
func mutateBulbEffectEmptyValues(device *upstreamDevice) {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == effectExposeName {
			device.Definition.Exposes[index].Values = nil
		}
	}
}

// buildBulbEffectEmptyValues returns the bulb with an empty effect value
// list.
func buildBulbEffectEmptyValues(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbEffectEmptyValues(&device)
	return device
}

// mutateBulbDuplicateEffectRoots duplicates the effect root so only that
// key is ambiguous.
func mutateBulbDuplicateEffectRoots(device *upstreamDevice) {
	for index := range device.Definition.Exposes {
		if device.Definition.Exposes[index].Name == effectExposeName {
			device.Definition.Exposes = append(device.Definition.Exposes, device.Definition.Exposes[index])
		}
	}
}

// buildBulbDuplicateEffectRoots returns the bulb with a duplicated effect
// root.
func buildBulbDuplicateEffectRoots(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbDuplicateEffectRoots(&device)
	return device
}

// mutateBulbStartupMalformedBounds inverts the startup temperature bounds
// so only that candidate is ineligible.
func mutateBulbStartupMalformedBounds(device *upstreamDevice) {
	minimum, maximum := 454.0, 142.0
	device.Definition.Exposes[0].Features[2].ValueMin = &minimum
	device.Definition.Exposes[0].Features[2].ValueMax = &maximum
}

// buildBulbStartupMalformedBounds returns the bulb with inverted startup
// bounds.
func buildBulbStartupMalformedBounds(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbStartupMalformedBounds(&device)
	return device
}

// requireBulbStartupWithoutPresetParity asserts that a startup candidate
// without the previous preset still plans in value mode identically on
// both paths.
func requireBulbStartupWithoutPresetParity(t *testing.T) {
	t.Helper()
	device := bulbTestDevice()
	device.Definition.Exposes[0].Features[2].Presets = nil
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := lightPlanner{}.Plan(input)
	got := evaluatePlannerProfile(light, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("startup presets %s", difference)
	}
	if got := entityKeys(got.Entities); !slices.Contains(got, "startupcolortemp") {
		t.Fatalf("startup without presets planned %v, want startupcolortemp kept", got)
	}
}

// This test protects light device-entity eligibility parity and fails if
// any access, value, property, duplicate-root, or unresolved-endpoint
// violation on power-on behavior or effect registers on one path but not
// the other, or if a valid sibling is suppressed with it.
func TestLightProfilesIsolateDeviceEntitySiblings(t *testing.T) {
	t.Parallel()
	runLightContributionParityCases(t, []lightContributionParityCase{
		{
			name:       "behavior without get access is omitted",
			parityName: "behavior access",
			build:      buildBulbBehaviorWithoutGetAccess,
			wantKeys:   []string{"power", "brightness", "startupcolortemp", "effect"},
		},
		{
			name:       "duplicate behavior roots omit only behavior",
			parityName: "behavior duplicate roots",
			build:      buildBulbDuplicateBehaviorRoots,
			wantKeys:   []string{"power", "brightness", "startupcolortemp", "effect"},
		},
		{
			name:       "effect with get access is not set-only",
			parityName: "effect access",
			build:      buildBulbEffectWithGetAccess,
			wantKeys:   []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:       "effect with empty values is omitted",
			parityName: "effect values",
			build:      buildBulbEffectEmptyValues,
			wantKeys:   []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:       "duplicate effect roots omit only effect",
			parityName: "effect duplicate roots",
			build:      buildBulbDuplicateEffectRoots,
			wantKeys:   []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:       "startup with malformed bounds is omitted",
			parityName: "startup bounds",
			build:      buildBulbStartupMalformedBounds,
			wantKeys:   []string{"power", "brightness", "poweronbehavior", "effect"},
		},
	})
	t.Run("startup without previous preset keeps value mode", func(t *testing.T) {
		t.Parallel()
		requireBulbStartupWithoutPresetParity(t)
	})
}

// mutateDualInvalidPower drops set access on light power so the whole
// family gates off.
func mutateDualInvalidPower(device *upstreamDevice) {
	lightTestFeature(device, "state").Access = 3
}

// buildDualInvalidPowerDevice returns the dual fixture with ineligible
// light power.
func buildDualInvalidPowerDevice(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateDualInvalidPower)
}

// mutateDualDuplicatePowerKeys prepends an ambiguous light power root so
// the family gates off.
func mutateDualDuplicatePowerKeys(device *upstreamDevice) {
	duplicate := upstreamExpose{
		Type: "light",
		Features: []upstreamExpose{{
			Type: "binary", Name: "state", Property: "state", Access: 7,
			ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
		}},
	}
	device.Definition.Exposes = append([]upstreamExpose{duplicate}, device.Definition.Exposes...)
}

// buildDualDuplicatePowerDevice returns the dual fixture with duplicated
// power keys.
func buildDualDuplicatePowerDevice(t *testing.T) upstreamDevice {
	t.Helper()
	return lightParityMutation(t, mutateDualDuplicatePowerKeys)
}

// mutateBulbInvalidLightPower drops set access on the bulb power feature
// so every device attribute gates off with it.
func mutateBulbInvalidLightPower(device *upstreamDevice) {
	device.Definition.Exposes[0].Features[0].Access = 3
}

// buildBulbInvalidLightPowerDevice returns the bulb with ineligible light
// power.
func buildBulbInvalidLightPowerDevice(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutateBulbInvalidLightPower(&device)
	return device
}

// mutateEndpointsLeftInvalidPower drops set access on the left endpoint
// power feature while leaving the right sibling eligible.
func mutateEndpointsLeftInvalidPower(device *upstreamDevice) {
	for rootIndex := range device.Definition.Exposes {
		if device.Definition.Exposes[rootIndex].Endpoint == "left" {
			for featureIndex := range device.Definition.Exposes[rootIndex].Features {
				if device.Definition.Exposes[rootIndex].Features[featureIndex].Name == "state" {
					device.Definition.Exposes[rootIndex].Features[featureIndex].Access = 3
				}
			}
		}
	}
}

// requireEndpointsSiblingSurvivorParity asserts that a broken left light
// root spares the valid right sibling identically on both paths with
// power-ep2 first.
func requireEndpointsSiblingSurvivorParity(t *testing.T) {
	t.Helper()
	devices := lightParityDevices(t, "bridge-devices-color-endpoints.json")
	if len(devices) != 1 {
		t.Fatal("endpoints fixture must contain exactly one device")
	}
	device := devices[0]
	mutateEndpointsLeftInvalidPower(&device)
	catalog := mustEmbeddedProfileCatalog(t)
	light := evalTestProfile(t, catalog, "light")
	input := profilePlanningInput(device, device.IEEEAddress)
	want := lightPlanner{}.Plan(input)
	got := evaluatePlannerProfile(light, input, catalog.overrides, catalog.strategies)
	if difference := parityContributionDifference(want, got); difference != "" {
		t.Fatalf("sibling survivor %s", difference)
	}
	keys := entityKeys(got.Entities)
	if len(keys) == 0 || keys[0] != "power-ep2" {
		t.Fatalf("light survivor keys = %v, want power-ep2 first", keys)
	}
}

// This test protects light family gating parity and fails if device-root
// attributes survive without eligible light power on one path, if valid
// siblings are suppressed with a broken root, or if duplicate power keys
// keep an ambiguous route. The defect would be a light profile with
// different gate or deduplication behavior than production.
func TestLightProfilesPreserveFamilyGating(t *testing.T) {
	t.Parallel()
	runLightContributionParityCases(t, []lightContributionParityCase{
		{
			name:       "invalid power gates every attribute",
			parityName: "invalid power",
			build:      buildDualInvalidPowerDevice,
			wantKeys:   nil,
		},
		{
			name:       "duplicate power keys gate attributes",
			parityName: "duplicate power",
			build:      buildDualDuplicatePowerDevice,
			wantKeys:   nil,
		},
		{
			name:       "device attributes need surviving power",
			parityName: "bulb invalid power",
			build:      buildBulbInvalidLightPowerDevice,
			wantKeys:   nil,
		},
	})
	t.Run("invalid light root spares the valid sibling", func(t *testing.T) {
		t.Parallel()
		requireEndpointsSiblingSurvivorParity(t)
	})
}

// This test protects the derived color-mode dependency parity and fails if
// the mode companion plans without a surviving color sibling on one path
// or is skipped despite one. The defect would be a mode entity that
// guesses or a missing mode entity on a valid color light.
func TestLightProfilesPreserveColorModeDependency(t *testing.T) {
	t.Parallel()
	t.Run("temperature-only plans mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 153, 500)),
		}
		requireLightContributionParity(t, "temperature-only mode", device,
			[]string{"power", "brightness", "colortemp", "colormode"})
	})
	t.Run("broken temperature omits temperature and mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 500, 150)),
		}
		requireLightContributionParity(t, "broken temperature mode", device,
			[]string{"power", "brightness"})
	})
	t.Run("no color plans no mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		requireLightContributionParity(t, "no color mode", device,
			[]string{"power", "brightness"})
	})
	t.Run("xy-only plans mode without temperature", func(t *testing.T) {
		t.Parallel()
		devices := lightParityDevices(t, "bridge-devices-color-xy-only.json")
		requireLightContributionParity(t, "xy-only mode", devices[0],
			[]string{"power", "brightness", "colorxy", "colormode"})
	})
}

// This test protects the differential harness sensitivity and fails if a
// mutated light profile escapes detection: restricting brightness to a
// mismatched feature and disabling power must both diverge from the
// handwritten oracle on the dual fixture. A passing mutation would prove
// the parity comparisons are blind.
func TestLightParityHarnessDetectsProfileDrift(t *testing.T) {
	t.Parallel()
	devices := lightParityDevices(t, "bridge-devices-color-dual.json")
	input := profilePlanningInput(devices[0], devices[0].IEEEAddress)
	want := lightPlanner{}.Plan(input)
	t.Run("feature retarget diverges", func(t *testing.T) {
		t.Parallel()
		rule := evalTestRuleJSON("light.brightness", `{"kind":"feature","type":"numeric","name":"missing"}`,
			"brightness", `{}`, "brightness", "Brightness")
		groups := evalTestGroupJSON("light.roots", "light", "", "light.power",
			evalTestRuleJSON("light.power", `{"kind":"feature","type":"binary","name":"state"}`,
				"binary-power", `{}`, "power", "Power")+","+rule)
		mutated := evalTestCatalog(t, map[string]string{
			"profiles/light.profile.json": evalTestPlannerJSON(
				"light", 10, "light", "primary", groups, ""),
		})
		got := evaluatePlannerProfile(
			mutated.profiles[0], input, mutated.overrides, mutated.strategies)
		if difference := parityContributionDifference(want, got); difference == "" {
			t.Fatal("mutated brightness feature escaped the parity comparison, want detected drift")
		}
	})
	t.Run("disabled gate diverges", func(t *testing.T) {
		t.Parallel()
		catalog := mustEmbeddedProfileCatalog(t)
		light := evalTestProfile(t, catalog, "light")
		_ = light
		raw, err := readEmbeddedProfileDocuments(embeddedProfileFiles)
		if err != nil {
			t.Fatalf("list embedded profile documents: %v", err)
		}
		var lightJSON string
		for _, file := range raw {
			if file.path == "profiles/light.profile.json" {
				lightJSON = string(file.data)
			}
		}
		if lightJSON == "" {
			t.Fatal("embedded light profile not found")
		}
		docs := map[string]string{
			"profiles/light.profile.json": lightJSON,
			"profiles/overrides/disable-power.override.json": evalTestOverrideJSON(
				"disable-power", "Fixture Vendor", "SYNTHETIC-DUAL-COLOR", nil,
				`{"rule":"light.power","enabled":false}`),
		}
		disabled := evalTestCatalog(t, docs)
		var disabledLight compiledPlannerProfile
		for _, profile := range disabled.profiles {
			if profile.document.ID == "light" {
				disabledLight = profile
			}
		}
		got := evaluatePlannerProfile(disabledLight, input, disabled.overrides, disabled.strategies)
		if difference := parityContributionDifference(want, got); difference == "" {
			t.Fatal("disabled light power escaped the parity comparison, want detected drift")
		}
	})
}
