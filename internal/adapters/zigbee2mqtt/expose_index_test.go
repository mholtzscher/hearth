package zigbee2mqtt //nolint:testpackage // Tests exercise package-private expose normalization.

import (
	"encoding/json"
	"reflect"
	"testing"
)

// This test protects Device-wide property ambiguity and fails if two plans can
// claim one MQTT property or if an unsupported expose escapes the uniqueness
// count.
func TestExposeIndexCountsPropertiesDeviceWide(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Definition.Exposes = append(device.Definition.Exposes, upstreamExpose{
		Type: "numeric", Name: "diagnostic", Property: "brightness",
	})
	index := newExposeIndex(device)
	if index.PropertyUnique("brightness") {
		t.Fatal("diagnostic expose did not break brightness property uniqueness")
	}
	if !index.PropertyUnique("state") {
		t.Fatal("uncontested State property was not unique")
	}
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power"}) {
		t.Fatalf("Entity keys = %v, want power only", got)
	}
}

// This test protects root ordering and unresolved-endpoint isolation. It fails
// if inventory order is lost or if one unresolvable endpoint suppresses an
// independent valid root.
func TestExposeIndexRootsPreserveOrderAndResolution(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	device.Endpoints = map[string]upstreamEndpoint{"1": {Name: "left"}}
	device.Definition.Exposes = []upstreamExpose{
		lightExpose("missing", "state_missing", "brightness_missing"),
		lightExpose("left", "state_left", "brightness_left"),
	}
	index := newExposeIndex(device)
	roots := index.Roots("light")
	if len(roots) != 2 || roots[0].order != 0 || roots[1].order != 1 {
		t.Fatalf("root order = %#v", roots)
	}
	if roots[0].resolved || !roots[0].scoped {
		t.Fatalf("unresolved root = %#v, want scoped and unresolved", roots[0])
	}
	if !roots[1].resolved || roots[1].endpoint != 1 {
		t.Fatalf("valid root = %#v, want endpoint 1", roots[1])
	}
	discovered, rejection := discoverDevice(device)
	if rejection != nil {
		t.Fatalf("Device rejected: %#v", rejection)
	}
	if got := entityKeys(discovered.Entities); !reflect.DeepEqual(got, []string{"power-ep1", "brightness-ep1"}) {
		t.Fatalf("Entity keys = %v", got)
	}
}

// This test protects feature unambiguity and fails if duplicated nested
// features can both be selected.
func TestExposeIndexUniqueFeatureRequiresOneMatch(t *testing.T) {
	t.Parallel()
	device := eligibleDevice()
	index := newExposeIndex(device)
	roots := index.Roots("light")
	if len(roots) != 1 {
		t.Fatalf("roots = %#v", roots)
	}
	if _, ok := index.UniqueFeature(roots[0], featureQuery{Type: "binary", Name: "state"}); !ok {
		t.Fatal("single power feature was not unique")
	}
	duplicated := roots[0]
	duplicated.expose.Features = append(duplicated.expose.Features, duplicated.expose.Features[0])
	if _, ok := index.UniqueFeature(duplicated, featureQuery{Type: "binary", Name: "state"}); ok {
		t.Fatal("duplicated power feature was accepted as unique")
	}
	if _, ok := index.UniqueFeature(roots[0], featureQuery{Type: "binary", Name: "missing"}); ok {
		t.Fatal("absent feature was accepted as unique")
	}
}

// This test protects tolerant unit decoding and fails if a malformed unit
// discards sibling exposes or a valid unit is lost.
func TestExposeIndexRetainsUnitIndependently(t *testing.T) {
	t.Parallel()
	payload := []byte(`[{
		"ieee_address": "0x00124b0024abcdef", "type": "Router", "supported": true,
		"friendly_name": "test-sensor", "interview_state": "SUCCESSFUL",
		"endpoints": {},
		"definition": {"model": "TEST", "vendor": "Fixture", "description": "Fixture", "exposes": [
			{"type": "numeric", "name": "temperature", "property": "temperature", "access": 1, "unit": "°C"},
			{"type": "numeric", "name": "humidity", "property": "humidity", "access": 1, "unit": 7}
		]}
	}]`)
	result, err := discoverInventory(payload)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Devices) != 1 || len(result.Rejections) != 0 {
		t.Fatalf("discovery = %#v", result)
	}
	index := newExposeIndex(eligibleSensorDevice("temperature", 1))
	for _, root := range index.roots {
		if root.expose.Name == "temperature" && root.expose.Unit != "°C" {
			t.Fatalf("temperature unit = %q", root.expose.Unit)
		}
	}
	var decoded []upstreamDevice
	if unmarshalErr := json.Unmarshal(payload, &decoded); unmarshalErr != nil {
		t.Fatal(unmarshalErr)
	}
	units := make(map[string]string)
	for _, expose := range decoded[0].Definition.Exposes {
		units[expose.Name] = expose.Unit
	}
	if units["temperature"] != "°C" || units["humidity"] != "" {
		t.Fatalf("units = %#v", units)
	}
}
