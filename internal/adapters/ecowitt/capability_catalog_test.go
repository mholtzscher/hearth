package ecowitt //nolint:testpackage // Catalog tests exercise the package-private plan table.

import (
	"encoding/json"
	"testing"
)

// catalog compiles the production capability catalog for a test.
func catalog(t *testing.T) []measurementPlan {
	t.Helper()
	plans, err := ecowittMeasurementCatalog()
	if err != nil {
		t.Fatalf("compile capability catalog: %v", err)
	}
	return plans
}

// planByKey returns one catalog plan by Entity key.
func planByKey(t *testing.T, plans []measurementPlan, key string) measurementPlan {
	t.Helper()
	for _, plan := range plans {
		if plan.EntityKey == key {
			return plan
		}
	}
	t.Fatalf("capability catalog has no Entity key %q", key)
	return measurementPlan{}
}

// catalogueEntry is one hand-written contract row. The table below is written
// from the Adapter contract, not derived from the production catalog, so a
// catalog defect cannot restate its own expectation.
type catalogueEntry struct {
	slot               deviceSlot
	field              string
	key                string
	name               string
	typeID             string
	unit               string
	minimum            float64
	maximum            float64
	expectsNumericUnit bool
}

// catalogueOracle is the closed v1 capability contract in registration order:
// the gateway Device's four Entities, then the outdoor array Device's fifteen.
func catalogueOracle() []catalogueEntry {
	return []catalogueEntry{
		{
			slot: gatewaySlot, field: "tempinf", key: "indoor-temperature", name: "Indoor Temperature",
			typeID: "hearth.temperature/v1", unit: "mCel", minimum: -273150, maximum: 1000000,
		},
		{
			slot: gatewaySlot, field: "humidityin", key: "indoor-humidity", name: "Indoor Humidity",
			typeID: "hearth.relativehumidity/v1", unit: "%", minimum: 0, maximum: 100,
		},
		{
			slot: gatewaySlot, field: "baromrelin", key: "relative-pressure", name: "Relative Pressure",
			typeID: "hearth.pressure/v1", unit: "hPa", minimum: 0, maximum: 2000,
		},
		{
			slot: gatewaySlot, field: "baromabsin", key: "absolute-pressure", name: "Absolute Pressure",
			typeID: "hearth.pressure/v1", unit: "hPa", minimum: 0, maximum: 2000,
		},
		{
			slot: outdoorArraySlot, field: "tempf", key: "outdoor-temperature", name: "Outdoor Temperature",
			typeID: "hearth.temperature/v1", unit: "mCel", minimum: -273150, maximum: 1000000,
		},
		{
			slot: outdoorArraySlot, field: "humidity", key: "outdoor-humidity", name: "Outdoor Humidity",
			typeID: "hearth.relativehumidity/v1", unit: "%", minimum: 0, maximum: 100,
		},
		{
			slot: outdoorArraySlot, field: "winddir", key: "wind-direction", name: "Wind Direction",
			typeID: "hearth.numericsensor/v1", unit: "deg", minimum: 0, maximum: 360, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "windspeedmph", key: "wind-speed", name: "Wind Speed",
			typeID: "hearth.speed/v1", unit: "m/s", minimum: 0, maximum: 200,
		},
		{
			slot: outdoorArraySlot, field: "windgustmph", key: "wind-gust", name: "Wind Gust",
			typeID: "hearth.speed/v1", unit: "m/s", minimum: 0, maximum: 200,
		},
		{
			slot: outdoorArraySlot, field: "maxdailygust", key: "maximum-daily-gust", name: "Maximum Daily Gust",
			typeID: "hearth.speed/v1", unit: "m/s", minimum: 0, maximum: 200,
		},
		{
			slot: outdoorArraySlot, field: "solarradiation", key: "solar-radiation", name: "Solar Radiation",
			typeID: "hearth.numericsensor/v1", unit: "W/m²", minimum: 0, maximum: 10000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "uv", key: "uv-index", name: "UV Index",
			typeID: "hearth.numericsensor/v1", unit: "index", minimum: 0, maximum: 100, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "rrain_piezo", key: "rain-rate", name: "Rain Rate",
			typeID: "hearth.numericsensor/v1", unit: "mm/h", minimum: 0, maximum: 10000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "erain_piezo", key: "event-rain", name: "Event Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "hrain_piezo", key: "hourly-rain", name: "Hourly Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "drain_piezo", key: "daily-rain", name: "Daily Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "wrain_piezo", key: "weekly-rain", name: "Weekly Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "mrain_piezo", key: "monthly-rain", name: "Monthly Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
		{
			slot: outdoorArraySlot, field: "yrain_piezo", key: "yearly-rain", name: "Yearly Rain",
			typeID: "hearth.numericsensor/v1", unit: "mm", minimum: 0, maximum: 10000000, expectsNumericUnit: true,
		},
	}
}

// TestCatalogMatchesContractTable protects registration order, slot ownership,
// vendor field ownership, Entity keys and names, Hearth types, canonical units,
// and validation envelopes against a hand-written contract table.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func TestCatalogMatchesContractTable(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	oracle := catalogueOracle()
	if len(plans) != len(oracle) {
		t.Fatalf("catalog size = %d, want %d", len(plans), len(oracle))
	}
	for index, expected := range oracle {
		plan := plans[index]
		if plan.Slot != expected.slot || plan.Field != expected.field || plan.EntityKey != expected.key {
			t.Errorf(
				"catalog[%d] = slot %q field %q key %q, want slot %q field %q key %q",
				index, plan.Slot, plan.Field, plan.EntityKey, expected.slot, expected.field, expected.key,
			)
		}
		if plan.EntityName != expected.name {
			t.Errorf("catalog[%d] name = %q, want %q", index, plan.EntityName, expected.name)
		}
		if plan.Descriptor.Type != expected.typeID {
			t.Errorf("catalog[%d] type = %q, want %q", index, plan.Descriptor.Type, expected.typeID)
		}
		if plan.Descriptor.Key != expected.key {
			t.Errorf("catalog[%d] descriptor key = %q, want %q", index, plan.Descriptor.Key, expected.key)
		}
		if plan.Descriptor.ExternalID != string(expected.slot)+"/"+expected.key {
			t.Errorf("catalog[%d] external ID = %q, want slot-scoped key", index, plan.Descriptor.ExternalID)
		}
		if plan.Descriptor.Name != expected.name {
			t.Errorf("catalog[%d] descriptor name = %q, want %q", index, plan.Descriptor.Name, expected.name)
		}
		if plan.Descriptor.InitiallyEnabled != nil {
			t.Errorf("catalog[%d] set initially_enabled, want unset", index)
		}
		if plan.Unit != expected.unit {
			t.Errorf("catalog[%d] unit = %q, want %q", index, plan.Unit, expected.unit)
		}
		if plan.Minimum != expected.minimum || plan.Maximum != expected.maximum {
			t.Errorf(
				"catalog[%d] envelope = [%v, %v], want [%v, %v]",
				index, plan.Minimum, plan.Maximum, expected.minimum, expected.maximum,
			)
		}
		if plan.Decode == nil {
			t.Errorf("catalog[%d] has no decoder", index)
		}
	}
}

// TestCatalogRegistersFourGatewayAndFifteenOutdoorEntities protects the per-
// Device Entity counts and the read-only closed operation support of every
// registered Entity.
func TestCatalogRegistersFourGatewayAndFifteenOutdoorEntities(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	if got := len(plansForSlot(plans, gatewaySlot)); got != 4 {
		t.Fatalf("gateway Entity count = %d, want 4", got)
	}
	if got := len(plansForSlot(plans, outdoorArraySlot)); got != 15 {
		t.Fatalf("outdoor array Entity count = %d, want 15", got)
	}
	for _, plan := range plans {
		document := supportDocument(t, plan.Descriptor.Support)
		operations, ok := document["operations"].(map[string]any)
		if !ok {
			t.Fatalf("%s support has no operations object: %s", plan.EntityKey, plan.Descriptor.Support)
		}
		if len(operations) != 0 {
			t.Fatalf("%s operations = %v, want the closed empty shape", plan.EntityKey, operations)
		}
		if len(document) != 2 {
			t.Fatalf("%s support keys = %v, want exactly state and operations", plan.EntityKey, document)
		}
	}
}

// TestCatalogSupportMatchesEntityTypeContracts protects each registered
// support document: the reusable physical quantities declare the closed empty
// state shape, and each generic numeric sensor carries exactly its unit and
// envelope.
func TestCatalogSupportMatchesEntityTypeContracts(t *testing.T) {
	t.Parallel()

	plans := catalog(t)
	for _, expected := range catalogueOracle() {
		plan := planByKey(t, plans, expected.key)
		document := supportDocument(t, plan.Descriptor.Support)
		state, ok := document["state"].(map[string]any)
		if !ok {
			t.Fatalf("%s support has no state object: %s", expected.key, plan.Descriptor.Support)
		}
		if !expected.expectsNumericUnit {
			if len(state) != 0 {
				t.Fatalf("%s state support = %v, want the closed empty shape", expected.key, state)
			}
			continue
		}
		if state["unit"] != expected.unit {
			t.Fatalf("%s unit = %v, want %q", expected.key, state["unit"], expected.unit)
		}
		if state["minimum"] != expected.minimum || state["maximum"] != expected.maximum {
			t.Fatalf(
				"%s envelope = [%v, %v], want [%v, %v]",
				expected.key, state["minimum"], state["maximum"], expected.minimum, expected.maximum,
			)
		}
	}
}

// TestCatalogIsDeterministic protects restart stability: recompiling the
// catalog yields identical identity, order, and support.
func TestCatalogIsDeterministic(t *testing.T) {
	t.Parallel()

	first := catalog(t)
	second := catalog(t)
	if len(first) != len(second) {
		t.Fatalf("catalog sizes differ: %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index].Descriptor.ExternalID != second[index].Descriptor.ExternalID {
			t.Fatalf("catalog[%d] external ID changed between compilations", index)
		}
		if string(first[index].Descriptor.Support) != string(second[index].Descriptor.Support) {
			t.Fatalf("catalog[%d] support changed between compilations", index)
		}
	}
}

// supportDocument decodes one registered support blob.
func supportDocument(t *testing.T, raw json.RawMessage) map[string]any {
	t.Helper()
	var document map[string]any
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("decode support %s: %v", raw, err)
	}
	return document
}
