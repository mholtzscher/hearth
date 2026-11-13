package esphome //nolint:testpackage // Planning is an unexported pure function.

import (
	"encoding/json"
	"errors"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/adapters/esphome/nativeapi"
	"github.com/mholtzscher/hearth/sdk/adapter"
)

var registrationKeyPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{0,62}$`)

func TestPlanIdenticalDevicesScopeExternalIDsWithoutChangingEntityKeys(t *testing.T) {
	t.Parallel()
	fixture := loadFixture(t, "apollo-air1.json")
	firstMAC := MAC{2, 0, 0, 0, 0, 1}
	secondMAC := MAC{2, 0, 0, 0, 0, 2}
	first, err := planDevice(firstMAC, fixture.Info, fixture.Entities, Exposure{})
	if err != nil {
		t.Fatal(err)
	}
	second, err := planDevice(secondMAC, fixture.Info, fixture.Entities, Exposure{})
	if err != nil {
		t.Fatal(err)
	}
	if first.Registration.BindingKey == second.Registration.BindingKey ||
		*first.Registration.Device.ExternalID == *second.Registration.Device.ExternalID {
		t.Fatal("identical Devices share a Binding or Device external ID")
	}
	if len(first.Registration.Entities) != 21 || len(second.Registration.Entities) != 21 {
		t.Fatal("both Apollo plans must contain twenty-one Entities")
	}
	for index, entity := range first.Registration.Entities {
		other := second.Registration.Entities[index]
		if entity.ExternalID == other.ExternalID {
			t.Fatalf("two physical Devices share Entity external ID %q", entity.ExternalID)
		}
		identity, scoped := strings.CutPrefix(entity.ExternalID, firstMAC.String()+"/")
		if !scoped || other.ExternalID != secondMAC.String()+"/"+identity {
			t.Fatalf("external IDs are not MAC-scoped: %q, %q", entity.ExternalID, other.ExternalID)
		}
		if entity.Key != other.Key {
			t.Fatalf("device-scoping changed Binding-local Entity keys: %q, %q", entity.Key, other.Key)
		}
	}
	for identity, want := range map[string]string{
		"0/sensor/CO2":                 "co2-23443a6e",
		"0/light/RGB Light#power":      "rgb-light-d64ac854",
		"0/light/RGB Light#brightness": "rgb-light-brightness-cd5fa35a",
	} {
		if got := descriptorByExternalID(t, first, firstMAC.String()+"/"+identity).Key; got != want {
			t.Errorf("legacy Entity key for %q changed: got %q, want %q", identity, got, want)
		}
	}
}

func externalIDs(plan devicePlan) []string {
	ids := make([]string, 0, len(plan.Registration.Entities))
	for _, entity := range plan.Registration.Entities {
		ids = append(ids, entity.ExternalID)
	}
	return ids
}

func descriptorByExternalID(t *testing.T, plan devicePlan, externalID string) adapter.EntityDescriptor {
	t.Helper()
	for _, entity := range plan.Registration.Entities {
		if entity.ExternalID == externalID {
			return entity
		}
	}
	t.Fatalf("no planned Entity %q in %v", externalID, externalIDs(plan))
	return adapter.EntityDescriptor{}
}

//nolint:gocognit // This cohesive scenario verifies the complete ordered behavior.
func TestPlanDeviceMapsOnlyPrimaryEntitiesByDefault(t *testing.T) {
	t.Parallel()
	cases := map[string][]string{
		"apollo-temp1.json": {
			"0/sensor/Temperature Probe", "0/sensor/Board Temperature", "0/sensor/Board Humidity",
			"0/sensor/Food Probe", "0/light/RGB Light#power", "0/light/RGB Light#brightness", "0/light/RGB Light#color",
			"0/switch/Accessory Power#power", "0/switch/Alarm Outside Temp Range#power",
		},
		"apollo-air1.json": {
			"0/sensor/CO2",
			"0/sensor/DPS310 Pressure",
			"0/sensor/PM <1µm Weight concentration",
			"0/sensor/PM <2.5µm Weight concentration",
			"0/sensor/PM <4µm Weight concentration",
			"0/sensor/PM <10µm Weight concentration",
			"0/sensor/SEN55 Temperature",
			"0/sensor/SEN55 Humidity",
			"0/sensor/SEN55 VOC",
			"0/sensor/SEN55 NOX",
			"0/sensor/Nitrogen Dioxide",
			"0/sensor/Carbon Monoxide",
			"0/sensor/Hydrogen",
			"0/sensor/Ethanol",
			"0/sensor/Methane",
			"0/sensor/Ammonia",
			"0/light/RGB Light#power",
			"0/light/RGB Light#brightness",
			"0/light/RGB Light#color",
			"0/light/RGB Light#effect",
			"0/text_sensor/VOC Quality",
		},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			fixture := loadFixture(t, name)
			plan := mustPlan(t, fixture, Exposure{})
			for index, identity := range want {
				want[index] = fixtureMAC(t, fixture).String() + "/" + identity
			}
			if got := externalIDs(plan); !slices.Equal(got, want) {
				t.Fatalf("planned = %q\nwant %q", got, want)
			}
			if plan.Registration.Device.Kind != deviceKindSensor {
				t.Errorf("device kind = %s", plan.Registration.Device.Kind)
			}
			if plan.Registration.BindingKey != fixtureMAC(t, fixture).BindingKey() {
				t.Errorf("binding key = %s", plan.Registration.BindingKey)
			}
			if *plan.Registration.Device.ExternalID != fixtureMAC(t, fixture).String() {
				t.Errorf("device external ID = %s", *plan.Registration.Device.ExternalID)
			}
			if plan.Registration.Device.Name != fixture.Info.FriendlyName {
				t.Errorf("device name = %s", plan.Registration.Device.Name)
			}
			for _, skipped := range plan.Skipped {
				if skipped.Reason == "" {
					t.Errorf("skip without reason: %+v", skipped)
				}
			}
			if len(plan.Skipped)+len(plan.Routes)-extraLightRoles(fixture) != len(fixture.Entities) {
				t.Errorf("skipped %d + routes %d do not account for %d entities",
					len(plan.Skipped), len(plan.Routes), len(fixture.Entities))
			}
		})
	}
}

func extraLightRoles(fixture deviceFixture) int {
	count := 0
	for _, entity := range fixture.Entities {
		if entity.Type == nativeapi.EntityLight {
			if supportsBrightness(entity.ColorModes) {
				count++
			}
			if supportsRGB(entity.ColorModes) {
				count++
			}
			if len(entity.Effects) > 0 {
				count++
			}
		}
	}
	return count
}

func TestPlanDeviceSkipReasons(t *testing.T) {
	t.Parallel()
	plan := mustPlan(t, loadFixture(t, "apollo-air1.json"), Exposure{})
	reasons := map[string]string{}
	for _, skipped := range plan.Skipped {
		reasons[skipped.Ref.String()] = skipped.Reason
	}
	want := map[string]string{
		"sensor/Uptime":                  skipExposureDefault,
		"sensor/PM 0.3 To 1 µm":          skipExposureDefault,
		"switch/Prevent Sleep":           skipExposureDefault,
		"other/Clean SEN55":              skipTypeUnsupported,
		"binary_sensor/Online":           skipExposureDefault,
		"other/SEN55 Temperature Offset": skipTypeUnsupported,
	}
	for ref, reason := range want {
		if reasons[ref] != reason {
			t.Errorf("%s skipped as %q, want %q", ref, reasons[ref], reason)
		}
	}
}

func TestPlanDeviceAppliesOverrides(t *testing.T) {
	t.Parallel()
	fixture := loadFixture(t, "apollo-air1.json")
	plan := mustPlan(t, fixture, Exposure{
		Include: refs(t, "sensor/Uptime", "text_sensor/IP Address", "sensor/Not On This Device"),
		Exclude: refs(t, "light/RGB Light"),
	})
	ids := externalIDs(plan)
	if !slices.Contains(ids, fixtureMAC(t, fixture).String()+"/0/sensor/Uptime") {
		t.Error("include did not add the diagnostic uptime sensor")
	}
	if !slices.Contains(ids, fixtureMAC(t, fixture).String()+"/0/text_sensor/IP Address") {
		t.Error("include did not add the diagnostic text sensor")
	}
	for _, id := range ids {
		if strings.HasPrefix(id, fixtureMAC(t, fixture).String()+"/0/light/RGB Light#") {
			t.Errorf("exclude left %s", id)
		}
	}
	defaults := externalIDs(mustPlan(t, fixture, Exposure{}))
	if len(ids) != len(defaults)-4+2 {
		t.Errorf("override plan has %d Entities; default has %d", len(ids), len(defaults))
	}
	if len(plan.UnmatchedOverrides) != 1 || plan.UnmatchedOverrides[0].Name != "Not On This Device" {
		t.Errorf("unmatched = %v", plan.UnmatchedOverrides)
	}
}

func numericSupportOf(t *testing.T, descriptor adapter.EntityDescriptor) map[string]any {
	t.Helper()
	var support struct {
		State map[string]any `json:"state"`
	}
	if err := json.Unmarshal(descriptor.Support, &support); err != nil {
		t.Fatal(err)
	}
	return support.State
}

func TestPlanDeviceTypesAirQualityAndGenericSensors(t *testing.T) {
	t.Parallel()
	plan := mustPlan(t, loadFixture(t, "apollo-air1.json"), Exposure{})
	cases := []struct {
		externalID string
		kind       any
		unit       string
		minimum    float64
		maximum    float64
	}{
		{"0/sensor/CO2", "carbon_dioxide", "ppm", 0, 40000},
		{"0/sensor/PM <2.5µm Weight concentration", "particulate_matter", "µg/m³", 0, 1000},
		{"0/sensor/SEN55 VOC", "air_quality_index", "index", 0, 500},
		{"0/sensor/PM <4µm Weight concentration", nil, "µg/m³", -1e12, 1e12},
		{"0/sensor/Ammonia", nil, "ppm", -1e12, 1e12},
	}
	for _, test := range cases {
		descriptor := descriptorByExternalID(t, plan, "02:00:00:00:00:02/"+test.externalID)
		if descriptor.Type != "hearth.numericsensor/v1" {
			t.Errorf("%s type = %s", test.externalID, descriptor.Type)
			continue
		}
		state := numericSupportOf(t, descriptor)
		if state["quantity_kind"] != test.kind || state["unit"] != test.unit ||
			state["minimum"] != test.minimum || state["maximum"] != test.maximum {
			t.Errorf("%s support = %v", test.externalID, state)
		}
	}
	for externalID, wantType := range map[string]string{
		"0/sensor/SEN55 Temperature":   "hearth.temperature/v1",
		"0/sensor/SEN55 Humidity":      "hearth.relativehumidity/v1",
		"0/sensor/DPS310 Pressure":     "hearth.pressure/v1",
		"0/light/RGB Light#power":      "hearth.power/v1",
		"0/light/RGB Light#brightness": "hearth.brightness/v1",
	} {
		if got := descriptorByExternalID(t, plan, "02:00:00:00:00:02/"+externalID).Type; got != wantType {
			t.Errorf("%s type = %s, want %s", externalID, got, wantType)
		}
	}
}

func TestPlanDeviceSkipsSubDevicesAssumedStateAndLongUnits(t *testing.T) {
	t.Parallel()
	entities := []nativeapi.EntityInfo{
		{Type: nativeapi.EntitySwitch, UpstreamType: "switch", Key: 1, Name: "Relay", AssumedState: true},
		{
			Type:         nativeapi.EntitySensor,
			UpstreamType: "sensor",
			Key:          2,
			DeviceID:     7,
			Name:         "Child",
			Unit:         "°C",
			DeviceClass:  "temperature",
		},
		{
			Type:         nativeapi.EntitySensor,
			UpstreamType: "sensor",
			Key:          3,
			Name:         "Odd",
			Unit:         "units-of-something-very-long-indeed",
		},
		{
			Type:         nativeapi.EntityBinarySensor,
			UpstreamType: "binary_sensor",
			Key:          4,
			Name:         "Motion",
			DeviceClass:  "motion",
		},
	}
	plan, err := planDevice(MAC{2, 0, 0, 0, 0, 9}, nativeapi.DeviceInfo{Name: "node"}, entities, Exposure{})
	if err != nil {
		t.Fatal(err)
	}
	if got := externalIDs(plan); !slices.Equal(got, []string{"02:00:00:00:00:09/0/binary_sensor/Motion"}) {
		t.Fatalf("planned = %q", got)
	}
	reasons := []string{}
	for _, skipped := range plan.Skipped {
		reasons = append(reasons, skipped.Reason)
	}
	if !slices.Equal(reasons, []string{skipAssumedState, skipSubDevice, skipUnitUnsupported}) {
		t.Fatalf("reasons = %v", reasons)
	}
	if plan.Registration.Device.Name != "node" || plan.Registration.Device.Kind != deviceKindSensor {
		t.Fatalf("device = %+v", plan.Registration.Device)
	}
}

func TestPlanDeviceKind(t *testing.T) {
	t.Parallel()
	light := nativeapi.EntityInfo{Type: nativeapi.EntityLight, Key: 1, Name: "Lamp", ColorModes: []uint32{3}}
	onOffLight := nativeapi.EntityInfo{Type: nativeapi.EntityLight, Key: 2, Name: "Lamp", ColorModes: []uint32{1}}
	relay := nativeapi.EntityInfo{Type: nativeapi.EntitySwitch, Key: 3, Name: "Relay"}
	for want, entities := range map[string][]nativeapi.EntityInfo{
		deviceKindLight: {light},
		deviceKindRelay: {onOffLight, relay},
	} {
		plan, err := planDevice(MAC{2}, nativeapi.DeviceInfo{}, entities, Exposure{})
		if err != nil {
			t.Fatal(err)
		}
		if plan.Registration.Device.Kind != want {
			t.Errorf("kind = %s, want %s", plan.Registration.Device.Kind, want)
		}
		if want == deviceKindRelay && len(plan.Registration.Entities) != 2 {
			t.Errorf("on/off light planned brightness: %v", externalIDs(plan))
		}
	}
}

func TestPlanDeviceRejectsUnsupportedDevices(t *testing.T) {
	t.Parallel()
	many := make([]nativeapi.EntityInfo, 0, 65)
	for index := range 65 {
		many = append(many, nativeapi.EntityInfo{
			Type: nativeapi.EntityBinarySensor,
			Key:  uint32(index),
			Name: "Sensor " + string(rune('A'+index%26)) + string(rune('a'+index/26)),
		})
	}
	cases := map[string]struct {
		info     nativeapi.DeviceInfo
		entities []nativeapi.EntityInfo
		reason   string
	}{
		"deep sleep":        {nativeapi.DeviceInfo{HasDeepSleep: true}, nil, reasonDeepSleepUnsupported},
		"too many entities": {nativeapi.DeviceInfo{}, many, reasonPlanningFailed},
	}
	for name, test := range cases {
		_, err := planDevice(MAC{2}, test.info, test.entities, Exposure{})
		var unsupported *unsupportedDeviceError
		if !errors.As(err, &unsupported) || unsupported.Reason != test.reason {
			t.Errorf("%s: error = %v", name, err)
		}
	}
}

func TestEntityKeysDependOnlyOnOwnIdentity(t *testing.T) {
	t.Parallel()
	fixture := loadFixture(t, "apollo-air1.json")
	full := mustPlan(t, fixture, Exposure{})
	keys := map[string]string{}
	for _, entity := range full.Registration.Entities {
		if !registrationKeyPattern.MatchString(entity.Key) {
			t.Errorf("key %q is not a registration slug", entity.Key)
		}
		keys[entity.ExternalID] = entity.Key
	}
	if len(keys) != len(full.Registration.Entities) {
		t.Fatal("duplicate external IDs")
	}
	reduced := fixture
	reduced.Entities = slices.DeleteFunc(slices.Clone(fixture.Entities), func(entity nativeapi.EntityInfo) bool {
		return entity.Name == "CO2" || entity.Name == "Methane"
	})
	reduced.Entities = append(reduced.Entities, nativeapi.EntityInfo{
		Type: nativeapi.EntitySensor, Key: 999, Name: "Radon", DeviceClass: "radon", Unit: "Bq/m³",
	})
	for _, entity := range mustPlan(t, reduced, Exposure{}).Registration.Entities {
		if previous, known := keys[entity.ExternalID]; known && previous != entity.Key {
			t.Errorf("%s key moved from %s to %s", entity.ExternalID, previous, entity.Key)
		}
	}
	if keys["02:00:00:00:00:02/0/light/RGB Light#power"] == keys["02:00:00:00:00:02/0/light/RGB Light#brightness"] {
		t.Error("light Entities share a key")
	}
}
