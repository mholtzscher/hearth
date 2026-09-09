package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery and translation routes.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	contractenumsettingv1 "github.com/mholtzscher/hearth/entitytypes/enumsettingv1"
	contractnumericsettingv1 "github.com/mholtzscher/hearth/entitytypes/numericsettingv1"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

func mustStartupState(t *testing.T, mode string, value *float64, choice *string) contractnumericsettingv1.State {
	t.Helper()
	return contractnumericsettingv1.State{Mode: mode, Value: value, Choice: choice}
}

func contractEnumSettingState(value string) contractenumsettingv1.State {
	return contractenumsettingv1.State(value)
}

// Fixture provenance: testdata/bridge-devices-wanda-synthetic.json is an
// explicitly SYNTHETIC Wanda-shaped inventory modeled on the Third Reality
// 3RCB01057Z office-table-lamp expose layout (software 1.00.74). It is NOT a
// live capture: the on-wire startup value and effect dispatch outcome are
// unobserved until separately authorized live validation (G2). Sanitized
// identifiers only; no real hardware is accessed.

func testTriggerCommand(entityID, parameters string) adapter.Command {
	return adapter.Command{
		ID: "cmd-test", CorrelationID: "cor-test", EntityID: entityID, OperationName: "trigger",
		Parameters: json.RawMessage(parameters), Deadline: time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano),
	}
}

func bulbTestDevice() upstreamDevice {
	startupMin, startupMax := 142.0, 454.0
	brightnessMin, brightnessMax := 0.0, 254.0
	return upstreamDevice{
		IEEEAddress: "0x00124b0022a9b101", Type: "Router", Supported: true,
		FriendlyName: "test-bulb", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{
				{
					Type: "light",
					Features: []upstreamExpose{
						{
							Type: "binary", Name: "state", Property: "state", Access: 7,
							ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
						},
						{
							Type: "numeric", Name: "brightness", Property: "brightness", Access: 7,
							ValueMin: &brightnessMin, ValueMax: &brightnessMax,
						},
						{
							Type: "numeric", Name: startupColorTempExposeName, Property: startupColorTempExposeName,
							Access:   7,
							ValueMin: &startupMin, ValueMax: &startupMax,
							Presets: []upstreamPreset{{Name: upstreamPreviousPreset, Value: startupPreviousWireValue}},
						},
					},
				},
				{Type: upstreamExposeNumeric, Name: linkqualityExposeName, Property: linkqualityExposeName, Access: 1},
				{
					Type: upstreamExposeEnum, Name: powerOnBehaviorExposeName, Property: powerOnBehaviorExposeName,
					Access: 7, Values: []string{"off", "on", "toggle", "previous"},
				},
				{
					Type: upstreamExposeEnum, Name: effectExposeName, Property: effectExposeName,
					Access: 2, Values: []string{"blink", "breathe"},
				},
			},
		},
	}
}

// This test protects tolerant values/presets wire parsing and fails if a
// malformed values or presets field suppresses the expose instead of being
// ignored, or if a previous-named preset with a bad value is not flagged.
// This test protects tolerant values wire parsing and fails if a
// well-formed string array is lost or a malformed values field poisons the
// expose instead of being ignored.
func TestBulbValuesWireParsing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		payload string
		want    []string
	}{
		{payload: `{"type":"enum","name":"effect","values":["blink","breathe"]}`, want: []string{"blink", "breathe"}},
		{payload: `{"type":"enum","name":"effect"}`, want: nil},
		{payload: `{"type":"enum","name":"effect","values":null}`, want: nil},
		{payload: `{"type":"enum","name":"effect","values":"blink"}`, want: nil},
		{payload: `{"type":"enum","name":"effect","values":["blink",7]}`, want: nil},
		{payload: `{"type":"enum","name":"effect","values":[]}`, want: []string{}},
	} {
		var expose upstreamExpose
		if err := json.Unmarshal([]byte(test.payload), &expose); err != nil {
			t.Fatal(err)
		}
		if (len(expose.Values) == 0 && len(test.want) != 0) || !reflect.DeepEqual(expose.Values, test.want) {
			t.Fatalf("values(%s) = %#v, want %#v", test.payload, expose.Values, test.want)
		}
	}
}

// This test protects tolerant presets wire parsing and fails if a
// previous-named preset with a bad value is not flagged, or if valid
// entries are lost.
func TestBulbPresetsWireParsing(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		payload     string
		want        []upstreamPreset
		wantInvalid bool
	}{
		{
			name:    "previous sentinel",
			payload: `{"presets":[{"name":"previous","value":65535,"description":"Previous value"}]}`,
			want:    []upstreamPreset{{Name: "previous", Value: 65535}},
		},
		{
			name:    "alias kept for planning to ignore",
			payload: `{"presets":[{"name":"previous","value":65535},{"name":"warm","value":454}]}`,
			want:    []upstreamPreset{{Name: "previous", Value: 65535}, {Name: "warm", Value: 454}},
		},
		{
			name:    "absent",
			payload: `{"type":"numeric","name":"color_temp_startup"}`,
			want:    nil,
		},
		{
			name:    "not an array",
			payload: `{"presets":42}`,
			want:    nil,
		},
		{
			name:    "unparseable entry skipped",
			payload: `{"presets":[42,{"name":"previous","value":65535}]}`,
			want:    []upstreamPreset{{Name: "previous", Value: 65535}},
		},
		{
			name:    "nameless entry skipped",
			payload: `{"presets":[{"value":65535}]}`,
			want:    nil,
		},
		{
			name:    "fractional alias skipped without poison",
			payload: `{"presets":[{"name":"warm","value":300.5},{"name":"previous","value":65535}]}`,
			want:    []upstreamPreset{{Name: "previous", Value: 65535}},
		},
		{
			name:        "previous wrong value",
			payload:     `{"presets":[{"name":"previous","value":100}]}`,
			want:        nil,
			wantInvalid: true,
		},
		{
			name:        "previous fractional value",
			payload:     `{"presets":[{"name":"previous","value":65535.5}]}`,
			want:        nil,
			wantInvalid: true,
		},
		{
			name:        "previous missing value",
			payload:     `{"presets":[{"name":"previous"}]}`,
			want:        nil,
			wantInvalid: true,
		},
		{
			name:        "previous string value",
			payload:     `{"presets":[{"name":"previous","value":"65535"}]}`,
			want:        nil,
			wantInvalid: true,
		},
		{
			name:    "duplicate previous sentinels stored for planning to reject",
			payload: `{"presets":[{"name":"previous","value":65535},{"name":"previous","value":65535}]}`,
			want: []upstreamPreset{
				{Name: "previous", Value: 65535},
				{Name: "previous", Value: 65535},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			var expose upstreamExpose
			if err := json.Unmarshal([]byte(test.payload), &expose); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(expose.Presets, test.want) || expose.previousInvalid != test.wantInvalid {
				t.Fatalf(
					"presets = %#v invalid=%t, want %#v invalid=%t",
					expose.Presets,
					expose.previousInvalid,
					test.want,
					test.wantInvalid,
				)
			}
		})
	}
}

// This test protects Wanda-shaped discovery and fails if the four allowlisted
// exposes do not discover with their exact support, if the power family
// regresses, or if unknown/start_bind/malformed siblings leak into entities
// or suppress valid ones.
func TestDiscoverWandaBulbAttributes(t *testing.T) {
	t.Parallel()
	result, err := discoverInventory(readFixture(t, "bridge-devices-wanda-synthetic.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejections) != 0 || len(result.Devices) != 1 {
		t.Fatalf("discovery = %#v", result)
	}
	device := result.Devices[0]
	if device.IEEEAddress != "0x00124b0022a9b101" || device.FriendlyName != "synthetic-wanda-bulb" ||
		device.Registration.Device.Kind != "light" {
		t.Fatalf("Device identity = %#v", device)
	}
	ieee := "0x00124b0022a9b101"
	want := []adapter.EntityDescriptor{
		{
			Key: "power", ExternalID: ieee + "/root/power", Name: "Power",
			Type: "hearth.power/v1", Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		},
		{
			Key: "brightness", ExternalID: ieee + "/root/brightness", Name: "Brightness",
			Type:    "hearth.brightness/v1",
			Support: json.RawMessage(`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`),
		},
		{
			Key: "colortemp", ExternalID: ieee + "/root/colortemp", Name: "Color Temperature",
			Type:    "hearth.colortemp/v1",
			Support: json.RawMessage(`{"state":{"maximum":500,"minimum":154},"operations":{"set":{"step":1}}}`),
		},
		{
			Key: "colormode", ExternalID: ieee + "/root/colormode", Name: "Color Mode",
			Type: "hearth.colormode/v1", Support: json.RawMessage(`{"state":{},"operations":{}}`),
		},
		{
			Key:        "startupcolortemp",
			ExternalID: ieee + "/root/startupcolortemp",
			Name:       "Startup Color Temperature",
			Type:       "hearth.numericsetting/v1",
			Support: json.RawMessage(
				`{"state":{"choices":["previous"],"maximum":454,"minimum":142,"unit":"mired"},"operations":{"set":{}}}`,
			),
		},
		{
			Key: "poweronbehavior", ExternalID: ieee + "/root/poweronbehavior", Name: "Power-On Behavior",
			Type:    "hearth.enumsetting/v1",
			Support: json.RawMessage(`{"state":{"choices":["off","on","toggle","previous"]},"operations":{"set":{}}}`),
		},
		{
			Key:        "effect",
			ExternalID: ieee + "/root/effect",
			Name:       "Effect",
			Type:       "hearth.enumaction/v1",
			Support: json.RawMessage(
				`{"state":{},"operations":{"trigger":{"values":["blink","breathe","okay","channel_change","finish_effect","stop_effect","stop_hue_effect","colorloop"]}}}`,
			),
		},
		{
			Key: "linkquality", ExternalID: ieee + "/root/linkquality", Name: "Link Quality",
			Type:    "hearth.numericsensor/v1",
			Support: json.RawMessage(`{"state":{"maximum":255,"minimum":0,"unit":"lqi"},"operations":{}}`),
		},
	}
	if !reflect.DeepEqual(device.Registration.Entities, want) {
		t.Fatalf("Entity descriptors = %#v, want %#v", device.Registration.Entities, want)
	}
	wantRoutes := []struct {
		state        []string
		refresh      []string
		controllable bool
		stateless    bool
	}{
		{state: []string{"state"}, refresh: []string{"state"}, controllable: true},
		{state: []string{"brightness"}, refresh: []string{"brightness"}, controllable: true},
		{state: []string{"color_temp"}, refresh: []string{"color_temp"}, controllable: true},
		{state: []string{"color_mode"}, refresh: nil, controllable: false},
		{state: []string{"color_temp_startup"}, refresh: []string{"color_temp_startup"}, controllable: true},
		{state: []string{"power_on_behavior"}, refresh: []string{"power_on_behavior"}, controllable: true},
		{state: nil, refresh: nil, controllable: true, stateless: true},
		{state: []string{"linkquality"}, refresh: nil, controllable: false},
	}
	if len(device.Entities) != len(wantRoutes) {
		t.Fatalf("Entity plans = %#v", device.Entities)
	}
	for index, route := range wantRoutes {
		plan := device.Entities[index]
		if !reflect.DeepEqual(plan.StateProperties, route.state) ||
			!reflect.DeepEqual(plan.GetProperties, route.refresh) ||
			(plan.TranslateCommand != nil) != route.controllable ||
			(plan.StatePolicy == entityStateless) != route.stateless {
			t.Fatalf("Entity route %d = %#v", index, plan)
		}
	}
	if planErr := validateEntityPlans(device.Entities); planErr != nil {
		t.Fatalf("Wanda plans failed validation: %v", planErr)
	}
}

// This test protects the exact eligibility matrix for the four allowlisted
// exposes and fails if access bits, units, values bounds, property
// uniqueness, integer bounds, or previous-preset mapping are relaxed, or if
// one ineligible sibling suppresses valid ones.
func TestBulbAttributeEligibilityAndIsolation(t *testing.T) {
	t.Parallel()
	fullKeys := []string{"power", "brightness", "startupcolortemp", "poweronbehavior", "effect", "linkquality"}
	without := func(keys ...string) []string {
		want := make([]string, 0, len(fullKeys))
		for _, key := range fullKeys {
			if !slices.Contains(keys, key) {
				want = append(want, key)
			}
		}
		return want
	}
	for _, test := range []struct {
		name     string
		edit     func(*upstreamDevice)
		wantKeys []string
		wantKind string
	}{
		{name: "valid", edit: func(*upstreamDevice) {}, wantKeys: fullKeys, wantKind: "light"},
		{
			name:     "startup missing set access",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[0].Features[2].Access = 5 },
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup fractional minimum",
			edit: func(device *upstreamDevice) {
				fractional := 142.5
				device.Definition.Exposes[0].Features[2].ValueMin = &fractional
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup inverted bounds",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[2].ValueMin, device.Definition.Exposes[0].Features[2].ValueMax =
					device.Definition.Exposes[0].Features[2].ValueMax, device.Definition.Exposes[0].Features[2].ValueMin
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup minimum below envelope",
			edit: func(device *upstreamDevice) {
				low := 50.0
				device.Definition.Exposes[0].Features[2].ValueMin = &low
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup previous wrong value",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[2].Presets =
					[]upstreamPreset{{Name: upstreamPreviousPreset, Value: 100}}
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup duplicate previous",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[2].Presets = []upstreamPreset{
					{Name: upstreamPreviousPreset, Value: startupPreviousWireValue},
					{Name: upstreamPreviousPreset, Value: startupPreviousWireValue},
				}
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup invalid previous flag",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[2].Presets = nil
				device.Definition.Exposes[0].Features[2].previousInvalid = true
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name: "startup property collision",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
					Type: upstreamExposeNumeric, Name: "other", Property: startupColorTempExposeName, Access: 1,
				})
			},
			wantKeys: without("startupcolortemp"), wantKind: "light",
		},
		{
			name:     "power behavior missing get access",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[2].Access = 3 },
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name:     "power behavior empty values",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[2].Values = nil },
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name: "power behavior duplicate values",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[2].Values = []string{"off", "off"}
			},
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name: "power behavior too many values",
			edit: func(device *upstreamDevice) {
				values := make([]string, 0, 65)
				for index := range 65 {
					values = append(values, fmt.Sprintf("choice-%d", index))
				}
				device.Definition.Exposes[2].Values = values
			},
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name: "power behavior empty choice",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[2].Values = []string{"off", ""}
			},
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name: "power behavior multibyte choice within rune limit",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[2].Values = []string{strings.Repeat("é", 128)}
			},
			wantKeys: fullKeys, wantKind: "light",
		},
		{
			name: "power behavior multibyte choice beyond rune limit",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[2].Values = []string{strings.Repeat("é", 129)}
			},
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name: "power behavior duplicate roots",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes = append(
					device.Definition.Exposes,
					device.Definition.Exposes[2],
				)
			},
			wantKeys: without("poweronbehavior"), wantKind: "light",
		},
		{
			name:     "effect with get access is not set-only",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[3].Access = 7 },
			wantKeys: without("effect"), wantKind: "light",
		},
		{
			name:     "effect publish-only is not set-only",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[3].Access = 1 },
			wantKeys: without("effect"), wantKind: "light",
		},
		{
			name:     "effect empty values",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[3].Values = nil },
			wantKeys: without("effect"), wantKind: "light",
		},
		{
			name:     "linkquality with set access",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[1].Access = 3 },
			wantKeys: without("linkquality"), wantKind: "light",
		},
		{
			name:     "linkquality unknown unit",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[1].Unit = "%" },
			wantKeys: without("linkquality"), wantKind: "light",
		},
		{
			name:     "linkquality lqi unit passthrough",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[1].Unit = linkqualityUnit },
			wantKeys: fullKeys, wantKind: "light",
		},
		{
			name: "linkquality duplicate roots",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes = append(
					device.Definition.Exposes,
					device.Definition.Exposes[1],
				)
			},
			wantKeys: without("linkquality"), wantKind: "light",
		},
		{
			name:     "broken power family keeps device-agnostic linkquality",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[0].Features[0].Access = 3 },
			wantKeys: []string{"linkquality"}, wantKind: "sensor",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := bulbTestDevice()
			test.edit(&device)
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			if discovered.Registration.Device.Kind != test.wantKind {
				t.Fatalf("Device kind = %q, want %q", discovered.Registration.Device.Kind, test.wantKind)
			}
			if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, test.wantKeys) {
				t.Fatalf("Entity keys = %v, want %v", got, test.wantKeys)
			}
			if err := validateEntityPlans(discovered.Entities); err != nil {
				t.Fatalf("plans failed validation: %v", err)
			}
		})
	}
}

// This test protects linkquality startup refresh and fails if get access
// is not reflected in get properties: publish-only stays refresh-free
// while publish+get advertises its property for startup /get. Both stay
// read-only with no command route, and the publish+get sensor still
// supplements a device whose power family is ineligible.
func TestLinkqualityGetAccessControlsRefresh(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name        string
		access      int
		breakPower  bool
		wantKind    string
		wantRefresh []string
	}{
		{name: "publish-only has no refresh", access: 1, wantKind: "light", wantRefresh: nil},
		{
			name: "publish-get advertises refresh", access: 5,
			wantKind:    "light",
			wantRefresh: []string{linkqualityExposeName},
		},
		{
			name: "publish-get supplements broken power family", access: 5,
			breakPower:  true,
			wantKind:    "sensor",
			wantRefresh: []string{linkqualityExposeName},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			checkLinkqualityAccess(t, test.access, test.breakPower, test.wantKind, test.wantRefresh)
		})
	}
}

// checkLinkqualityAccess discovers one bulb with the given linkquality
// access and fails if eligibility, refresh properties, or the read-only
// plan diverge from the expected contract.
func checkLinkqualityAccess(t *testing.T, access int, breakPower bool, wantKind string, wantRefresh []string) {
	t.Helper()
	device := bulbTestDevice()
	device.Definition.Exposes[1].Access = access
	if breakPower {
		device.Definition.Exposes[0].Features[0].Access = 3
	}
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if discovered.Registration.Device.Kind != wantKind {
		t.Fatalf("Device kind = %q, want %q", discovered.Registration.Device.Kind, wantKind)
	}
	var plan *entityPlan
	for index := range discovered.Entities {
		if discovered.Entities[index].Descriptor.Key == "linkquality" {
			plan = &discovered.Entities[index]
		}
	}
	if plan == nil {
		t.Fatal("linkquality Entity was not discovered")
	}
	if !reflect.DeepEqual(plan.GetProperties, wantRefresh) {
		t.Fatalf("linkquality refresh = %v, want %v", plan.GetProperties, wantRefresh)
	}
	// Read-only is unchanged: no translator, so reconciliation
	// never creates a command route.
	if plan.TranslateCommand != nil {
		t.Fatal("linkquality gained a command translator")
	}
	if err := validateEntityPlans(discovered.Entities); err != nil {
		t.Fatalf("plans failed validation: %v", err)
	}
	assertLinkqualityStartupGet(t, discovered, wantRefresh != nil)
}

// assertLinkqualityStartupGet reconciles one discovered Device and fails if
// startup /get does not publish exactly the expected linkquality refresh.
func assertLinkqualityStartupGet(t *testing.T, discovered discoveredDevice, wantRefresh bool) {
	t.Helper()
	recorder := &runtimeRecorder{}
	session := newFakeSession(recorder)
	z2m := newRuntimeAdapter(t, session, &fakeDialer{})
	ctx := context.Background()
	inventory := inventoryDiscovery{Devices: []discoveredDevice{discovered}}
	snapshot, err := z2m.buildRouteSnapshot(ctx, 1, inventory)
	if err != nil {
		t.Fatal(err)
	}
	for _, entity := range snapshot.devices[discovered.FriendlyName].entities {
		if entity.plan.Descriptor.Key != "linkquality" {
			continue
		}
		if _, routed := snapshot.routes[entity.entityID]; routed {
			t.Fatal("read-only linkquality created a command route")
		}
	}
	connection := newFakeConnection(recorder)
	if refreshErr := z2m.requestCurrentState(ctx, connection, inventory, snapshot); refreshErr != nil {
		t.Fatal(refreshErr)
	}
	refreshing := false
	for _, publication := range connection.published {
		if publication.payload == `{"linkquality":""}` {
			refreshing = true
		}
	}
	if refreshing != wantRefresh {
		t.Fatalf(
			"linkquality startup refresh published = %t, want %t: %#v",
			refreshing,
			wantRefresh,
			connection.published,
		)
	}
}

// startupSupportChoices extracts the advertised named choices of the
// discovered startup entity.
func startupSupportChoices(t *testing.T, device discoveredDevice) []string {
	t.Helper()
	for _, entity := range device.Entities {
		if entity.Descriptor.Key != "startupcolortemp" {
			continue
		}
		var support struct {
			State struct {
				Choices []string `json:"choices"`
			} `json:"state"`
		}
		if err := json.Unmarshal(entity.Descriptor.Support, &support); err != nil {
			t.Fatal(err)
		}
		return support.State.Choices
	}
	t.Fatal("startup entity was not discovered")
	return nil
}

// This test protects dynamic startup choices and fails if a preset-less
// expose does not discover with empty choices, if wire aliases leak into
// choices, or if the previous mapping is lost.
// TestStartupWireBoundsStayWithinMiredEnvelope protects the exact raw-JSON
// path and fails if bounds outside Hearth's 100–1000 mired envelope are
// accepted even though fallback bounds are validated.
func TestStartupWireBoundsStayWithinMiredEnvelope(t *testing.T) {
	t.Parallel()
	fixture := readFixture(t, "bridge-devices-wanda-synthetic.json")
	for _, replacement := range []struct {
		from string
		to   string
	}{
		{from: `"value_min": 142`, to: `"value_min": 99`},
		{from: `"value_max": 454`, to: `"value_max": 1001`},
	} {
		payload := bytes.Replace(fixture, []byte(replacement.from), []byte(replacement.to), 1)
		result, err := discoverInventory(payload)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Devices) != 1 || slices.Contains(entityKeys(result.Devices[0].Entities), "startupcolortemp") {
			t.Fatalf("out-of-envelope startup bounds discovered: %#v", result.Devices)
		}
	}
}

func TestStartupChoicesFollowPresets(t *testing.T) {
	t.Parallel()
	withPrevious := bulbTestDevice()
	discovered, rejection := discoverDevice(withPrevious)
	if rejection != nil {
		t.Fatal(rejection)
	}
	if choices := startupSupportChoices(t, discovered); !reflect.DeepEqual(choices, []string{"previous"}) {
		t.Fatalf("choices = %#v", choices)
	}
	withoutPresets := bulbTestDevice()
	withoutPresets.Definition.Exposes[0].Features[2].Presets = nil
	discovered, rejection = discoverDevice(withoutPresets)
	if rejection != nil {
		t.Fatal(rejection)
	}
	if choices := startupSupportChoices(t, discovered); len(choices) != 0 {
		t.Fatalf("preset-less choices = %#v, want empty", choices)
	}
	aliased := bulbTestDevice()
	aliased.Definition.Exposes[0].Features[2].Presets = []upstreamPreset{
		{Name: upstreamPreviousPreset, Value: startupPreviousWireValue},
		{Name: "warm", Value: 454},
	}
	discovered, rejection = discoverDevice(aliased)
	if rejection != nil {
		t.Fatal(rejection)
	}
	if choices := startupSupportChoices(t, discovered); !reflect.DeepEqual(choices, []string{"previous"}) {
		t.Fatalf("aliased choices = %#v", choices)
	}
}

func mustBulbEntities(testingT interface {
	Helper()
	Fatal(...any)
},
) []runtimeEntity {
	testingT.Helper()
	discovered, rejection := discoverDevice(bulbTestDevice())
	if rejection != nil {
		testingT.Fatal(rejection)
	}
	return bindPlans(discovered.Entities)
}

func decodeBulb(
	testingT interface {
		Helper()
		Fatal(...any)
	},
	entities []runtimeEntity,
	payload string,
) ([]decodedState, []stateDecodeIssue) {
	testingT.Helper()
	states, issues, err := decodeDeviceState([]byte(payload), entities, time.Unix(1, 0).UTC())
	if err != nil {
		testingT.Fatal(err)
	}
	return states, issues
}

func bulbValues(states []decodedState) map[string]string {
	values := make(map[string]string, len(states))
	for _, state := range states {
		values[state.entityID] = string(state.report.Observation.Value)
	}
	return values
}

// This test protects bulb state decoding and fails if 65535 does not map to
// the previous choice, if a plain integer does not map to a value state, or
// if valid siblings are suppressed.
func TestDecodeBulbAttributes(t *testing.T) {
	t.Parallel()
	entities := mustBulbEntities(t)
	states, issues := decodeBulb(
		t,
		entities,
		`{"state":"ON","color_temp_startup":65535,"power_on_behavior":"previous","linkquality":18}`,
	)
	if len(issues) != 0 || len(states) != 4 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	values := bulbValues(states)
	if values["entity-power"] != "true" ||
		values["entity-startupcolortemp"] != `{"choice":"previous","mode":"choice"}` ||
		values["entity-poweronbehavior"] != `"previous"` ||
		values["entity-linkquality"] != "18" {
		t.Fatalf("states = %v", values)
	}
	states, issues = decodeBulb(t, entities, `{"state":"OFF","color_temp_startup":250,"linkquality":0}`)
	if len(issues) != 0 || len(states) != 3 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
	if plain := bulbValues(states); plain["entity-startupcolortemp"] != `{"mode":"value","value":250}` ||
		plain["entity-linkquality"] != "0" {
		t.Fatalf("states = %v", plain)
	}
}

// This test protects startup integer-only decoding and fails if an
// out-of-range integer, fraction, or string decodes, or if the valid power
// and linkquality siblings are suppressed with it.
func TestDecodeBulbInvalidStartup(t *testing.T) {
	t.Parallel()
	entities := mustBulbEntities(t)
	for _, payload := range []string{
		`{"state":"ON","color_temp_startup":455,"linkquality":18}`,
		`{"state":"ON","color_temp_startup":141,"linkquality":18}`,
		`{"state":"ON","color_temp_startup":250.5,"linkquality":18}`,
		`{"state":"ON","color_temp_startup":"250","linkquality":18}`,
		`{"state":"ON","color_temp_startup":9007199254740993,"linkquality":18}`,
		`{"state":"ON","color_temp_startup":1e10000,"linkquality":18}`,
	} {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			states, issues := decodeBulb(t, entities, payload)
			if len(issues) != 1 {
				t.Fatalf("states = %#v, issues = %#v", states, issues)
			}
			values := bulbValues(states)
			if _, present := values["entity-startupcolortemp"]; present {
				t.Fatalf("invalid startup value produced State: %v", values)
			}
			if values["entity-power"] != "true" || values["entity-linkquality"] != "18" {
				t.Fatalf("valid siblings suppressed: states=%v issues=%#v", values, issues)
			}
		})
	}
}

// This test protects sibling isolation for bulb state and fails if an
// invalid linkquality or off-choices power report suppresses the valid
// startup and power siblings, or if an effect property produces state.
func TestDecodeBulbInvalidSiblings(t *testing.T) {
	t.Parallel()
	entities := mustBulbEntities(t)
	for _, payload := range []string{
		`{"state":"ON","color_temp_startup":250,"linkquality":18.5}`,
		`{"state":"ON","color_temp_startup":250,"linkquality":256}`,
		`{"state":"ON","color_temp_startup":250,"linkquality":"18"}`,
		`{"state":"ON","color_temp_startup":250,"linkquality":9007199254740993}`,
		`{"state":"ON","color_temp_startup":250,"power_on_behavior":"eco"}`,
	} {
		t.Run(payload, func(t *testing.T) {
			t.Parallel()
			states, issues := decodeBulb(t, entities, payload)
			if len(issues) != 1 {
				t.Fatalf("states = %#v, issues = %#v", states, issues)
			}
			values := bulbValues(states)
			if values["entity-power"] != "true" ||
				values["entity-startupcolortemp"] != `{"mode":"value","value":250}` {
				t.Fatalf("valid siblings suppressed: states=%v issues=%#v", values, issues)
			}
		})
	}
	states, issues := decodeBulb(t, entities, `{"state":"ON","effect":"breathe"}`)
	if len(issues) != 0 || len(states) != 1 || states[0].entityID != "entity-power" {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
}

// This test protects the previous mapping precondition and fails if 65535
// decodes without advertised previous support.
func TestDecodeBulbSentinelWithoutPrevious(t *testing.T) {
	t.Parallel()
	plain := bulbTestDevice()
	plain.Definition.Exposes[0].Features[2].Presets = nil
	unsupported, unsupportedRejection := discoverDevice(plain)
	if unsupportedRejection != nil {
		t.Fatal(unsupportedRejection)
	}
	states, issues, err := decodeDeviceState(
		[]byte(`{"state":"ON","color_temp_startup":65535}`),
		bindPlans(unsupported.Entities),
		time.Unix(1, 0).UTC(),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(states) != 1 || states[0].entityID != "entity-power" || len(issues) != 1 {
		t.Fatalf("states = %#v, issues = %#v", states, issues)
	}
}

func bulbRoutedPlans(testingT interface {
	Helper()
	Fatal(...any)
},
) map[string]runtimeEntity {
	testingT.Helper()
	discovered, rejection := discoverDevice(bulbTestDevice())
	if rejection != nil {
		testingT.Fatal(rejection)
	}
	entities := bindPlans(discovered.Entities)
	byKey := make(map[string]runtimeEntity, len(entities))
	for _, entity := range entities {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	return byKey
}

func translateBulb(
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
	}
	return translateCommand(
		context.Background(),
		commandRoute{entityID: "entity-" + key, entity: byKey[key]},
		command,
		newFakeResponder(&runtimeRecorder{}, newFakeSession(&runtimeRecorder{})),
	)
}

// This test protects startup command translation and fails if set payloads
// do not carry exact wire values, if refresh properties are wrong, if the
// matcher accepts foreign states, or if fractional, out-of-range, or
// off-choices parameters reach MQTT publication.
func TestTranslateStartupCommands(t *testing.T) {
	t.Parallel()
	byKey := bulbRoutedPlans(t)
	translate := func(key, operation, parameters string) ([]byte, plannedCommand, error) {
		t.Helper()
		return translateBulb(t, byKey, key, operation, parameters)
	}
	t.Run("startup value", func(t *testing.T) {
		t.Parallel()
		payload, planned, err := translate("startupcolortemp", "set", `{"mode":"value","value":250}`)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != `{"color_temp_startup":250}` {
			t.Fatalf("payload = %s", payload)
		}
		if !reflect.DeepEqual(planned.GetProperties, []string{"color_temp_startup"}) {
			t.Fatalf("refresh = %v", planned.GetProperties)
		}
		value := 250.0
		other := 251.0
		choice := "previous"
		if !planned.Matches(stateReport{
			semantic: mustStartupState(t, "value", &value, nil),
		}) || planned.Matches(stateReport{
			semantic: mustStartupState(t, "value", &other, nil),
		}) || planned.Matches(stateReport{
			semantic: mustStartupState(t, "choice", nil, &choice),
		}) {
			t.Fatal("startup matcher did not enforce the commanded value")
		}
	})
	t.Run("startup previous", func(t *testing.T) {
		t.Parallel()
		payload, planned, err := translate("startupcolortemp", "set", `{"mode":"choice","choice":"previous"}`)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != `{"color_temp_startup":65535}` {
			t.Fatalf("payload = %s", payload)
		}
		choice := "previous"
		if !planned.Matches(stateReport{semantic: mustStartupState(t, "choice", nil, &choice)}) {
			t.Fatal("startup matcher rejected the commanded previous choice")
		}
	})
	for _, parameters := range []string{
		`{"mode":"value","value":250.5}`,
		`{"mode":"value","value":455}`,
		`{"mode":"value","value":141}`,
		`{"mode":"choice","choice":"eco"}`,
		`{"mode":"choice","choice":"previous","value":250}`,
		`{"mode":"value"}`,
		`{"mode":"value","value":null}`,
		`{"mode":"choice"}`,
		`{"mode":"choice","choice":null}`,
		`{"mode":"invalid","value":250}`,
		`null`,
	} {
		t.Run("invalid startup "+parameters, func(t *testing.T) {
			t.Parallel()
			if _, _, err := translate("startupcolortemp", "set", parameters); err == nil {
				t.Fatal("invalid startup command was accepted")
			}
		})
	}
}

// This test protects command-side choice membership and fails if the previous
// sentinel can be sent when discovery did not advertise that choice.
func TestTranslateStartupPreviousRequiresSupport(t *testing.T) {
	t.Parallel()
	device := bulbTestDevice()
	device.Definition.Exposes[0].Features[2].Presets = nil
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatal(rejection)
	}
	byKey := make(map[string]runtimeEntity)
	for _, entity := range bindPlans(discovered.Entities) {
		byKey[entity.plan.Descriptor.Key] = entity
	}
	payload, _, err := translateBulb(
		t, byKey, "startupcolortemp", "set", `{"mode":"choice","choice":"previous"}`,
	)
	if err == nil || len(payload) != 0 {
		t.Fatalf("unsupported previous command payload=%s err=%v", payload, err)
	}
}

// This test protects power-on behavior command translation and fails if
// the set payload, refresh, or matcher diverge from the discovered choice,
// or if an off-choices value reaches MQTT publication.
func TestTranslatePowerBehaviorCommand(t *testing.T) {
	t.Parallel()
	byKey := bulbRoutedPlans(t)
	translate := func(key, operation, parameters string) ([]byte, plannedCommand, error) {
		t.Helper()
		return translateBulb(t, byKey, key, operation, parameters)
	}
	t.Run("power behavior", func(t *testing.T) {
		t.Parallel()
		payload, planned, err := translate("poweronbehavior", "set", `{"value":"previous"}`)
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
			planned.Matches(stateReport{semantic: contractEnumSettingState("on")}) {
			t.Fatal("power matcher did not enforce the commanded choice")
		}
		if _, _, err = translate("poweronbehavior", "set", `{"value":"eco"}`); err == nil {
			t.Fatal("off-choices power command was accepted")
		}
	})
}

// This test protects effect trigger translation and fails if the trigger
// payload diverges from the named value, if the plan is not dispatched, or
// if off-values parameters or a set operation reach MQTT publication.
func TestTranslateEffectTrigger(t *testing.T) {
	t.Parallel()
	byKey := bulbRoutedPlans(t)
	translate := func(key, operation, parameters string) ([]byte, plannedCommand, error) {
		t.Helper()
		return translateBulb(t, byKey, key, operation, parameters)
	}
	t.Run("effect trigger", func(t *testing.T) {
		t.Parallel()
		payload, planned, err := translate("effect", "trigger", `{"name":"breathe"}`)
		if err != nil {
			t.Fatal(err)
		}
		if string(payload) != `{"effect":"breathe"}` {
			t.Fatalf("payload = %s", payload)
		}
		if planned.Outcome != plannedDispatched || len(planned.GetProperties) != 0 || planned.Matches != nil {
			t.Fatalf("effect plan is not dispatched: %#v", planned)
		}
		if _, _, err = translate("effect", "trigger", `{"name":"party"}`); err == nil {
			t.Fatal("off-values effect trigger was accepted")
		}
		if _, _, err = translate("effect", "set", `{"name":"breathe"}`); err == nil {
			t.Fatal("effect set operation was accepted")
		}
	})
}
