package zigbee2mqtt //nolint:testpackage // Tests exercise package-private discovery routes.

import (
	"reflect"
	"testing"
)

// This test protects exact optional-Entity discovery for every representation
// combination: XY-only, HS-only, dual, and multi-endpoint devices discover
// exactly the intended optional Entities beside power and brightness, with
// stable numeric identities and companion mode suffixes per endpoint label.
func TestDiscoverColorRepresentationCombinations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		fixture string
		want    []string
	}{
		{
			fixture: "bridge-devices-color-dual.json",
			want:    []string{"power", "brightness", "colortemp", "colorxy", "colorhs", "colormode"},
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
			device := mustDiscoveredFixtureDevice(t, test.fixture)
			if got := entityKeys(device.Entities); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Entity keys = %v, want %v", got, test.want)
			}
			if err := validateEntityPlans(device.Entities); err != nil {
				t.Fatalf("planned color Entities failed validation: %v", err)
			}
		})
	}
}

// This test protects discovered (not assumed) property routing and canonical
// identity for color Entities: external IDs, display names, State properties,
// and refresh properties follow the exposes, with companion mode properties
// derived from exact upstream endpoint labels.
//
//nolint:gocognit // One table keeps every color identity assertion together.
func TestDiscoverColorIdentitiesAndProperties(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	ieee := "0x00124b0000000011"
	for _, test := range []struct {
		key       string
		external  string
		name      string
		state     []string
		refresh   []string
		entityTyp string
	}{
		{
			key: "colorxy", external: ieee + "/root/colorxy", name: "Color XY",
			state: []string{"color", "color_mode"}, refresh: []string{"color"},
			entityTyp: "hearth.colorxy/v1",
		},
		{
			key: "colorhs", external: ieee + "/root/colorhs", name: "Color Hue/Saturation",
			state: []string{"color", "color_mode"}, refresh: []string{"color"},
			entityTyp: "hearth.colorhs/v1",
		},
		{
			key: "colormode", external: ieee + "/root/colormode", name: "Color Mode",
			state: []string{"color_mode"}, refresh: nil,
			entityTyp: "hearth.colormode/v1",
		},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()
			var found *entityPlan
			for index := range device.Entities {
				if device.Entities[index].Descriptor.Key == test.key {
					found = &device.Entities[index]
				}
			}
			if found == nil {
				t.Fatalf("Entity %q was not discovered", test.key)
			}
			if found.Descriptor.ExternalID != test.external || found.Descriptor.Name != test.name ||
				found.Descriptor.Type != test.entityTyp {
				t.Fatalf("Entity descriptor = %#v", found.Descriptor)
			}
			if !reflect.DeepEqual(found.StateProperties, test.state) ||
				!reflect.DeepEqual(found.GetProperties, test.refresh) {
				t.Fatalf("Entity routes = state %v get %v", found.StateProperties, found.GetProperties)
			}
			if test.key == "colormode" && found.TranslateCommand != nil {
				t.Fatal("mode Entity must be read-only")
			}
		})
	}
}

// This test protects endpoint-scoped color identity: companion mode suffixes
// use exact upstream endpoint labels, and Entity keys stay numeric.
func TestDiscoverColorEndpointIdentities(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-endpoints.json")
	ieee := "0x00124b0000000014"
	for _, test := range []struct {
		key      string
		external string
		name     string
		state    []string
	}{
		{
			key: "colorxy-ep1", external: ieee + "/ep1/colorxy", name: "left Color XY",
			state: []string{"color_left", "color_mode_left"},
		},
		{
			key: "colormode-ep1", external: ieee + "/ep1/colormode", name: "left Color Mode",
			state: []string{"color_mode_left"},
		},
		{
			key: "colorhs-ep2", external: ieee + "/ep2/colorhs", name: "right Color Hue/Saturation",
			state: []string{"color_right", "color_mode_right"},
		},
		{
			key: "colormode-ep2", external: ieee + "/ep2/colormode", name: "right Color Mode",
			state: []string{"color_mode_right"},
		},
	} {
		t.Run(test.key, func(t *testing.T) {
			t.Parallel()
			for _, entity := range device.Entities {
				if entity.Descriptor.Key != test.key {
					continue
				}
				if entity.Descriptor.ExternalID != test.external || entity.Descriptor.Name != test.name ||
					!reflect.DeepEqual(entity.StateProperties, test.state) {
					t.Fatalf("Entity = %#v properties = %v", entity.Descriptor, entity.StateProperties)
				}
				return
			}
			t.Fatalf("Entity %q was not discovered", test.key)
		})
	}
}

// This test protects the shared-property exception: one XY composite and one
// HS composite may share a property within the same root, while duplicate
// same-representation claims, cross-root claims, and unrelated claims omit
// only the affected color candidates without suppressing valid siblings.
func TestDiscoverColorPropertyOwnership(t *testing.T) {
	t.Parallel()
	xyChildren := []upstreamExpose{
		colorAxisChild("x", nil, nil),
		colorAxisChild("y", nil, nil),
	}
	hsChildren := []upstreamExpose{
		colorAxisChild("hue", nil, nil),
		colorAxisChild("saturation", nil, nil),
	}
	badHueMin, badHueMax := 1.0, 2.0
	badHue := colorAxisChild("hue", &badHueMin, &badHueMax)
	for _, test := range []struct {
		name   string
		expose upstreamExpose
		extra  []upstreamExpose
		want   []string
	}{
		{
			name: "dual shared property survives",
			expose: colorLightExpose("", "state", "brightness",
				colorXYComposite("color", xyChildren...),
				colorHSComposite("color", hsChildren...)),
			want: []string{"power", "brightness", "colorxy", "colorhs", "colormode"},
		},
		{
			name: "duplicate same representation disqualifies both claims",
			expose: colorLightExpose("", "state", "brightness",
				colorXYComposite("color", xyChildren...), colorXYComposite("color", xyChildren...),
				colorHSComposite("other", hsChildren...)),
			want: []string{"power", "brightness", "colorhs", "colormode"},
		},
		{
			name: "duplicate HS invalidates shared ownership including XY",
			expose: colorLightExpose("", "state", "brightness",
				colorXYComposite("color", xyChildren...),
				colorHSComposite("color", hsChildren...), colorHSComposite("color", hsChildren...)),
			want: []string{"power", "brightness"},
		},
		{
			name:   "unrelated claim disqualifies the color candidate",
			expose: colorLightExpose("", "state", "brightness", colorXYComposite("color", xyChildren...)),
			extra: []upstreamExpose{
				{Type: "numeric", Name: "diagnostic", Property: "color"},
			},
			want: []string{"power", "brightness"},
		},
		{
			name: "invalid HS never suppresses valid XY",
			expose: colorLightExpose("", "state", "brightness",
				colorXYComposite("color", xyChildren...),
				colorHSComposite("color", badHue)),
			want: []string{"power", "brightness", "colorxy", "colormode"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			device.Definition.Exposes = []upstreamExpose{test.expose}
			device.Definition.Exposes = append(device.Definition.Exposes, test.extra...)
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Entity keys = %v, want %v", got, test.want)
			}
		})
	}
}

// This test protects cross-root property isolation: a color property claimed
// in another root disqualifies the affected candidates in every claiming
// root, while an independent root keeps its own color Entities.
func TestDiscoverColorPropertyAcrossRoots(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{
		"1": {Name: "left"}, "2": {Name: "right"}, "3": {Name: "independent"},
	}
	children := []upstreamExpose{colorAxisChild("x", nil, nil), colorAxisChild("y", nil, nil)}
	left := colorLightExpose("left", "state_left", "brightness_left", colorXYComposite("color", children...))
	right := colorLightExpose("right", "state_right", "brightness_right", colorXYComposite("color", children...))
	independent := colorLightExpose(
		"independent", "state_independent", "brightness_independent", colorXYComposite("color_ind", children...),
	)
	device.Definition.Exposes = []upstreamExpose{left, right, independent}

	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	// The shared claim disqualifies both left and right XY candidates while
	// the independent root keeps its own color Entities.
	want := []string{
		"power-ep1", "brightness-ep1", "power-ep2", "brightness-ep2",
		"power-ep3", "brightness-ep3", "colorxy-ep3", "colormode-ep3",
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
		t.Fatalf("Entity keys = %v, want %v", got, want)
	}
}

// This test protects strict color candidate validation: access bits,
// property identity, child completeness, child access, and explicit bounds
// each omit only the affected candidate, and temperature mode policy follows
// the advertised composite.
//
//nolint:gocognit // One table keeps every candidate eligibility case together.
func TestDiscoverColorCandidateEligibility(t *testing.T) {
	t.Parallel()
	children := func() []upstreamExpose {
		return []upstreamExpose{colorAxisChild("x", nil, nil), colorAxisChild("y", nil, nil)}
	}
	for _, test := range []struct {
		name   string
		edit   func(*upstreamExpose)
		wantXY bool
	}{
		{
			name:   "extra access bits are accepted",
			edit:   func(feature *upstreamExpose) { feature.Access = 15 },
			wantXY: true,
		},
		{
			name:   "missing set access omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Access = 5 },
			wantXY: false,
		},
		{
			name:   "empty property omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Property = "" },
			wantXY: false,
		},
		{
			name:   "missing child omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Features = feature.Features[:1] },
			wantXY: false,
		},
		{
			name:   "duplicate child omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Features = append(feature.Features, feature.Features[0]) },
			wantXY: false,
		},
		{
			name:   "child access failure omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Features[0].Access = 5 },
			wantXY: false,
		},
		{
			name:   "mismatched child property omits the candidate",
			edit:   func(feature *upstreamExpose) { feature.Features[0].Property = "color" },
			wantXY: false,
		},
		{
			name: "wrong child bounds omit the candidate",
			edit: func(feature *upstreamExpose) {
				wrong := 2.0
				feature.Features[1].ValueMax = &wrong
			},
			wantXY: false,
		},
		{
			name: "matching explicit bounds are accepted",
			edit: func(feature *upstreamExpose) {
				lower, upper := 0.0, 1.0
				feature.Features[0].ValueMin = &lower
				feature.Features[0].ValueMax = &upper
				feature.Features[1].ValueMin = &lower
				feature.Features[1].ValueMax = &upper
			},
			wantXY: true,
		},
		{
			name: "extra unrelated children are harmless",
			edit: func(feature *upstreamExpose) {
				feature.Features = append(feature.Features, upstreamExpose{
					Type: "numeric", Name: "foo", Property: "foo", Access: 7,
				})
			},
			wantXY: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			composite := colorXYComposite("color", children()...)
			test.edit(&composite)
			device.Definition.Exposes = []upstreamExpose{
				colorLightExpose("", "state", "brightness", composite, colorTempFeature("color_temp", 153, 500)),
			}
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			want := []string{"power", "brightness", "colortemp", "colormode"}
			if test.wantXY {
				want = []string{"power", "brightness", "colortemp", "colorxy", "colormode"}
			}
			if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
				t.Fatalf("Entity keys = %v, want %v", got, want)
			}
			// The advertised composite keeps temperature mode-sensitive even
			// when the XY candidate itself is unplannable.
			for _, entity := range discovered.Entities {
				if entity.Descriptor.Key != "colortemp" {
					continue
				}
				if !reflect.DeepEqual(entity.StateProperties, []string{"color_temp", "color_mode"}) {
					t.Fatalf("temperature State properties = %v", entity.StateProperties)
				}
			}
		})
	}
}

// This test protects temperature mode policy: temperature without any
// advertised color composite stays active when mode is absent, while an
// advertised but unplannable composite keeps temperature mode-sensitive with
// no silent always-active fallback.
func TestDiscoverTemperatureModePolicy(t *testing.T) {
	t.Parallel()
	t.Run("temperature only falls back without requiring mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 153, 500)),
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(
			got,
			[]string{"power", "brightness", "colortemp", "colormode"},
		) {
			t.Fatalf("Entity keys = %v", got)
		}
		if got := discovered.Entities[2].StateProperties; !reflect.DeepEqual(got, []string{"color_temp"}) {
			t.Fatalf("fallback temperature State properties = %v", got)
		}
	})
	t.Run("color without temperature plans no temperature Entity", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness",
				colorXYComposite("color", colorAxisChild("x", nil, nil), colorAxisChild("y", nil, nil))),
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(
			got,
			[]string{"power", "brightness", "colorxy", "colormode"},
		) {
			t.Fatalf("Entity keys = %v", got)
		}
	})
	t.Run("advertised but unplannable color without temperature plans nothing extra", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorXYComposite("color")),
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
			t.Fatalf("Entity keys = %v", got)
		}
	})
}

// This test protects companion mode ownership: an ambiguous or colliding
// mode property omits the affected color and mode capabilities instead of
// guessing, and mode-sensitive temperature never silently falls back to
// always-active behavior.
func TestDiscoverColorModeOwnership(t *testing.T) {
	t.Parallel()
	children := []upstreamExpose{colorAxisChild("x", nil, nil), colorAxisChild("y", nil, nil)}
	t.Run("foreign mode property claim omits color, mode, and sensitive temperature", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness",
				colorXYComposite("color", children...), colorTempFeature("color_temp", 153, 500)),
			{Type: "numeric", Name: "diagnostic", Property: "color_mode"},
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
			t.Fatalf("Entity keys = %v", got)
		}
	})
	t.Run("foreign mode claim omits temperature-only Entity instead of falling back", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 153, 500)),
			{Type: "numeric", Name: "diagnostic", Property: "color_mode"},
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		// A colliding companion cannot be trusted for present-mode activity
		// (hs must not decode as active temperature, malformed must not be
		// swallowed), so temperature is omitted rather than silently
		// always-active. The spec fallback covers an absent mode only.
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(
			got,
			[]string{"power", "brightness"},
		) {
			t.Fatalf("Entity keys = %v", got)
		}
	})
	t.Run("declared mode enum with the same meaning does not block", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorXYComposite("color", children...)),
			{Type: "enum", Name: "color_mode", Property: "color_mode"},
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatal(rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(
			got,
			[]string{"power", "brightness", "colorxy", "colormode"},
		) {
			t.Fatalf("Entity keys = %v", got)
		}
	})
}

// This test protects deep ownership traversal: only direct same-root color
// siblings may share a color property. A nested descendant claiming the same
// property disqualifies the candidate, and a nested foreign mode claim omits
// color, mode, and temperature instead of guessing.
func TestDiscoverColorOwnershipTraversesDescendants(t *testing.T) {
	t.Parallel()
	freshXYChildren := func() []upstreamExpose {
		return []upstreamExpose{colorAxisChild("x", nil, nil), colorAxisChild("y", nil, nil)}
	}
	t.Run("nested color property claim disqualifies the candidate", func(t *testing.T) {
		t.Parallel()
		composite := colorXYComposite("color", freshXYChildren()...)
		composite.Features[0].Features = []upstreamExpose{
			{Type: "numeric", Name: "nested", Property: "color", Access: 7},
		}
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", composite),
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatalf("Device rejected: %#v", rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
			t.Fatalf("Entity keys = %v, want power and brightness only", got)
		}
	})
	t.Run("nested foreign mode claim omits color, mode, and temperature", func(t *testing.T) {
		t.Parallel()
		nested := colorXYComposite("probe", colorAxisChild("x", nil, nil))
		nested.Features = append(nested.Features, upstreamExpose{
			Type: "numeric", Name: "diagnostic", Property: "color_mode", Access: 1,
		})
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness",
				colorXYComposite("color", freshXYChildren()...), colorTempFeature("color_temp", 153, 500)),
			nested,
		}
		discovered, rejection := discoverDevice(device)
		if rejection != nil {
			t.Fatalf("Device rejected: %#v", rejection)
		}
		if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power", "brightness"}) {
			t.Fatalf("Entity keys = %v, want power and brightness only", got)
		}
	})
}
