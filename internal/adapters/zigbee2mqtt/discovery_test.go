package zigbee2mqtt //nolint:testpackage // Tests exercise package-private wire DTOs and discovery routes.

import (
	"bytes"
	"encoding/json"
	"reflect"
	"testing"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// This test protects captured root-light identity and descriptor construction and fails on friendly-name identity,
// hard-coded expose values, unsupported capability leakage, incorrect color-temperature bounds, or generated support.
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
		{
			Key: "colortemp", ExternalID: "0xa4c1380000000001/root/colortemp", Name: "Color Temperature",
			Type:    "hearth.colortemp/v1",
			Support: json.RawMessage(`{"state":{"maximum":500,"minimum":153},"operations":{"set":{"step":1}}}`),
		},
	}
	if !reflect.DeepEqual(device.Registration.Entities, want) {
		t.Fatalf("Entity descriptors = %#v, want %#v", device.Registration.Entities, want)
	}
	if len(device.Entities) != 3 || device.Entities[0].Property != "state" ||
		string(device.Entities[0].PowerOn.Raw) != `"ON"` || device.Entities[1].Property != "brightness" ||
		device.Entities[1].BrightnessMaximum != 255 || device.Entities[2].Property != "color_temp" ||
		device.Entities[2].ColorTempMinimum != 153 || device.Entities[2].ColorTempMaximum != 500 {
		t.Fatalf("Entity routes = %#v", device.Entities)
	}
}

// This test protects optional-feature isolation at the JSON boundary and fails if malformed color-temperature bounds
// cause an otherwise valid power and brightness Device to be rejected.
func TestDiscoverMalformedColorTempBoundsPreservesSiblings(t *testing.T) {
	t.Parallel()
	fixture := readFixture(t, "bridge-devices-3rcb01057z.json")
	for _, invalid := range [][]byte{
		[]byte(`"153"`),
		[]byte(`null`),
		[]byte(`153.00000000000001`),
		[]byte(`1e10000`),
	} {
		payload := bytes.Replace(fixture, []byte(`"value_min": 153`), []byte(`"value_min": `+string(invalid)), 1)
		result, err := discoverInventory(payload)
		if err != nil {
			t.Fatalf("discover inventory with value_min %s: %v", invalid, err)
		}
		if len(result.Rejections) != 0 || len(result.Devices) != 1 {
			t.Fatalf("discovery with value_min %s = %#v", invalid, result)
		}
		if got := entityKeys(result.Devices[0].Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
			t.Fatalf("Entity keys with value_min %s = %v", invalid, got)
		}
	}
}

// This test protects JSON null from being coerced to a numeric zero that creates an unsupported optional Entity.
func TestDiscoverNullBrightnessBoundDoesNotCreateBrightness(t *testing.T) {
	t.Parallel()
	fixture := readFixture(t, "bridge-devices-3rcb01057z.json")
	payload := bytes.Replace(fixture, []byte(`"value_min": 0`), []byte(`"value_min": null`), 1)
	result, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rejections) != 0 || len(result.Devices) != 1 {
		t.Fatalf("discovery = %#v", result)
	}
	if got := entityKeys(result.Devices[0].Entities); !reflect.DeepEqual(got, []string{"power", "colortemp"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects numeric endpoint identity and fails on map-order dependence or cross-endpoint routing.
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
	wantKeys := []string{"power-ep1", "brightness-ep1", "colortemp-ep1", "power-ep2", "brightness-ep2"}
	wantNames := []string{"left Power", "left Brightness", "left Color Temperature", "right Power", "right Brightness"}
	wantProperties := []string{"state_left", "brightness_left", "color_temp_left", "state_right", "brightness_right"}
	wantLocations := []string{"ep1/power", "ep1/brightness", "ep1/colortemp", "ep2/power", "ep2/brightness"}
	for index := range wantKeys {
		entity := device.Entities[index]
		if entity.Descriptor.Key != wantKeys[index] || entity.Descriptor.Name != wantNames[index] ||
			entity.Property != wantProperties[index] {
			t.Fatalf("Entity %d = %#v", index, entity)
		}
		wantExternalID := "0x00124b0000000002/" + wantLocations[index]
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

// This test protects retained bridge/info decoding, including proof that optimistic mode was explicitly false.
// This fixture test protects the valid-power eligibility gate and fails if color temperature creates a Device alone.
func TestColorTempOnlyFixtureIsNotEligible(t *testing.T) {
	t.Parallel()
	result, err := discoverInventory(readFixture(t, "color-temp-only-light.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 0 || len(result.Rejections) != 1 ||
		result.Rejections[0].Code != rejectionNoEligibleLight {
		t.Fatalf("discovery = %#v", result)
	}
}

func TestDecodeCapturedBridgeInfo(t *testing.T) {
	t.Parallel()
	info, err := decodeBridgeInfo(readFixture(t, "bridge-info-2.13.0.json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Version != "2.13.0" || info.MQTTVersion != 4 || !info.AvailabilityEnabled || info.Optimistic {
		t.Fatalf("bridge info = %#v", info)
	}
	if _, err = decodeBridgeInfo([]byte(
		`{"version":"2.13.0","config":{"mqtt":{"version":4},"availability":{"enabled":true},"device_options":{}}}`,
	)); err == nil {
		t.Fatal("bridge info without explicit optimistic behavior was accepted")
	}
}

// FuzzDiscoverInventory protects parser stability and the accepted-output invariants needed by routing.
//
//nolint:gocognit // The fuzz invariant checks every accepted Device and Entity kind together.
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
				if entity.Kind == entityKindColorTemp &&
					(entity.ColorTempMinimum < hearthColorTempMinimum ||
						entity.ColorTempMaximum > hearthColorTempMaximum ||
						entity.ColorTempMinimum >= entity.ColorTempMaximum) {
					t.Fatalf(
						"accepted invalid color temperature range %d..%d",
						entity.ColorTempMinimum,
						entity.ColorTempMaximum,
					)
				}
			}
		}
	})
}
