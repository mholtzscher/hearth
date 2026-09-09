package zigbee2mqtt //nolint:testpackage // Light boundary tests exercise plan-level SDK validation.

import (
	"context"
	"reflect"
	"testing"
	"time"
)

// This test protects Hearth-range command rejection through the translator:
// out-of-range brightness percentages fail in the SDK command handler before
// any MQTT publication.
func TestLightBrightnessCommandRejectsOutOfRangeBeforePublish(t *testing.T) {
	t.Parallel()
	for _, parameters := range []string{`{"value":101}`, `{"value":-1}`, `{"value":100.5}`} {
		t.Run(parameters, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, _, connection, device := commandReadyAdapterFor(
				t,
				recorder,
				session,
				"bridge-devices-color-dual.json",
			)
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(entityByKey(device, "brightness").entityID, parameters),
				newFakeResponder(recorder, session),
			); err == nil {
				t.Fatal("out-of-range brightness command was accepted")
			}
			connection.mutex.Lock()
			defer connection.mutex.Unlock()
			if len(connection.published) != 0 {
				t.Fatalf("invalid brightness command reached MQTT: %#v", connection.published)
			}
		})
	}
}

// This test protects discovered-range command rejection through the
// translator: values outside the discovered color-temperature support fail in
// the SDK command handler before any MQTT publication, including a
// schema-valid value below the discovered minimum.
func TestLightColorTempCommandRejectsOutOfRangeBeforePublish(t *testing.T) {
	t.Parallel()
	for _, parameters := range []string{`{"value":100}`, `{"value":600}`, `{"value":370.5}`} {
		t.Run(parameters, func(t *testing.T) {
			t.Parallel()
			recorder := &runtimeRecorder{}
			session := newFakeSession(recorder)
			z2m, _, connection, device := commandReadyAdapterFor(
				t,
				recorder,
				session,
				"bridge-devices-color-dual.json",
			)
			if err := z2m.HandleCommand(
				context.Background(),
				testCommand(entityByKey(device, "colortemp").entityID, parameters),
				newFakeResponder(recorder, session),
			); err == nil {
				t.Fatal("out-of-range color-temperature command was accepted")
			}
			connection.mutex.Lock()
			defer connection.mutex.Unlock()
			if len(connection.published) != 0 {
				t.Fatalf("invalid color-temperature command reached MQTT: %#v", connection.published)
			}
		})
	}
}

// This test protects plan-boundary State rejection for values the decoder
// accepts but the discovered support forbids: schema-valid temperatures below
// the discovered minimum or above the discovered maximum issue only the
// temperature Entity while power, brightness, and mode siblings survive.
func TestLightColorTempStateBoundaryRejectsWithSiblingsSurviving(t *testing.T) {
	t.Parallel()
	device := mustDiscoveredFixtureDevice(t, "bridge-devices-color-dual.json")
	entities := bindPlans(device.Entities)
	receivedAt := time.Unix(1, 0).UTC()
	for _, value := range []string{`100`, `600`} {
		states, issues, err := decodeDeviceState(
			[]byte(`{"state":"ON","brightness":127,"color_temp":`+value+`,"color_mode":"color_temp"}`),
			entities,
			receivedAt,
		)
		if err != nil {
			t.Fatalf("color_temp %s: %v", value, err)
		}
		if len(states) != 3 || len(issues) != 1 ||
			states[0].entityID != "entity-power" || string(states[0].report.Observation.Value) != "true" ||
			states[1].entityID != "entity-brightness" ||
			string(states[1].report.Observation.Value) != "50" ||
			states[2].entityID != "entity-colormode" ||
			string(states[2].report.Observation.Value) != `"color_temp"` ||
			!reflect.DeepEqual(issues[0].Properties, []string{"color_temp", "color_mode"}) {
			t.Fatalf("color_temp %s: states=%#v issues=%#v", value, states, issues)
		}
	}
}

// This test protects descriptor-path omission for out-of-envelope discovered
// bounds: the 100..1000 envelope lives in the colortemp descriptor support
// codec, so an exact but out-of-envelope range omits only the temperature
// Entity while power and brightness survive.
func TestLightColorTempOuterBoundsOmittedViaDescriptor(t *testing.T) {
	t.Parallel()
	for _, bounds := range [][2]float64{{99, 500}, {153, 1001}, {100, 1000}} {
		t.Run(jsonNumber(int(bounds[0]))+"-"+jsonNumber(int(bounds[1])), func(t *testing.T) {
			t.Parallel()
			device := eligibleDevice()
			device.Definition.Exposes[0].Features = append(
				device.Definition.Exposes[0].Features,
				colorTempFeature("color_temp", bounds[0], bounds[1]),
			)
			discovered, rejection := discoverDevice(device)
			if rejection != nil {
				t.Fatalf("Device rejected: %#v", rejection)
			}
			// 100..1000 is exactly the envelope edge and stays plannable;
			// anything outside omits only the temperature candidate.
			want := []string{"power", "brightness"}
			if bounds == [2]float64{100, 1000} {
				want = []string{"power", "brightness", "colortemp", "colormode"}
			}
			if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, want) {
				t.Fatalf("Entity keys for bounds %v = %v, want %v", bounds, got, want)
			}
		})
	}
}
