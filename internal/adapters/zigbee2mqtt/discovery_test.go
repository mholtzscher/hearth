package zigbee2mqtt //nolint:testpackage // Tests exercise package-private wire DTOs and discovery routes.

import (
	"encoding/json"
	"math"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects captured root-light identity and descriptor construction and fails on friendly-name identity,
// hard-coded expose values, unsupported capability leakage, or incorrect generated support.
func TestDiscoverCapturedThirdRealityLight(t *testing.T) {
	t.Parallel()
	result, err := discoverInventory(readFixture(t, "bridge-devices-3rcb01057z.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejections) != 0 || len(result.Devices) != 1 {
		t.Fatalf("discovery = %#v", result)
	}
	device := result.Devices[0]
	if device.IEEEAddress != "0xa4c1380000000001" || device.FriendlyName != "fixture-light" ||
		device.Registration.BindingKey != "z2m-a4c1380000000001" {
		t.Fatalf("Device identity = %#v", device)
	}
	if device.Registration.Device.ExternalID == nil ||
		*device.Registration.Device.ExternalID != "0xa4c1380000000001" ||
		device.Registration.Device.Name != "Sanitized Fixture Light" || device.Registration.Device.Kind != "light" {
		t.Fatalf("Device descriptor = %#v", device.Registration.Device)
	}
	want := []adapter.EntityDescriptor{
		{
			Key: "power", ExternalID: "0xa4c1380000000001/root/power", Name: "Power",
			Type: "hearth.power/v1", Support: json.RawMessage(`{"state":{},"operations":{"set":{}}}`),
		},
		{
			Key: "brightness", ExternalID: "0xa4c1380000000001/root/brightness", Name: "Brightness",
			Type:    "hearth.brightness/v1",
			Support: json.RawMessage(`{"state":{"maximum":100},"operations":{"set":{"step":1}}}`),
		},
	}
	if !reflect.DeepEqual(device.Registration.Entities, want) {
		t.Fatalf("Entity descriptors = %#v, want %#v", device.Registration.Entities, want)
	}
	if len(device.Entities) != 2 || device.Entities[0].Property != "state" ||
		string(device.Entities[0].PowerOn.Raw) != `"ON"` || device.Entities[1].Property != "brightness" ||
		device.Entities[1].BrightnessMaximum != 255 {
		t.Fatalf("Entity routes = %#v", device.Entities)
	}
}

// This test protects numeric endpoint identity and fails on map-order dependence, cross-endpoint routing, or color/effect leakage.
func TestDiscoverMultiEndpointLight(t *testing.T) {
	t.Parallel()
	result, err := discoverInventory(readFixture(t, "multi-endpoint-light.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 1 || len(result.Rejections) != 0 {
		t.Fatalf("discovery = %#v", result)
	}
	device := result.Devices[0]
	wantKeys := []string{"power-ep1", "brightness-ep1", "power-ep2", "brightness-ep2"}
	wantNames := []string{"left Power", "left Brightness", "right Power", "right Brightness"}
	wantProperties := []string{"state_left", "brightness_left", "state_right", "brightness_right"}
	for index := range wantKeys {
		entity := device.Entities[index]
		if entity.Descriptor.Key != wantKeys[index] || entity.Descriptor.Name != wantNames[index] ||
			entity.Property != wantProperties[index] {
			t.Fatalf("Entity %d = %#v", index, entity)
		}
		wantExternalID := "0x00124b0000000002/ep" + strconv.Itoa(1+index/2) + "/"
		if index%2 == 0 {
			wantExternalID += "power"
		} else {
			wantExternalID += "brightness"
		}
		if entity.Descriptor.ExternalID != wantExternalID {
			t.Fatalf("Entity %d external ID = %q, want %q", index, entity.Descriptor.ExternalID, wantExternalID)
		}
	}
}

// This test protects strict canonical IEEE identity and fails if separators, whitespace, short values, or non-hex data pass.
func TestNormalizeIEEEAddress(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		input string
		want  string
		valid bool
	}{
		{input: "0x00124b0024abcdef", want: "0x00124b0024abcdef", valid: true},
		{input: "0X00124B0024ABCDEF", want: "0x00124b0024abcdef", valid: true},
		{input: "00124b0024abcdef"},
		{input: " 0x00124b0024abcdef"},
		{input: "0x00:12:4b:00:24:ab:cd:ef"},
		{input: "0x00124b0024abcdeg"},
		{input: "0x00124b0024abcde"},
	} {
		got, err := normalizeIEEEAddress(test.input)
		if (err == nil) != test.valid || got != test.want {
			t.Errorf(
				"normalizeIEEEAddress(%q) = %q, %v; want %q, valid=%t",
				test.input,
				got,
				err,
				test.want,
				test.valid,
			)
		}
	}
}

// This test protects per-expose isolation and fails if one unresolved endpoint, optional malformed brightness, or duplicate root
// identity discards an independent valid endpoint or accidentally keeps ambiguous routes.
func TestDiscoveryIsolatesMalformedAndAmbiguousExposes(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}, "2": {Name: "right"}}
	valid := lightExpose("left", "state_left", "brightness_left")
	badBrightness := lightExpose("right", "state_right", "brightness_right")
	*badBrightness.Features[1].ValueMin = 1
	unresolved := lightExpose("missing", "state_missing", "brightness_missing")
	duplicateRootA := lightExpose("", "state_root_a", "")
	duplicateRootA.Features = duplicateRootA.Features[:1]
	duplicateRootB := lightExpose("", "state_root_b", "")
	duplicateRootB.Features = duplicateRootB.Features[:1]
	device.Definition.Exposes = []upstreamExpose{valid, badBrightness, unresolved, duplicateRootA, duplicateRootB}

	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	wantKeys := []string{"power-ep1", "brightness-ep1", "power-ep2"}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, wantKeys) {
		t.Fatalf("Entity keys = %v, want %v", got, wantKeys)
	}
}

// This test protects expose eligibility and fails if access bits, power scalar metadata, brightness range, or endpoint
// resolution are ignored, or if optional brightness incorrectly disqualifies valid power.
func TestDiscoveryExposeEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		edit       func(*upstreamDevice)
		wantKeys   []string
		wantReject bool
	}{
		{
			name:       "missing power get access",
			edit:       func(device *upstreamDevice) { device.Definition.Exposes[0].Features[0].Access = 3 },
			wantReject: true,
		},
		{
			name: "equal power values",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[0].ValueOff = json.RawMessage(`"ON"`)
			},
			wantReject: true,
		},
		{
			name: "structured power scalar",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features[0].ValueOn = json.RawMessage(`{"on":true}`)
			},
			wantReject: true,
		},
		{
			name:     "brightness minimum",
			edit:     func(device *upstreamDevice) { *device.Definition.Exposes[0].Features[1].ValueMin = 1 },
			wantKeys: []string{"power"},
		},
		{
			name:     "brightness maximum",
			edit:     func(device *upstreamDevice) { *device.Definition.Exposes[0].Features[1].ValueMax = 99 },
			wantKeys: []string{"power"},
		},
		{
			name:     "brightness missing set access",
			edit:     func(device *upstreamDevice) { device.Definition.Exposes[0].Features[1].Access = 5 },
			wantKeys: []string{"power"},
		},
		{
			name:       "unresolved endpoint",
			edit:       func(device *upstreamDevice) { device.Definition.Exposes[0].Endpoint = "missing" },
			wantReject: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			test.edit(&device)
			discovered, rejection := discoverDevice(device)
			if test.wantReject {
				if rejection == nil || rejection.Code != rejectionNoEligibleLight {
					t.Fatalf("rejection = %#v", rejection)
				}
				return
			}
			if rejection != nil || !reflect.DeepEqual(entityKeys(discovered.Entities), test.wantKeys) {
				t.Fatalf("keys = %v, rejection = %#v", entityKeys(discovered.Entities), rejection)
			}
		})
	}
}

// This test protects endpoint resolution by numeric key and unique endpoint name, including canonical ep<N> fallback labels.
func TestDiscoveryEndpointResolution(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}}
	device.Definition.Exposes = []upstreamExpose{lightExpose("1", "state_1", "brightness_1")}
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("numeric endpoint rejected: %#v", rejection)
	}
	if discovered.Entities[0].Descriptor.Key != "power-ep1" || discovered.Entities[0].Descriptor.Name != "ep1 Power" {
		t.Fatalf("numeric endpoint Entity = %#v", discovered.Entities[0])
	}

	device.Endpoints["2"] = upstreamEndpoint{Name: "shared"}
	device.Endpoints["3"] = upstreamEndpoint{Name: "shared"}
	device.Definition.Exposes = []upstreamExpose{lightExpose("shared", "state_shared", "brightness_shared")}
	_, rejection = discoverDevice(device)
	if rejection == nil || rejection.Code != rejectionNoEligibleLight {
		t.Fatalf("ambiguous endpoint rejection = %#v", rejection)
	}
}

// This test protects global MQTT-property uniqueness and fails if the same State key can route to two Entities.
func TestDiscoveryRejectsDuplicatePropertiesWithoutDiscardingIndependentExpose(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{
		"1": {Name: "left"}, "2": {Name: "right"}, "3": {Name: "independent"},
	}
	left := lightExpose("left", "shared_state", "brightness_left")
	right := lightExpose("right", "shared_state", "brightness_right")
	independent := lightExpose("independent", "state_independent", "brightness_independent")
	device.Definition.Exposes = []upstreamExpose{left, right, independent}

	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	want := []string{"power-ep3", "brightness-ep3"}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entity keys = %v, want %v", got, want)
	}
}

// This test protects the exact 128-rune metadata bound and per-expose isolation instead of truncation.
//
//nolint:gocognit // Independent Device and Entity boundary checks share fixture construction.
func TestDiscoveryEnforcesDescriptorRuneBound(t *testing.T) {
	t.Parallel()
	for runes, accepted := range map[int]bool{128: true, 129: false} {
		device := eligibleDevice()
		device.Description = strings.Repeat("d", runes)
		_, rejection := discoverDevice(device)
		if accepted && rejection != nil {
			t.Fatalf("%d-rune Device name rejected: %#v", runes, rejection)
		}
		if !accepted && (rejection == nil || rejection.Code != rejectionInvalidDescriptor) {
			t.Fatalf("%d-rune Device name rejection = %#v", runes, rejection)
		}
	}

	for labelRunes, accepted := range map[int]bool{122: true, 123: false} {
		device := eligibleDevice()
		label := strings.Repeat("e", labelRunes)
		device.Endpoints = map[string]upstreamEndpoint{"1": {Name: label}}
		expose := lightExpose(label, "state_scoped", "")
		expose.Features = expose.Features[:1]
		device.Definition.Exposes = []upstreamExpose{expose}
		discovered, rejection := discoverDevice(device)
		if accepted && (rejection != nil || len(discovered.Entities) != 1 ||
			utf8.RuneCountInString(discovered.Entities[0].Descriptor.Name) != maximumDescriptorRunes) {
			t.Fatalf("%d-rune label discovery = %#v, rejection = %#v", labelRunes, discovered, rejection)
		}
		if !accepted && (rejection == nil || rejection.Code != rejectionNoEligibleLight) {
			t.Fatalf("%d-rune label rejection = %#v", labelRunes, rejection)
		}
	}

	for labelRunes, wantBrightness := range map[int]bool{117: true, 118: false} {
		device := eligibleDevice()
		label := strings.Repeat("b", labelRunes)
		device.Endpoints = map[string]upstreamEndpoint{"1": {Name: label}}
		device.Definition.Exposes = []upstreamExpose{lightExpose(label, "state_scoped", "brightness_scoped")}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatalf("%d-rune brightness label rejected Device: %#v", labelRunes, rejection)
		}
		if got := len(discovered.Entities) == 2; got != wantBrightness {
			t.Fatalf("%d-rune label brightness present = %t, want %t", labelRunes, got, wantBrightness)
		}
		if wantBrightness &&
			utf8.RuneCountInString(discovered.Entities[1].Descriptor.Name) != maximumDescriptorRunes {
			t.Fatalf("brightness name = %q", discovered.Entities[1].Descriptor.Name)
		}
	}
}

// This test protects the registration protocol bound and fails on accidental splitting or an off-by-one limit.
func TestDiscoveryEnforcesSixtyFourEntityBound(t *testing.T) {
	t.Parallel()
	for exposeCount, rejected := range map[int]bool{32: false, 33: true} {
		device := eligibleDevice()
		device.Endpoints = make(map[string]upstreamEndpoint, exposeCount)
		device.Definition.Exposes = make([]upstreamExpose, 0, exposeCount)
		for endpoint := 1; endpoint <= exposeCount; endpoint++ {
			label := "e" + jsonNumber(endpoint)
			device.Endpoints[jsonNumber(endpoint)] = upstreamEndpoint{Name: label}
			device.Definition.Exposes = append(
				device.Definition.Exposes,
				lightExpose(label, "state_"+label, "brightness_"+label),
			)
		}
		discovered, rejection := discoverDevice(device)
		if rejected {
			if rejection == nil || rejection.Code != rejectionTooManyEntities {
				t.Fatalf("%d exposes rejection = %#v", exposeCount, rejection)
			}
		} else if rejection != nil || len(discovered.Entities) != maximumEntitiesPerDevice {
			t.Fatalf("%d exposes discovery = %#v, rejection = %#v", exposeCount, discovered, rejection)
		}
	}
}

// This test protects Device eligibility gates and fails if unsupported, disabled, incomplete, malformed, or route-unsafe
// inventory entries register.
func TestDiscoveryDeviceEligibility(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		edit func(*upstreamDevice)
		code string
	}{
		{name: "unsupported", edit: func(device *upstreamDevice) { device.Supported = false }, code: rejectionUnsupported},
		{name: "disabled", edit: func(device *upstreamDevice) { device.Disabled = true }, code: rejectionDisabled},
		{name: "interview", edit: func(device *upstreamDevice) { device.InterviewState = "IN_PROGRESS" }, code: rejectionInterview},
		{name: "definition", edit: func(device *upstreamDevice) { device.Definition = nil }, code: rejectionMissingDefinition},
		{name: "ieee", edit: func(device *upstreamDevice) { device.IEEEAddress = "bad" }, code: rejectionInvalidIEEE},
		{name: "friendly_name", edit: func(device *upstreamDevice) { device.FriendlyName = "bad/name" }, code: rejectionInvalidName},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			test.edit(&device)
			_, rejection := discoverDevice(device)
			if rejection == nil || rejection.Code != test.code {
				t.Fatalf("rejection = %#v, want code %q", rejection, test.code)
			}
			if test.name == "disabled" && rejection.IEEEAddress != "0x00124b0024abcdef" {
				t.Fatalf("disabled Device diagnostic IEEE = %q", rejection.IEEEAddress)
			}
		})
	}
}

// This test protects inventory-wide IEEE uniqueness and fails if two registrations can claim one canonical Device.
func TestDiscoverInventoryRejectsDuplicateIEEE(t *testing.T) {
	t.Parallel()
	first := eligibleDevice()
	second := eligibleDevice()
	second.FriendlyName = "renamed-light"
	payload, err := json.Marshal([]upstreamDevice{first, second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 0 || len(result.Rejections) != 1 ||
		result.Rejections[0].Code != rejectionDuplicateIEEE {
		t.Fatalf("discovery = %#v", result)
	}
}

// This test protects route uniqueness and fails if two physical Devices can share one mutable MQTT route.
func TestDiscoverInventoryRejectsDuplicateFriendlyNames(t *testing.T) {
	t.Parallel()
	first := eligibleDevice()
	second := eligibleDevice()
	second.IEEEAddress = "0x00124b0024abcdee"
	payload, err := json.Marshal([]upstreamDevice{first, second})
	if err != nil {
		t.Fatal(err)
	}
	result, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 0 || len(result.Rejections) != 2 {
		t.Fatalf("discovery = %#v", result)
	}
	for _, rejection := range result.Rejections {
		if rejection.Code != rejectionDuplicateFriendly {
			t.Fatalf("rejection = %#v", rejection)
		}
	}
}

// This test protects Device-wide property uniqueness and independent-expose isolation.
func TestDiscoveryCountsPropertiesOutsideLightFeatures(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}, "2": {Name: "right"}}
	left := lightExpose("left", "shared_state", "brightness_left")
	right := lightExpose("right", "state_right", "brightness_right")
	diagnostic := upstreamExpose{Type: "numeric", Name: "diagnostic", Property: "shared_state"}
	device.Definition.Exposes = []upstreamExpose{left, diagnostic, right}

	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	want := []string{"power-ep2", "brightness-ep2"}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entity keys = %v, want %v", got, want)
	}
}

// This test protects malformed-element isolation and complete-document validation.
func TestDiscoverInventoryDocumentBoundaries(t *testing.T) {
	t.Parallel()
	valid, err := json.Marshal(eligibleDevice())
	if err != nil {
		t.Fatal(err)
	}
	payload := append([]byte(`[42,{"ieee_address":7},`), valid...)
	payload = append(payload, ']')
	result, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 1 || len(result.Rejections) != 2 ||
		result.Rejections[0].Code != rejectionMalformedDevice {
		t.Fatalf("discovery = %#v", result)
	}
	for _, malformed := range [][]byte{nil, []byte(`null`), []byte(`{}`), []byte(`[] trailing`)} {
		if _, discoverErr := discoverInventory(malformed); discoverErr == nil {
			t.Errorf("discoverInventory(%q) accepted malformed document", malformed)
		}
	}
}

// This test protects the retained bridge/info DTO shape, including proof that optimistic mode was explicitly false.
func TestDecodeCapturedBridgeInfo(t *testing.T) {
	t.Parallel()
	var info bridgeInfo
	if err := decodeJSON(readFixture(t, "bridge-info-2.13.0.json"), &info); err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.13.0" || info.Config.MQTT.Version != 4 || !info.Config.Availability.Enabled ||
		info.Config.DeviceOptions.Optimistic == nil || *info.Config.DeviceOptions.Optimistic {
		t.Fatalf("bridge info = %#v", info)
	}
}

// FuzzDiscoverInventory protects parser stability and the accepted-output invariants needed by routing.
func FuzzDiscoverInventory(fuzz *testing.F) {
	fuzz.Add(readFixture(fuzz, "bridge-devices-3rcb01057z.json"))
	fuzz.Add(readFixture(fuzz, "multi-endpoint-light.json"))
	fuzz.Add([]byte(`[]`))
	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		result, err := discoverInventory(payload)
		if err != nil {
			return
		}
		for _, device := range result.Devices {
			if !validRouteSlug(device.FriendlyName) {
				t.Fatalf("accepted invalid friendly name %q", device.FriendlyName)
			}
			seen := make(map[string]struct{}, len(device.Entities))
			for _, entity := range device.Entities {
				if _, duplicate := seen[entity.Descriptor.Key]; duplicate {
					t.Fatalf("duplicate Entity key %q", entity.Descriptor.Key)
				}
				seen[entity.Descriptor.Key] = struct{}{}
				if entity.Kind == entityKindBrightness &&
					(!isFinite(entity.BrightnessMaximum) || entity.BrightnessMaximum < 100) {
					t.Fatalf("accepted invalid brightness maximum %v", entity.BrightnessMaximum)
				}
			}
		}
	})
}

func eligibleDevice() upstreamDevice {
	return upstreamDevice{
		IEEEAddress: "0x00124b0024abcdef", Type: "Router", Supported: true,
		FriendlyName: "test-light", InterviewState: "SUCCESSFUL",
		Endpoints: map[string]upstreamEndpoint{},
		Definition: &upstreamDefinition{
			Model: "TEST", Vendor: "Fixture", Description: "Fixture",
			Exposes: []upstreamExpose{lightExpose("", "state", "brightness")},
		},
	}
}

func lightExpose(endpoint, stateProperty, brightnessProperty string) upstreamExpose {
	minimum, maximum, step := 0.0, 255.0, 1.0
	return upstreamExpose{
		Type: "light", Endpoint: endpoint,
		Features: []upstreamExpose{
			{
				Type: "binary", Name: "state", Property: stateProperty, Access: 7,
				ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
			},
			{
				Type: "numeric", Name: "brightness", Property: brightnessProperty, Access: 7,
				ValueMin: &minimum, ValueMax: &maximum, ValueStep: &step,
			},
		},
	}
}

func entityKeys(entities []discoveredEntity) []string {
	keys := make([]string, 0, len(entities))
	for _, entity := range entities {
		keys = append(keys, entity.Descriptor.Key)
	}
	return keys
}

func jsonNumber(value int) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func readFixture(testingT interface {
	Helper()
	Fatal(...any)
}, name string) []byte {
	testingT.Helper()
	payload, err := os.ReadFile("testdata/" + name)
	if err != nil {
		testingT.Fatal(err)
	}
	return payload
}

func TestBrightnessRangeRejectsNonFiniteValues(t *testing.T) {
	t.Parallel()
	for _, maximum := range []float64{math.NaN(), math.Inf(1), math.Inf(-1), 99.99} {
		if brightnessRangeSupported(maximum) {
			t.Errorf("brightnessRangeSupported(%v) = true", maximum)
		}
	}
	if !brightnessRangeSupported(100) {
		t.Fatal("brightness maximum 100 was rejected")
	}
}

func TestValidRouteSlugBoundaries(t *testing.T) {
	t.Parallel()
	if !validRouteSlug("a"+strings.Repeat("-", 62)) || validRouteSlug("a"+strings.Repeat("-", 63)) ||
		validRouteSlug("Upper") || validRouteSlug("with.dot") || validRouteSlug("_prefix") {
		t.Fatal("route slug boundary validation did not enforce the specified grammar")
	}
}
