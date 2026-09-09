package zigbee2mqtt //nolint:testpackage // Tests exercise package-private wire DTOs and discovery routes.

import (
	"encoding/json"
	"math"
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

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

// This test protects strict optional color-temperature discovery and fails if bounds, uniqueness, property identity,
// or the low three access bits are relaxed, or if an invalid optional feature suppresses valid siblings.
func TestDiscoveryColorTempEligibilityAndIsolation(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name      string
		edit      func(*upstreamDevice)
		wantColor bool
	}{
		{
			name: "outer bounds and extra access bits",
			edit: func(device *upstreamDevice) {
				feature := colorTempFeature("color_temp", 100, 1000)
				feature.Access = 15
				device.Definition.Exposes[0].Features = append(device.Definition.Exposes[0].Features, feature)
			},
			wantColor: true,
		},
		{
			name: "missing access bit",
			edit: func(device *upstreamDevice) {
				feature := colorTempFeature("color_temp", 153, 500)
				feature.Access = 6
				device.Definition.Exposes[0].Features = append(device.Definition.Exposes[0].Features, feature)
			},
		},
		{
			name: "empty property",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("", 153, 500),
				)
			},
		},
		{
			name: "duplicate matching feature",
			edit: func(device *upstreamDevice) {
				feature := colorTempFeature("color_temp", 153, 500)
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					feature,
					feature,
				)
			},
		},
		{
			name: "duplicate Device property",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", 153, 500),
				)
				device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
					Type: "numeric", Name: "diagnostic", Property: "color_temp",
				})
			},
		},
		{
			name: "fractional bound",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", 153.5, 500),
				)
			},
		},
		{
			name: "below outer minimum",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", 99, 500),
				)
			},
		},
		{
			name: "above outer maximum",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", 153, 1001),
				)
			},
		},
		{
			name: "unordered bounds",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", 500, 500),
				)
			},
		},
		{
			name: "non-finite bound",
			edit: func(device *upstreamDevice) {
				device.Definition.Exposes[0].Features = append(
					device.Definition.Exposes[0].Features,
					colorTempFeature("color_temp", math.Inf(-1), 500),
				)
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			test.edit(&device)
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			want := []string{"power", "brightness"}
			if test.wantColor {
				want = append(want, "colortemp", "colormode")
			}
			if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
				t.Fatalf("Entity keys = %v, want %v", got, want)
			}
			if test.wantColor {
				colorTemp := discovered.Entities[2]
				support := string(colorTemp.Descriptor.Support)
				if !strings.Contains(support, `"minimum":100`) || !strings.Contains(support, `"maximum":1000`) {
					t.Fatalf("color-temperature support = %s", support)
				}
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

	for labelRunes, wantKeys := range map[int][]string{
		110: {"power-ep1", "colortemp-ep1", "colormode-ep1"},
		111: {"power-ep1"},
	} {
		device := eligibleDevice()
		label := strings.Repeat("c", labelRunes)
		device.Endpoints = map[string]upstreamEndpoint{"1": {Name: label}}
		expose := lightExpose(label, "state_scoped", "")
		expose.Features = expose.Features[:1]
		expose.Features = append(expose.Features, colorTempFeature("color_temp_scoped", 153, 500))
		device.Definition.Exposes = []upstreamExpose{expose}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatalf("%d-rune color-temperature label rejected Device: %#v", labelRunes, rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, wantKeys) {
			t.Fatalf("%d-rune label Entity keys = %v, want %v", labelRunes, got, wantKeys)
		}
		if slices.Contains(wantKeys, "colortemp-ep1") &&
			utf8.RuneCountInString(discovered.Entities[1].Descriptor.Name) != maximumDescriptorRunes {
			t.Fatalf("color-temperature name = %q", discovered.Entities[1].Descriptor.Name)
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
