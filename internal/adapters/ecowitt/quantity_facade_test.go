package ecowitt //nolint:testpackage // Facade tests exercise the package-private unit seam.

import (
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// canonicalUnits is a hand-written oracle mapping every reusable physical
// quantity to the one unit its Entity-type contract fixes. It is written from
// the Adapter contract rather than read from the production catalog, so a
// catalog defect cannot restate its own expectation.
func canonicalUnits() map[quantityKind]string {
	// Generic numeric sensors intentionally have no fixed unit to include.
	//nolint:exhaustive // The omitted quantityNumericSensor is the behavior under contrast.
	return map[quantityKind]string{
		quantityTemperature:      "mCel",
		quantityRelativeHumidity: "%",
		quantityPressure:         "hPa",
		quantitySpeed:            "m/s",
	}
}

func quantityMetadata() adapter.EntityMetadata {
	return adapter.EntityMetadata{Key: "entity", ExternalID: "slot/entity", Name: "Quantity"}
}

// TestQuantityDescriptorPinsCanonicalUnitPerKind protects that every semantic
// quantity publishes its plan's canonical unit in Entity support, and fails if
// a reusable physical quantity registers unlabeled State.
func TestQuantityDescriptorPinsCanonicalUnitPerKind(t *testing.T) {
	t.Parallel()
	for kind, unit := range canonicalUnits() {
		descriptor, err := newQuantityDescriptor(kind, quantityMetadata(), unit, 0, 1)
		if err != nil {
			t.Fatalf("kind %d unit %q: %v", kind, unit, err)
		}
		document := supportDocument(t, descriptor.Support)
		state, ok := document["state"].(map[string]any)
		if !ok || state["unit"] != unit || len(state) != 1 {
			t.Fatalf("kind %d support = %s, want state unit %q", kind, descriptor.Support, unit)
		}
	}
}

// TestQuantityDescriptorRejectsMismatchedUnit protects that a plan whose unit
// disagrees with its Entity type is rejected at construction rather than
// publishing a mislabeled reading.
func TestQuantityDescriptorRejectsMismatchedUnit(t *testing.T) {
	t.Parallel()
	for kind, unit := range canonicalUnits() {
		wrong := "not-" + unit
		if _, err := newQuantityDescriptor(kind, quantityMetadata(), wrong, 0, 1); err == nil {
			t.Fatalf("kind %d accepted mismatched unit %q", kind, wrong)
		}
	}
}

// TestQuantityObservationRejectsMismatchedUnit protects the same unit check on
// the Observation path, so a mislabeled reading cannot reach the wire.
func TestQuantityObservationRejectsMismatchedUnit(t *testing.T) {
	t.Parallel()
	receivedAt := time.Unix(1, 0).UTC()
	for kind, unit := range canonicalUnits() {
		state := normalizedState{Kind: kind, MilliCelsius: 21500, Value: 1}
		if _, err := newQuantityObservation(kind, "entity", state, unit, 0, 1, receivedAt, nil); err != nil {
			t.Fatalf("kind %d rejected canonical unit %q: %v", kind, unit, err)
		}
		if _, err := newQuantityObservation(kind, "entity", state, "not-"+unit, 0, 1, receivedAt, nil); err == nil {
			t.Fatalf("kind %d accepted mismatched unit %q", kind, "not-"+unit)
		}
	}
}

// TestCatalogPlanUnitMismatchIsRejected protects that measurementPlan.Unit is
// consumed rather than ignored: swapping one production plan's unit for another
// kind's unit makes descriptor construction fail.
func TestCatalogPlanUnitMismatchIsRejected(t *testing.T) {
	t.Parallel()
	for _, plan := range catalog(t) {
		canonical, ok := canonicalUnits()[plan.Kind]
		if !ok || plan.Unit != canonical {
			continue
		}
		mismatched := plan
		mismatched.Unit = "not-" + canonical
		if _, err := registerPlanDescriptors([]measurementPlan{mismatched}); err == nil {
			t.Fatalf("%s accepted mismatched plan unit %q", plan.EntityKey, mismatched.Unit)
		}
	}
}

// TestNumericSensorUnitRemainsConfigurable protects that the generic numeric
// sensor still carries whatever unit and envelope its plan declares, unlike the
// reusable physical quantities whose unit the contract fixes.
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
