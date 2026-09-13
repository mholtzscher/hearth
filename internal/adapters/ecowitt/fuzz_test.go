package ecowitt //nolint:testpackage // Fuzz targets exercise the package-private parser and projection.

import (
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/sdk/adapter"
)

// parseFailureSentinels is the closed set of parser failures. A fuzz corpus can
// only ever produce one of these, which proves a rejection diagnostic can never
// carry payload content, a field value, or a PASSKEY.
func parseFailureSentinels() []error {
	return []error{
		errReportTooLarge,
		errReportMalformed,
		errReportFieldLimit,
		errReportDuplicateField,
		errReportMissingIdentity,
		errReportWrongPasskey,
		errReportUnexpectedStationType,
	}
}

// FuzzParseStationReport protects the bounded parser: no panic, a closed set of
// failures, an exact size bound, and a decoded report whose identity is exactly
// the configured PASSKEY with no PASSKEY retained in its field map.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func FuzzParseStationReport(fuzz *testing.F) {
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-report.txt"))
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-malformed.txt"))
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-missing.txt"))
	fuzz.Add([]byte("PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00"))
	fuzz.Add([]byte("\x00\xff"))
	fuzz.Add([]byte{})

	expected := mustDecodePasskey(fuzz)
	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		report, err := parseStationReport(payload, fixtureReceivedAt, expected)
		if len(payload) > maximumReportBytes {
			if !errors.Is(err, errReportTooLarge) {
				t.Fatalf("parser error for %d payload bytes = %v, want the size rejection", len(payload), err)
			}
			return
		}
		if err != nil {
			for _, sentinel := range parseFailureSentinels() {
				if errors.Is(err, sentinel) {
					return
				}
			}
			t.Fatalf("parser returned an unclassified failure %v for %d payload bytes", err, len(payload))
		}
		if len(report.Fields) > maximumReportFields-1 {
			t.Fatalf("decoded %d fields", len(report.Fields))
		}
		if _, leaked := report.Fields[passkeyField]; leaked {
			t.Fatal("decoded report retained the PASSKEY")
		}
		if report.Passkey != expected {
			t.Fatal("decoded report identity does not match the configured PASSKEY")
		}
		if report.DateUTC == "" || report.StationType == "" {
			t.Fatal("decoded report omitted a required identity field")
		}
		if report.SourceTime != nil {
			floor := time.Date(2000, time.January, 1, 0, 0, 0, 0, time.UTC)
			if report.SourceTime.Before(floor) {
				t.Fatalf("source time %s is before the plausibility floor", report.SourceTime)
			}
			if report.SourceTime.After(fixtureReceivedAt.Add(maximumSourceTimeAhead + time.Second)) {
				t.Fatalf("source time %s is implausibly far ahead", report.SourceTime)
			}
		}
	})
}

// FuzzProjectReport protects the projection: no panic, every published State is
// a finite canonical number inside its registered envelope, every Entity is
// published at most once, and no valid sibling is suppressed by a malformed
// one.
//
//nolint:gocognit // Table-driven contract test; the explicit field comparisons are the assertion.
func FuzzProjectReport(fuzz *testing.F) {
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-report.txt"))
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-malformed.txt"))
	fuzz.Add(fixtureBytes(fuzz, "gw2000-ws90-missing.txt"))
	fuzz.Add([]byte("garbage"))

	expected := mustDecodePasskey(fuzz)
	plans, err := ecowittMeasurementCatalog()
	if err != nil {
		fuzz.Fatalf("compile capability catalog: %v", err)
	}
	registrations := staticRegistrations(Config{
		GatewayName: "Gateway", OutdoorArrayName: "Array",
	}, plans)
	routes, err := buildRouteSnapshot(plans, []adapter.Binding{
		derivedBinding(registrations[0]), derivedBinding(registrations[1]),
	})
	if err != nil {
		fuzz.Fatalf("build route snapshot: %v", err)
	}

	fuzz.Fuzz(func(t *testing.T, payload []byte) {
		report, parseErr := parseStationReport(payload, fixtureReceivedAt, expected)
		if parseErr != nil {
			return
		}
		projected, invalid := projectReport(routes, report, fixtureReceivedAt)
		if invalid < 0 {
			t.Fatalf("invalid measurement count = %d", invalid)
		}
		if len(projected) > len(routes.entities) {
			t.Fatalf("projected %d measurements for %d Entities", len(projected), len(routes.entities))
		}
		seen := make(map[string]struct{}, len(projected))
		for _, measurement := range projected {
			if _, duplicate := seen[measurement.Observation.EntityID]; duplicate {
				t.Fatalf("Entity %s was published twice", measurement.Observation.EntityID)
			}
			seen[measurement.Observation.EntityID] = struct{}{}
			if measurement.Observation.AdapterReceivedAt != fixtureReceivedAt.Format(time.RFC3339Nano) {
				t.Fatalf("Observation receipt time = %q", measurement.Observation.AdapterReceivedAt)
			}
			var value float64
			if decodeErr := json.Unmarshal(measurement.Observation.Value, &value); decodeErr != nil {
				t.Fatalf("Observation State %s is not a JSON number: %v", measurement.Observation.Value, decodeErr)
			}
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("Observation State %s is not finite", measurement.Observation.Value)
			}
			plan := measurement.Route.Plan
			if value < plan.Minimum || value > plan.Maximum {
				t.Fatalf("Observation State %v is outside the %s envelope [%v, %v]",
					value, plan.EntityKey, plan.Minimum, plan.Maximum)
			}
		}
		// A valid measurement is never suppressed by a malformed sibling: every
		// field that decodes produces exactly one Observation.
		want := 0
		for _, route := range routes.entities {
			raw, present := report.Fields[route.Plan.Field]
			if !present {
				continue
			}
			if _, decodeErr := route.Plan.Decode(raw); decodeErr == nil {
				want++
			}
		}
		if len(payload) <= maximumReportBytes && len(projected) != want {
			t.Fatalf("projected %d Observations, want %d decodable fields", len(projected), want)
		}
	})
}
