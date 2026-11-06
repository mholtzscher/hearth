package simulator_test

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/adapters/scripted"
)

func TestDashboardCatalogExceedsOneEntityPage(t *testing.T) {
	t.Parallel()
	config := loadValidationSimulatorExample(t)
	devices, entities := 0, 0
	for _, entry := range config.Adapters {
		seen := make(map[string]bool)
		for _, device := range entry.Devices {
			if seen[device.BindingKey] {
				t.Errorf("Adapter %s repeats Device identity %s", entry.AdapterID, device.BindingKey)
			}
			seen[device.BindingKey] = true
			devices++
			entities += len(device.Entities)
		}
	}
	if devices < 25 || devices > 30 || entities < 80 || entities > 120 {
		t.Fatalf(
			"dashboard inventory = %d Devices, %d Entities; want 25-30 Devices and 80-120 Entities",
			devices,
			entities,
		)
	}
}

func TestDashboardCatalogHasEightComparableRoomSeries(t *testing.T) {
	t.Parallel()
	devices := loadValidationSimulatorExample(t).Adapters[0].Devices
	for _, key := range []string{
		"living-room-sensor", "kitchen-sensor", "office-sensor", "bedroom-sensor",
		"nursery-sensor", "garage-sensor", "outdoor-sensor", "freezer-sensor",
	} {
		entity := dashboardExampleEntity(t, devices, key, "temperature")
		if entity.Type != "hearth.temperature/v1" || entity.Outputs == nil || len(entity.Outputs.Values) < 2 {
			t.Fatalf("%s must provide a looping temperature series: %+v", key, entity)
		}
		if entity.Outputs.Interval <= 0 || time.Duration(entity.Outputs.Interval) > 30*time.Second {
			t.Errorf("%s temperature does not update within 30 seconds", key)
		}
	}
	freezer := dashboardExampleEntity(t, devices, "freezer-sensor", "temperature")
	assertDashboardJSON(t, freezer.Initial, "-18000")
}

func TestDashboardCatalogLightCapabilitySubsets(t *testing.T) {
	t.Parallel()
	devices := loadValidationSimulatorExample(t).Adapters[0].Devices
	for _, test := range []struct {
		key      string
		entities []string
	}{
		{"hallway-light", []string{"power"}},
		{"bedside-light", []string{"power", "brightness"}},
		{"kitchen-white-light", []string{"power", "brightness", "colortemp"}},
		{"living-room-color-light", []string{"power", "brightness", "colorhs", "colortemp", "colormode"}},
	} {
		device := dashboardExampleDevice(t, devices, test.key)
		var keys []string
		for _, entity := range device.Entities {
			keys = append(keys, entity.Key)
			if (entity.Key == "power" || entity.Key == "brightness") && entity.Outputs != nil {
				t.Errorf("%s/%s overwrites manual Commands with scripted output", test.key, entity.Key)
			}
		}
		if device.Kind != "light" || !reflect.DeepEqual(keys, test.entities) {
			t.Errorf("%s capabilities = %s %v, want light %v", test.key, device.Kind, keys, test.entities)
		}
	}
}

func TestDashboardCatalogNumericUnitsAndBoundaries(t *testing.T) {
	t.Parallel()
	devices := loadValidationSimulatorExample(t).Adapters[0].Devices
	for _, test := range []struct {
		device, key, unit, initial string
	}{
		{"living-room-sensor", "battery", "%", "100"},
		{"outdoor-sensor", "battery", "%", "0"},
		{"kitchen-metered-plug", "consumption", "W", "0"},
		{"kitchen-metered-plug", "energy", "W.h", "1234567.8"},
		{"kitchen-metered-plug", "voltage", "V", "230.1"},
		{"kitchen-metered-plug", "current", "A", "0"},
		{"air-quality-monitor", "co2", "[ppm]", "420"},
		{"air-quality-monitor", "pm25", "ug/m3", "2.5"},
		{"weather-station", "illuminance", "lx", "0"},
		{"diagnostic-ranges", "signed", "W", "-100"},
		{"diagnostic-ranges", "tiny", "A", "0.001"},
		{"diagnostic-ranges", "large", "W.h", "987654321.5"},
	} {
		entity := dashboardExampleEntity(t, devices, test.device, test.key)
		if entity.Type != "hearth.numericsensor/v1" {
			t.Errorf("%s/%s type = %s, want numericsensor", test.device, test.key, entity.Type)
		}
		support, ok := entity.Support.(map[string]any)
		if !ok {
			t.Fatalf("%s/%s has non-object support", test.device, test.key)
		}
		state, ok := support["state"].(map[string]any)
		if !ok || state["unit"] != test.unit {
			t.Errorf("%s/%s unit = %v, want %s", test.device, test.key, state["unit"], test.unit)
		}
		assertDashboardJSON(t, entity.Initial, test.initial)
	}
}

func TestDashboardCatalogColorTemperatureStartsActive(t *testing.T) {
	t.Parallel()
	devices := loadValidationSimulatorExample(t).Adapters[0].Devices
	for _, key := range []string{"kitchen-white-light", "living-room-color-light"} {
		entity := dashboardExampleEntity(t, devices, key, "colortemp")
		state, ok := entity.Initial.(map[string]any)
		if !ok || state["active"] != true {
			t.Errorf("%s color temperature must start active so set can satisfy its outcome", key)
		}
	}
}

func TestDashboardCatalogDiagnosticOutputPatterns(t *testing.T) {
	t.Parallel()
	devices := loadValidationSimulatorExample(t).Adapters[0].Devices
	for _, test := range []struct {
		device, key, values string
		interval            time.Duration
	}{
		{"diagnostic-patterns", "repeated", "[42,42]", 5 * time.Second},
		{"diagnostic-patterns", "rounded", "[21.501,21.502,21.503]", 5 * time.Second},
		{"diagnostic-patterns", "slow", "[0,25,50,75,100]", 2 * time.Minute},
		{"diagnostic-patterns", "rapid", "[0,0,100,100,0]", 2 * time.Second},
		{"diagnostic-ranges", "signed", "[-100,-0.5,0,0.5,100]", 5 * time.Second},
		{"diagnostic-ranges", "spike", "[10,10,10,1000,10,10]", 5 * time.Second},
	} {
		entity := dashboardExampleEntity(t, devices, test.device, test.key)
		if entity.Outputs == nil || time.Duration(entity.Outputs.Interval) != test.interval {
			t.Fatalf("%s/%s must report every %s", test.device, test.key, test.interval)
		}
		assertDashboardJSON(t, entity.Outputs.Values, test.values)
	}
	constant := dashboardExampleEntity(t, devices, "diagnostic-patterns", "constant")
	if constant.Outputs != nil {
		t.Fatal("constant sensor must publish once, not repeat")
	}
	assertDashboardJSON(t, constant.Initial, "42")
	choice := dashboardExampleEntity(t, devices, "office-metered-plug", "countdown")
	assertDashboardJSON(t, choice.Initial, `{"choice":"disabled","mode":"choice"}`)
	for _, key := range []string{"kitchen-metered-plug", "office-metered-plug"} {
		if dashboardExampleEntity(t, devices, key, "energy").Outputs != nil {
			t.Errorf("%s cumulative energy must not loop back to a smaller total", key)
		}
	}
}

func TestDashboardCatalogHasUnavailableNumericEntity(t *testing.T) {
	t.Parallel()
	config := loadValidationSimulatorExample(t)
	entity := dashboardExampleEntity(t, config.Adapters[4].Devices, "simulated-unavailable-light", "temperature")
	if entity.Type != "hearth.temperature/v1" || entity.Available == nil || *entity.Available ||
		entity.AvailabilityReason == "" {
		t.Fatalf("numeric unavailable fixture is not unavailable: %+v", entity)
	}
}

func dashboardExampleDevice(t *testing.T, devices []scripted.DeviceSpec, key string) scripted.DeviceSpec {
	t.Helper()
	for _, device := range devices {
		if device.BindingKey == key {
			return device
		}
	}
	t.Fatalf("dashboard example missing Device %s", key)
	return scripted.DeviceSpec{}
}

func dashboardExampleEntity(t *testing.T, devices []scripted.DeviceSpec, deviceKey, key string) scripted.EntitySpec {
	t.Helper()
	for _, entity := range dashboardExampleDevice(t, devices, deviceKey).Entities {
		if entity.Key == key {
			return entity
		}
	}
	t.Fatalf("dashboard example missing Entity %s/%s", deviceKey, key)
	return scripted.EntitySpec{}
}

func assertDashboardJSON(t *testing.T, value any, want string) {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != want {
		t.Errorf("dashboard fixture = %s, want %s", encoded, want)
	}
}
