package zigbee2mqtt //nolint:testpackage // Tests exercise package-private wire DTOs and discovery routes.

import (
	"encoding/json"
	"os"
)

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

func colorTempFeature(property string, minimum, maximum float64) upstreamExpose {
	return upstreamExpose{
		Type: "numeric", Name: upstreamColorTempName, Property: property, Access: 7,
		ValueMin: &minimum, ValueMax: &maximum,
	}
}

func colorAxisChild(name string, minimum, maximum *float64) upstreamExpose {
	return upstreamExpose{
		Type: "numeric", Name: name, Property: name, Access: 7,
		ValueMin: minimum, ValueMax: maximum,
	}
}

func colorXYComposite(property string, children ...upstreamExpose) upstreamExpose {
	return upstreamExpose{
		Type: upstreamExposeComposite, Name: upstreamColorXYName, Property: property, Access: 7,
		Features: children,
	}
}

func colorHSComposite(property string, children ...upstreamExpose) upstreamExpose {
	return upstreamExpose{
		Type: upstreamExposeComposite, Name: upstreamColorHSName, Property: property, Access: 7,
		Features: children,
	}
}

// colorLightExpose builds one light root with power, optional brightness,
// and additional color or temperature features.
func colorLightExpose(endpoint, stateProperty, brightnessProperty string, extra ...upstreamExpose) upstreamExpose {
	minimum, maximum, step := 0.0, 255.0, 1.0
	features := []upstreamExpose{
		{
			Type: "binary", Name: "state", Property: stateProperty, Access: 7,
			ValueOn: json.RawMessage(`"ON"`), ValueOff: json.RawMessage(`"OFF"`),
		},
	}
	if brightnessProperty != "" {
		features = append(features, upstreamExpose{
			Type: "numeric", Name: "brightness", Property: brightnessProperty, Access: 7,
			ValueMin: &minimum, ValueMax: &maximum, ValueStep: &step,
		})
	}
	return upstreamExpose{
		Type: "light", Endpoint: endpoint,
		Features: append(features, extra...),
	}
}

func entityKeys(entities []entityPlan) []string {
	keys := make([]string, 0, len(entities))
	for _, entity := range entities {
		keys = append(keys, entity.Descriptor.Key)
	}
	return keys
}

func bindPlans(plans []entityPlan) []runtimeEntity {
	entities := make([]runtimeEntity, 0, len(plans))
	for _, plan := range plans {
		entities = append(entities, runtimeEntity{plan: plan, entityID: "entity-" + plan.Descriptor.Key})
	}
	return entities
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

func mustDiscoveredFixtureDevice(testingT interface {
	Helper()
	Fatal(...any)
}, fixture string) discoveredDevice {
	testingT.Helper()
	result, err := discoverInventory(readFixture(testingT, fixture))
	if err != nil {
		testingT.Fatal(err)
	}
	if len(result.Devices) != 1 {
		testingT.Fatal("fixture did not discover exactly one Device")
	}
	return result.Devices[0]
}
