package zigbee2mqtt //nolint:testpackage // Contract tests exercise package-private discovery and wire DTOs.

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"
)

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

// contractLightMutation applies one device mutation to a fresh dual-color
// fixture so each case isolates exactly one eligibility dimension.
func contractLightMutation(t *testing.T, mutate func(*upstreamDevice)) upstreamDevice {
	t.Helper()
	devices := fixtureDevices(t, "bridge-devices-color-dual.json")
	if len(devices) != 1 {
		t.Fatal("dual fixture must contain exactly one device")
	}
	device := devices[0]
	mutate(&device)
	return device
}

// requireContractLightKeys asserts that one mutated device produces exactly
// the expected light contribution keys. The defect would be a light profile
// that admits or drops a different set for that eligibility rule.
func requireContractLightKeys(t *testing.T, name string, device upstreamDevice, wantKeys []string) {
	t.Helper()
	gotKeys := entityKeys(contractProfileContribution(t, "light", device).Entities)
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

// buildContractLightBrightnessWithoutSetAccess returns the dual fixture
// with brightness set access removed.
func buildContractLightBrightnessWithoutSetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		lightTestFeature(device, "brightness").Access = 1
	})
}

// buildContractLightInvertedTemperatureBounds returns the dual fixture with
// inverted temperature bounds.
func buildContractLightInvertedTemperatureBounds(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		feature := lightTestFeature(device, "color_temp")
		feature.valueMinRaw = json.RawMessage(`500`)
		feature.valueMaxRaw = json.RawMessage(`150`)
		minimum, maximum := 500.0, 150.0
		feature.ValueMin = &minimum
		feature.ValueMax = &maximum
	})
}

// buildContractLightDuplicateBrightnessFeatures returns the dual fixture
// with a duplicated brightness feature.
func buildContractLightDuplicateBrightnessFeatures(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		duplicate := *lightTestFeature(device, "brightness")
		duplicate.Property = "brightness_shadow"
		for rootIndex := range device.Definition.Exposes {
			if device.Definition.Exposes[rootIndex].Type == upstreamDeviceKindLight {
				device.Definition.Exposes[rootIndex].Features = append(
					device.Definition.Exposes[rootIndex].Features, duplicate)
			}
		}
	})
}

// buildContractLightXYAxisWithoutSetAccess returns the dual fixture with
// the x axis unreadable for set.
func buildContractLightXYAxisWithoutSetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		feature := lightTestFeature(device, "color_xy")
		for axisIndex := range feature.Features {
			if feature.Features[axisIndex].Name == "x" {
				feature.Features[axisIndex].Access = 1
			}
		}
	})
}

// buildContractLightHSAxisWithWrongProperty returns the dual fixture with
// the hue axis property retargeted.
func buildContractLightHSAxisWithWrongProperty(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		feature := lightTestFeature(device, "color_hs")
		for axisIndex := range feature.Features {
			if feature.Features[axisIndex].Name == "hue" {
				feature.Features[axisIndex].Property = "hue_shadow"
			}
		}
	})
}

// buildContractLightDuplicateColorProperty returns the dual fixture with a
// duplicated XY composite.
func buildContractLightDuplicateColorProperty(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		for rootIndex := range device.Definition.Exposes {
			if device.Definition.Exposes[rootIndex].Type == upstreamDeviceKindLight {
				duplicate := *lightTestFeature(device, "color_xy")
				device.Definition.Exposes[rootIndex].Features = append(
					device.Definition.Exposes[rootIndex].Features, duplicate)
			}
		}
	})
}

// buildContractLightForeignColorModeClaim returns the dual fixture with a
// foreign color_mode claim.
func buildContractLightForeignColorModeClaim(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
			Type: "numeric", Name: "diagnostic", Property: "color_mode", Access: 1,
		})
	})
}

// buildContractLightUnresolvedEndpoint returns the dual fixture with the
// first root pointed at a missing endpoint.
func buildContractLightUnresolvedEndpoint(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		device.Definition.Exposes[0].Endpoint = "missing"
	})
}

// buildContractBulbWithoutBehaviorGetAccess returns the bulb with power-on
// behavior get access removed.
func buildContractBulbWithoutBehaviorGetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, contractBulbBehaviorMutation(func(expose *upstreamExpose) {
		expose.Access = 3
	}))
}

// buildContractBulbDuplicateBehaviorRoots returns the bulb with a
// duplicated power-on behavior root.
func buildContractBulbDuplicateBehaviorRoots(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, duplicateContractExpose(powerOnBehaviorExposeName))
}

// buildContractBulbEffectWithGetAccess returns the bulb with effect get
// access added.
func buildContractBulbEffectWithGetAccess(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, contractBulbEffectMutation(func(expose *upstreamExpose) {
		expose.Access = 7
	}))
}

// buildContractBulbEffectEmptyValues returns the bulb with an empty effect
// value list.
func buildContractBulbEffectEmptyValues(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, contractBulbEffectMutation(func(expose *upstreamExpose) {
		expose.Values = nil
	}))
}

// buildContractBulbDuplicateEffectRoots returns the bulb with a duplicated
// effect root.
func buildContractBulbDuplicateEffectRoots(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, duplicateContractExpose(effectExposeName))
}

// buildContractBulbStartupMalformedBounds returns the bulb with inverted
// startup bounds.
func buildContractBulbStartupMalformedBounds(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, func(device *upstreamDevice) {
		minimum, maximum := 454.0, 142.0
		device.Definition.Exposes[0].Features[2].ValueMin = &minimum
		device.Definition.Exposes[0].Features[2].ValueMax = &maximum
	})
}

// buildContractBulbStartupWithoutPreset returns the bulb with the previous
// preset removed.
func buildContractBulbStartupWithoutPreset(t *testing.T) upstreamDevice {
	t.Helper()
	return contractBulbMutation(t, func(device *upstreamDevice) {
		device.Definition.Exposes[0].Features[2].Presets = nil
	})
}

// buildContractDualInvalidPower returns the dual fixture with ineligible
// light power.
func buildContractDualInvalidPower(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		lightTestFeature(device, "state").Access = 3
	})
}

// buildContractDualDuplicatePowerKeys returns the dual fixture with
// duplicated power keys.
func buildContractDualDuplicatePowerKeys(t *testing.T) upstreamDevice {
	t.Helper()
	return contractLightMutation(t, func(device *upstreamDevice) {
		duplicate := upstreamExpose{
			Type: "light",
			Features: []upstreamExpose{{
				Type: "binary", Name: "state", Property: "state", Access: 7,
				ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
			}},
		}
		device.Definition.Exposes = append([]upstreamExpose{duplicate}, device.Definition.Exposes...)
	})
}

// buildContractBulbInvalidPower returns the bulb with ineligible light
// power.
func buildContractBulbInvalidPower(t *testing.T) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	device.Definition.Exposes[0].Features[0].Access = 3
	return device
}

// duplicateContractExpose returns a mutation duplicating the named expose
// root.
func duplicateContractExpose(name string) func(*upstreamDevice) {
	return func(device *upstreamDevice) {
		for index := range device.Definition.Exposes {
			if device.Definition.Exposes[index].Name == name {
				device.Definition.Exposes = append(
					device.Definition.Exposes, device.Definition.Exposes[index])
			}
		}
	}
}

// This test protects color representation, dependency, and duplicate-gate
// eligibility and fails if any access, bound, axis, property, foreign-claim,
// duplicate-root, or unresolved-endpoint violation registers, or if a valid
// sibling is suppressed with it. Each case mutates exactly one dimension of
// the captured dual light.
// contractLightCase describes one single-dimension light eligibility
// mutation: how to build the device and the exact contribution keys
// expected on the profile path.
type contractLightCase struct {
	name     string
	build    func(t *testing.T) upstreamDevice
	wantKeys []string
}

// runContractLightCases executes one eligibility case per subtest. Tables
// keep each top-level test linear while the per-case builders isolate
// exactly one eligibility dimension.
func runContractLightCases(t *testing.T, cases []contractLightCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			requireContractLightKeys(t, testCase.name, testCase.build(t), testCase.wantKeys)
		})
	}
}

// contractBulbMutation applies one mutation to a fresh bulb device so each
// device-entity case isolates exactly one eligibility dimension.
func contractBulbMutation(t *testing.T, mutate func(*upstreamDevice)) upstreamDevice {
	t.Helper()
	device := bulbTestDevice()
	mutate(&device)
	return device
}

// This test protects color representation, dependency, and duplicate-gate
// eligibility and fails if any access, bound, axis, property, foreign-claim,
// duplicate-root, or unresolved-endpoint violation registers, or if a valid
// sibling is suppressed with it. Each case mutates exactly one dimension of
// the captured dual light.
func TestProfileContractLightIsolatesMalformedSiblings(t *testing.T) {
	t.Parallel()
	runContractLightCases(t, []contractLightCase{
		{
			name:     "brightness without set access is omitted",
			build:    buildContractLightBrightnessWithoutSetAccess,
			wantKeys: dualLightKeysWithout("brightness"),
		},
		{
			// Mode survives on the remaining XY and HS siblings.
			name:     "inverted temperature bounds omit temperature but keep mode",
			build:    buildContractLightInvertedTemperatureBounds,
			wantKeys: dualLightKeysWithout("colortemp"),
		},
		{
			name:     "duplicate brightness features omit only brightness",
			build:    buildContractLightDuplicateBrightnessFeatures,
			wantKeys: dualLightKeysWithout("brightness"),
		},
		{
			name:     "xy axis without set access omits xy but keeps mode",
			build:    buildContractLightXYAxisWithoutSetAccess,
			wantKeys: dualLightKeysWithout("colorxy"),
		},
		{
			name:     "hs axis with wrong property omits hs but keeps mode",
			build:    buildContractLightHSAxisWithWrongProperty,
			wantKeys: dualLightKeysWithout("colorhs"),
		},
		{
			name:     "duplicate color property omits both composites but keeps mode",
			build:    buildContractLightDuplicateColorProperty,
			wantKeys: dualLightKeysWithout("colorxy", "colorhs"),
		},
		{
			name:     "foreign color mode claim omits color, mode, and temperature",
			build:    buildContractLightForeignColorModeClaim,
			wantKeys: []string{"power", "brightness"},
		},
		{
			name:     "unresolved light endpoint plans no light family",
			build:    buildContractLightUnresolvedEndpoint,
			wantKeys: nil,
		},
	})
}

// contractBulbBehaviorMutation applies one mutation to the power-on
// behavior expose of a fresh bulb device.
func contractBulbBehaviorMutation(mutate func(*upstreamExpose)) func(*upstreamDevice) {
	return func(device *upstreamDevice) {
		for index := range device.Definition.Exposes {
			if device.Definition.Exposes[index].Name == powerOnBehaviorExposeName {
				mutate(&device.Definition.Exposes[index])
			}
		}
	}
}

// contractBulbEffectMutation applies one mutation to the effect expose of
// a fresh bulb device.
func contractBulbEffectMutation(mutate func(*upstreamExpose)) func(*upstreamDevice) {
	return func(device *upstreamDevice) {
		for index := range device.Definition.Exposes {
			if device.Definition.Exposes[index].Name == effectExposeName {
				mutate(&device.Definition.Exposes[index])
			}
		}
	}
}

// This test protects light device-entity eligibility and fails if any
// access, value, property, duplicate-root, or bound violation on power-on
// behavior, effect, or startup registers, or if a valid sibling is
// suppressed with it.
func TestProfileContractLightIsolatesDeviceEntitySiblings(t *testing.T) {
	t.Parallel()
	runContractLightCases(t, []contractLightCase{
		{
			name:     "behavior without get access is omitted",
			build:    buildContractBulbWithoutBehaviorGetAccess,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "effect"},
		},
		{
			name:     "duplicate behavior roots omit only behavior",
			build:    buildContractBulbDuplicateBehaviorRoots,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "effect"},
		},
		{
			name:     "effect with get access is not set-only",
			build:    buildContractBulbEffectWithGetAccess,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:     "effect with empty values is omitted",
			build:    buildContractBulbEffectEmptyValues,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:     "duplicate effect roots omit only effect",
			build:    buildContractBulbDuplicateEffectRoots,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "poweronbehavior"},
		},
		{
			name:     "startup with malformed bounds is omitted",
			build:    buildContractBulbStartupMalformedBounds,
			wantKeys: []string{"power", "brightness", "poweronbehavior", "effect"},
		},
		{
			name:     "startup without previous preset keeps value mode",
			build:    buildContractBulbStartupWithoutPreset,
			wantKeys: []string{"power", "brightness", "startupcolortemp", "poweronbehavior", "effect"},
		},
	})
}

// This test protects light family gating and fails if device-root
// attributes survive without eligible light power, if valid siblings are
// suppressed with a broken root, or if duplicate power keys keep an
// ambiguous route.
func TestProfileContractLightFamilyGating(t *testing.T) {
	t.Parallel()
	runContractLightCases(t, []contractLightCase{
		{
			name:     "invalid power gates every attribute",
			build:    buildContractDualInvalidPower,
			wantKeys: nil,
		},
		{
			name:     "duplicate power keys gate attributes",
			build:    buildContractDualDuplicatePowerKeys,
			wantKeys: nil,
		},
		{
			name:     "device attributes need surviving power",
			build:    buildContractBulbInvalidPower,
			wantKeys: nil,
		},
	})
	t.Run("invalid light root spares the valid sibling", func(t *testing.T) {
		t.Parallel()
		devices := fixtureDevices(t, "bridge-devices-color-endpoints.json")
		if len(devices) != 1 {
			t.Fatal("endpoints fixture must contain exactly one device")
		}
		device := devices[0]
		for rootIndex := range device.Definition.Exposes {
			if device.Definition.Exposes[rootIndex].Endpoint == "left" {
				for featureIndex := range device.Definition.Exposes[rootIndex].Features {
					if device.Definition.Exposes[rootIndex].Features[featureIndex].Name == "state" {
						device.Definition.Exposes[rootIndex].Features[featureIndex].Access = 3
					}
				}
			}
		}
		keys := entityKeys(contractProfileContribution(t, "light", device).Entities)
		if len(keys) == 0 || keys[0] != "power-ep2" {
			t.Fatalf("light survivor keys = %v, want power-ep2 first", keys)
		}
	})
}

// This test protects the derived color-mode dependency and fails if the
// mode companion plans without a surviving color sibling or is skipped
// despite one.
func TestProfileContractLightColorModeDependency(t *testing.T) {
	t.Parallel()
	t.Run("temperature-only plans mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 153, 500)),
		}
		requireContractLightKeys(t, "temperature-only mode", device,
			[]string{"power", "brightness", "colortemp", "colormode"})
	})
	t.Run("broken temperature omits temperature and mode", func(t *testing.T) {
		t.Parallel()
		device := eligibleDevice()
		device.Definition.Exposes = []upstreamExpose{
			colorLightExpose("", "state", "brightness", colorTempFeature("color_temp", 500, 150)),
		}
		requireContractLightKeys(t, "broken temperature mode", device,
			[]string{"power", "brightness"})
	})
	t.Run("no color plans no mode", func(t *testing.T) {
		t.Parallel()
		requireContractLightKeys(t, "no color mode", eligibleDevice(),
			[]string{"power", "brightness"})
	})
	t.Run("xy-only plans mode without temperature", func(t *testing.T) {
		t.Parallel()
		devices := fixtureDevices(t, "bridge-devices-color-xy-only.json")
		requireContractLightKeys(t, "xy-only mode", devices[0],
			[]string{"power", "brightness", "colorxy", "colormode"})
	})
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

// requireContractRelayKeys asserts that one mutated plug device produces
// exactly the expected relay contribution keys. The defect would be a relay
// profile that admits or drops a different set for that eligibility rule.
func requireContractRelayKeys(t *testing.T, name string, device upstreamDevice, wantKeys []string) {
	t.Helper()
	gotKeys := entityKeys(contractProfileContribution(t, "relay", device).Entities)
	if len(gotKeys) != len(wantKeys) {
		t.Fatalf("%s keys = %v, want %v", name, gotKeys, wantKeys)
	}
	for index := range gotKeys {
		if gotKeys[index] != wantKeys[index] {
			t.Fatalf("%s keys = %v, want %v", name, gotKeys, wantKeys)
		}
	}
}

// contractPlugMutation applies one device mutation to a fresh plug fixture
// so each case isolates exactly one eligibility dimension.
func contractPlugMutation(t *testing.T, mutate func(*upstreamDevice)) upstreamDevice {
	t.Helper()
	device := mustPlugDevice(t)
	mutate(&device)
	return device
}

// This test protects electrical, setting, and action eligibility and fails
// if any access, unit, bound, property, duplicate-root, or
// unresolved-endpoint violation registers, or if a valid sibling is
// suppressed with it. Each case mutates exactly one dimension of the
// captured plug.
func TestProfileContractRelayIsolatesMalformedSiblings(t *testing.T) {
	t.Parallel()
	t.Run("set access omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "voltage").Access = 7
		})
		requireContractRelayKeys(t, "voltage set access", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("wrong unit omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "current").Unit = "mA"
		})
		requireContractRelayKeys(t, "current unit", device, relayPlugKeysWithout("current"))
	})
	t.Run("power factor requires empty upstream unit", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "power_factor").Unit = "%"
		})
		requireContractRelayKeys(t, "power factor unit", device, relayPlugKeysWithout("powerfactor"))
	})
	t.Run("one-sided upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			maximum := 250.0
			plugExposeByName(device, "voltage").ValueMax = &maximum
		})
		requireContractRelayKeys(t, "voltage one-sided bounds", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("malformed upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			voltage := plugExposeByName(device, "voltage")
			voltage.valueMinRaw = json.RawMessage(`"low"`)
			voltage.valueMaxRaw = json.RawMessage(`"high"`)
		})
		requireContractRelayKeys(t, "voltage malformed bounds", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("inverted upstream bounds omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			minimum, maximum := 300.0, 100.0
			voltage := plugExposeByName(device, "voltage")
			voltage.ValueMin = &minimum
			voltage.ValueMax = &maximum
		})
		requireContractRelayKeys(t, "voltage inverted bounds", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("missing setting bounds omit only that setting", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "led_brightness").ValueMax = nil
		})
		requireContractRelayKeys(t, "LED bounds", device, relayPlugKeysWithout("ledbrightness"))
	})
	t.Run("wrong setting unit omits only that setting", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "led_brightness").Unit = "mired"
		})
		requireContractRelayKeys(t, "LED unit", device, relayPlugKeysWithout("ledbrightness"))
	})
	t.Run("duplicate sensor property omits only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
				Type: "numeric", Name: "diagnostic", Property: "voltage", Access: 1, Unit: "V",
			})
		})
		requireContractRelayKeys(t, "voltage duplicate property", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("duplicate sensor roots omit only that sensor", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "voltage")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireContractRelayKeys(t, "voltage duplicate roots", device, relayPlugKeysWithout("voltage"))
	})
	t.Run("duplicate behavior roots omit only behavior", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "power_on_behavior")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireContractRelayKeys(t, "behavior duplicate roots", device, relayPlugKeysWithout("poweronbehavior"))
	})
	t.Run("reset without set access is omitted", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			plugExposeByName(device, "reset_total_energy").Access = 1
		})
		requireContractRelayKeys(t, "reset access", device, relayPlugKeysWithout("resettotalenergy"))
	})
	t.Run("duplicate reset roots are omitted", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			duplicate := *plugExposeByName(device, "reset_total_energy")
			device.Definition.Exposes = append(device.Definition.Exposes, duplicate)
		})
		requireContractRelayKeys(t, "reset duplicate roots", device, relayPlugKeysWithout("resettotalenergy"))
	})
	t.Run("unresolved relay endpoint plans no relay family", func(t *testing.T) {
		t.Parallel()
		device := contractPlugMutation(t, func(device *upstreamDevice) {
			device.Definition.Exposes[0].Endpoint = "missing"
		})
		requireContractRelayKeys(t, "unresolved endpoint", device, nil)
	})
}

// This test protects discovered-scalar behavior and fails if the relay
// profile hard-codes ON/OFF instead of translating its expose values: a
// synthetic switch with custom scalars must decode and publish those
// scalars.
func TestProfileContractRelayUsesDiscoveredScalars(t *testing.T) {
	t.Parallel()
	device := evalTestSwitchDevice("Fixture", "EVAL", "", upstreamExpose{
		Type: "switch",
		Features: []upstreamExpose{{
			Type: "binary", Name: "state", Property: "state",
			Access:  exposePublishAccessBit | exposeSetAccessBit | exposeGetAccessBit,
			ValueOn: json.RawMessage(`"POWER_ON"`), ValueOff: json.RawMessage(`"POWER_OFF"`),
		}},
	})
	contribution := contractProfileContribution(t, "relay", device)
	if len(contribution.Entities) != 1 {
		t.Fatalf("custom scalars planned %d entities, want 1", len(contribution.Entities))
	}
	power := contribution.Entities[0]
	if report := contractDecode(t, power, "state", `"POWER_ON"`); report.semantic != true {
		t.Fatalf("custom ON decoded to %v, want true", report.semantic)
	}
	payload, planned := contractTranslate(t, power, "set", `{"value":true}`)
	if string(payload) != `{"state":"POWER_ON"}` {
		t.Fatalf("custom ON payload = %s, want the discovered scalar", payload)
	}
	if !planned.Matches(stateReport{semantic: true}) || planned.Matches(stateReport{semantic: false}) {
		t.Fatal("custom scalar matcher did not enforce the commanded value")
	}
}

// This test protects valid upstream bound preference and fails if the
// profile ignores both present valid finite upstream bounds: voltage with
// 100-250 bounds must carry that support.
func TestProfileContractRelayPrefersValidUpstreamBounds(t *testing.T) {
	t.Parallel()
	device := contractPlugMutation(t, func(device *upstreamDevice) {
		minimum, maximum := 100.0, 250.0
		voltage := plugExposeByName(device, "voltage")
		voltage.ValueMin = &minimum
		voltage.ValueMax = &maximum
	})
	contribution := contractProfileContribution(t, "relay", device)
	const expectedVoltageSupport = `{"state":{"maximum":250,"minimum":100,"unit":"V"},"operations":{}}`
	found := false
	for _, plan := range contribution.Entities {
		if plan.Descriptor.Key == "voltage" {
			found = true
			if string(plan.Descriptor.Support) != expectedVoltageSupport {
				t.Fatalf("voltage support = %s, want the upstream bounds", plan.Descriptor.Support)
			}
		}
	}
	if !found {
		t.Fatal("voltage entity missing with valid upstream bounds")
	}
}

// This test protects tolerant top-level software build id decoding and fails
// if any JSON shape rejects the device: absent, null, and non-string values
// decode as absent while strings are retained for override selection.
func TestProfileContractDecodesSoftwareBuildIDTolerantly(t *testing.T) {
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
func TestProfileContractSoftwareBuildIDNeverEntersIdentity(t *testing.T) {
	t.Parallel()
	catalog := mustEmbeddedProfileCatalog(t)
	withoutBuild := eligibleSensorDevice("temperature",
		exposePublishAccessBit|exposeGetAccessBit)
	withBuild := withoutBuild
	withBuild.SoftwareBuildID = "1.01.01"
	want, wantRejection := discoverDevice(withoutBuild, catalog)
	got, gotRejection := discoverDevice(withBuild, catalog)
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
		if !reflect.DeepEqual(want.Entities[index].Descriptor, got.Entities[index].Descriptor) {
			t.Fatalf("entity %d descriptor diverged with firmware evidence", index)
		}
	}
	input := profilePlanningInput(withBuild, withBuild.IEEEAddress)
	if input.Vendor != "Fixture" || input.Model != "TEST" || input.SoftwareBuildID != "1.01.01" {
		t.Fatalf("evaluator input = %+v, want vendor/model/build evidence carried", input)
	}
}
