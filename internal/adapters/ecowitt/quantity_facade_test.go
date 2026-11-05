package ecowitt //nolint:testpackage // Facade tests exercise the package-private quantity seam.

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

func TestPhysicalQuantitySupportHasNoDuplicatedUnit(t *testing.T) {
	t.Parallel()
	for _, kind := range []quantityKind{quantityTemperature, quantityRelativeHumidity, quantityPressure, quantitySpeed} {
		metadata := adapter.EntityMetadata{Key: "entity", ExternalID: "slot/entity", Name: "Quantity"}
		descriptor, err := newQuantityDescriptor(kind, metadata, "", 0, 1)
		if err != nil {
			t.Fatal(err)
		}
		if string(descriptor.Support) != `{"state":{},"operations":{}}` {
			t.Fatalf("kind %d support = %s", kind, descriptor.Support)
		}
		observation, err := newQuantityObservation(
			kind,
			"entity",
			normalizedState{Kind: kind, MilliCelsius: 21500, Value: 1},
			"",
			0,
			1,
			time.Unix(1, 0).UTC(),
			nil,
		)
		if err != nil {
			t.Fatal(err)
		}
		want := "1"
		if kind == quantityTemperature {
			want = "21500"
		}
		if string(observation.Value) != want {
			t.Fatalf("kind %d State = %s, want %s", kind, observation.Value, want)
		}
	}
}

func TestNumericSensorUnitRemainsConfigurable(t *testing.T) {
	t.Parallel()
	metadata := adapter.EntityMetadata{Key: "level", ExternalID: "slot/level", Name: "Level"}
	descriptor, err := newQuantityDescriptor(quantityNumericSensor, metadata, "widgets", 2, 9)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"state":{"maximum":9,"minimum":2,"unit":"widgets"},"operations":{}}`
	if string(descriptor.Support) != want {
		t.Fatalf("numeric sensor support = %s, want %s", descriptor.Support, want)
	}
	observation, err := newQuantityObservation(
		quantityNumericSensor,
		"entity",
		normalizedState{Kind: quantityNumericSensor, Value: 5},
		"widgets",
		2,
		9,
		time.Unix(1, 0).UTC(),
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if string(observation.Value) != "5" {
		t.Fatalf("numeric sensor observation = %s, want 5", observation.Value)
	}
}
