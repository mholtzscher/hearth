package ecowitt //nolint:testpackage // Projection tests exercise the package-private observation seam.

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// observationValue decodes one published Observation's typed State.
func observationValue(t *testing.T, observation adapter.Observation) float64 {
	t.Helper()
	var value float64
	if err := json.Unmarshal(observation.Value, &value); err != nil {
		t.Fatalf("decode Observation value %s: %v", observation.Value, err)
	}
	return value
}

// TestProjectReportPublishesEveryValidMeasurementInCatalogOrder protects
// catalog-order projection, canonical conversion, and shared timestamps against
// the sanitized real capture.
func TestProjectReportPublishesEveryValidMeasurementInCatalogOrder(t *testing.T) {
	t.Parallel()

	payload := loadFixture(t, "gw2000-ws90-report.txt")
	report, err := parseStationReport(payload, fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	snapshot := snapshotFor(t, testConfig(t))
	projected, invalid := projectReport(snapshot, report, fixtureReceivedAt)
	if invalid != 0 {
		t.Fatalf("invalid measurements = %d, want 0 for the complete capture", invalid)
	}
	if len(projected) != len(snapshot.entities) {
		t.Fatalf("projected measurements = %d, want one per catalog Entity", len(projected))
	}
	for index, measurement := range projected {
		if measurement.Route.EntityKey != snapshot.entities[index].EntityKey {
			t.Fatalf("projected[%d] key = %q, want %q",
				index, measurement.Route.EntityKey, snapshot.entities[index].EntityKey)
		}
		if measurement.Observation.EntityID != snapshot.entities[index].EntityID {
			t.Fatalf("projected[%d] Entity ID = %q, want %q",
				index, measurement.Observation.EntityID, snapshot.entities[index].EntityID)
		}
		if measurement.Observation.AdapterReceivedAt != fixtureReceivedAt.Format(time.RFC3339Nano) {
			t.Fatalf("projected[%d] adapter received time = %q, want the single receipt time",
				index, measurement.Observation.AdapterReceivedAt)
		}
		if measurement.Observation.SourceUpdatedAt == nil {
			t.Fatalf("projected[%d] omitted the sane source time", index)
		}
		if *measurement.Observation.SourceUpdatedAt != "2026-09-12T14:30:00Z" {
			t.Fatalf("projected[%d] source time = %q", index, *measurement.Observation.SourceUpdatedAt)
		}
	}
	// Hand-written conversion oracles for the sanitized capture.
	values := make(map[string]float64, len(projected))
	for _, measurement := range projected {
		values[measurement.Route.EntityKey] = observationValue(t, measurement.Observation)
	}
	for key, want := range map[string]float64{
		"indoor-temperature":  22200,
		"indoor-humidity":     43,
		"relative-pressure":   29.046 * 33.8638866667,
		"absolute-pressure":   29.046 * 33.8638866667,
		"outdoor-temperature": 18800,
		"outdoor-humidity":    85,
		"wind-direction":      44,
		"wind-speed":          3.13 * 0.44704,
		"wind-gust":           4.03 * 0.44704,
		"maximum-daily-gust":  6.04 * 0.44704,
		"solar-radiation":     98.26,
		"uv-index":            0,
		"rain-rate":           0,
		"event-rain":          0.150 * 25.4,
		"hourly-rain":         0,
		"daily-rain":          0,
		"weekly-rain":         0,
		"monthly-rain":        1.390 * 25.4,
		"yearly-rain":         32.866 * 25.4,
	} {
		got, present := values[key]
		if !present {
			t.Errorf("no Observation for %s", key)
			continue
		}
		tolerance := 1e-9 * math.Max(1, math.Abs(want))
		if math.Abs(got-want) > tolerance {
			t.Errorf("%s value = %v, want %v", key, got, want)
		}
	}
}

// TestProjectReportIsolatesMalformedSiblings protects per-Entity independence:
// one malformed or out-of-envelope field produces no Observation and leaves
// every valid sibling published.
func TestProjectReportIsolatesMalformedSiblings(t *testing.T) {
	t.Parallel()

	report, err := parseStationReport(
		loadFixture(t, "gw2000-ws90-malformed.txt"), fixtureReceivedAt, sanitizedPasskey(t),
	)
	if err != nil {
		t.Fatalf("parse malformed fixture: %v", err)
	}
	snapshot := snapshotFor(t, testConfig(t))
	projected, invalid := projectReport(snapshot, report, fixtureReceivedAt)
	if invalid != 7 {
		t.Fatalf("invalid measurements = %d, want the 7 deliberately broken fields", invalid)
	}
	if len(projected) != len(snapshot.entities)-invalid {
		t.Fatalf("projected measurements = %d, want %d", len(projected), len(snapshot.entities)-invalid)
	}
	broken := map[string]struct{}{
		"outdoor-temperature": {}, "outdoor-humidity": {}, "solar-radiation": {}, "uv-index": {},
		"rain-rate": {}, "wind-direction": {}, "event-rain": {},
	}
	published := make(map[string]struct{}, len(projected))
	for _, measurement := range projected {
		published[measurement.Route.EntityKey] = struct{}{}
	}
	for key := range broken {
		if _, present := published[key]; present {
			t.Errorf("broken field %s was published", key)
		}
	}
	for _, plan := range snapshot.entities {
		if _, isBroken := broken[plan.EntityKey]; isBroken {
			continue
		}
		if _, present := published[plan.EntityKey]; !present {
			t.Errorf("valid sibling %s was suppressed by a malformed field", plan.EntityKey)
		}
	}
}

// TestProjectReportPublishesOnlyPresentMeasurements protects the absent-field
// contract and the never-observed Entity case.
func TestProjectReportPublishesOnlyPresentMeasurements(t *testing.T) {
	t.Parallel()

	report, err := parseStationReport(
		loadFixture(t, "gw2000-ws90-missing.txt"), fixtureReceivedAt, sanitizedPasskey(t),
	)
	if err != nil {
		t.Fatalf("parse missing fixture: %v", err)
	}
	snapshot := snapshotFor(t, testConfig(t))
	projected, invalid := projectReport(snapshot, report, fixtureReceivedAt)
	if invalid != 0 {
		t.Fatalf("invalid measurements = %d, want 0", invalid)
	}
	published := make(map[string]struct{}, len(projected))
	for _, measurement := range projected {
		published[measurement.Route.EntityKey] = struct{}{}
	}
	for _, key := range []string{
		"indoor-temperature", "indoor-humidity", "absolute-pressure", "outdoor-temperature",
		"outdoor-humidity", "wind-speed", "solar-radiation", "uv-index", "rain-rate", "daily-rain",
		"monthly-rain",
	} {
		if _, present := published[key]; present {
			t.Errorf("absent field %s was published", key)
		}
	}
	for _, key := range []string{
		"relative-pressure", "wind-direction", "wind-gust", "maximum-daily-gust", "event-rain",
		"hourly-rain", "weekly-rain", "yearly-rain",
	} {
		if _, present := published[key]; !present {
			t.Errorf("present field %s was not published", key)
		}
	}
}

// TestProjectReportLeavesImplausibleSourceTimeAbsent protects the optional
// source time: an implausible station clock omits source_updated_at while the
// report keeps its shared local receipt time.
func TestProjectReportLeavesImplausibleSourceTimeAbsent(t *testing.T) {
	t.Parallel()

	payload := loadFixture(t, "gw2000-ws90-report.txt")
	stale := strings.Replace(string(payload), "dateutc=2026-09-12+14%3A30%3A00", "dateutc=1970-01-01+00%3A00%3A00", 1)
	report, err := parseStationReport([]byte(stale), fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatalf("parse stale-clock report: %v", err)
	}
	snapshot := snapshotFor(t, testConfig(t))
	projected, _ := projectReport(snapshot, report, fixtureReceivedAt)
	if len(projected) != len(snapshot.entities) {
		t.Fatalf("projected measurements = %d, want %d", len(projected), len(snapshot.entities))
	}
	for index, measurement := range projected {
		if measurement.Observation.SourceUpdatedAt != nil {
			t.Fatalf("projected[%d] kept an implausible source time %q",
				index, *measurement.Observation.SourceUpdatedAt)
		}
		if measurement.Observation.AdapterReceivedAt != fixtureReceivedAt.Format(time.RFC3339Nano) {
			t.Fatalf("projected[%d] lost its local receipt time", index)
		}
	}
}

// TestProjectReportSuppressesNoUnchangedValues protects the no-checkpoint
// contract: projecting the same report twice yields identical evidence, because
// the Adapter owns no value cache and records fresh evidence for unchanged
// weather.
func TestProjectReportSuppressesNoUnchangedValues(t *testing.T) {
	t.Parallel()

	report, err := parseStationReport(
		loadFixture(t, "gw2000-ws90-report.txt"), fixtureReceivedAt, sanitizedPasskey(t),
	)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	snapshot := snapshotFor(t, testConfig(t))
	first, _ := projectReport(snapshot, report, fixtureReceivedAt)
	second, _ := projectReport(snapshot, report, fixtureReceivedAt)
	if len(first) != len(second) {
		t.Fatalf("reprojection produced %d then %d measurements", len(first), len(second))
	}
	for index := range first {
		if first[index].Observation.EntityID != second[index].Observation.EntityID {
			t.Fatalf("reprojection[%d] Entity ID changed", index)
		}
		if string(first[index].Observation.Value) != string(second[index].Observation.Value) {
			t.Fatalf("reprojection[%d] State changed for an unchanged report", index)
		}
	}
}

// TestProjectReportUsesCanonicalEntityIDsFromTheSnapshot protects that vendor
// identity never reaches an Observation: the published Entity ID is exactly the
// canonical ID the Session returned.
func TestProjectReportUsesCanonicalEntityIDsFromTheSnapshot(t *testing.T) {
	t.Parallel()

	report, err := parseStationReport(
		loadFixture(t, "gw2000-ws90-report.txt"), fixtureReceivedAt, sanitizedPasskey(t),
	)
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	plans := catalog(t)
	registrations := staticRegistrations(testConfig(t), plans)
	bindings := []adapter.Binding{
		derivedBinding(registrations[0]),
		derivedBinding(registrations[1]),
	}
	for index := range bindings[0].Entities {
		bindings[0].Entities[index].EntityID = "ent-canonical-gateway-" + bindings[0].Entities[index].Key
	}
	snapshot, err := buildRouteSnapshot(plans, bindings)
	if err != nil {
		t.Fatalf("build route snapshot: %v", err)
	}
	projected, _ := projectReport(snapshot, report, fixtureReceivedAt)
	for _, measurement := range projected {
		if !strings.HasPrefix(measurement.Observation.EntityID, "ent-canonical-gateway-") &&
			!strings.HasPrefix(measurement.Observation.EntityID, "ent-outdoor-array-") {
			t.Fatalf("Observation Entity ID = %q, want a canonical Session ID", measurement.Observation.EntityID)
		}
		if strings.Contains(measurement.Observation.EntityID, "GW2000") {
			t.Fatalf("Observation Entity ID %q carries vendor firmware identity", measurement.Observation.EntityID)
		}
	}
}
