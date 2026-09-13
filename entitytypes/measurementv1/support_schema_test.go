package measurementv1_test

import (
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/entitytypes/measurementv1"
)

// supportSchemaTestName identifies one authored support document and the
// schema boundary that must accept or reject it. The authoritative kind, unit,
// envelope, required-field, and closed-shape rules live entirely in
// support.schema.json, so these cases assert the codec's verdict rather than
// restating the catalog in Go.
type supportSchemaTest struct {
	name string
	raw  string
}

func compileMeasurementCodecs(t *testing.T) *measurementv1.Codecs {
	t.Helper()
	codecs, err := measurementv1.Compile()
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	return codecs
}

// decodeSupportRaw schema-decodes one support document through the
// authoritative codec, which embeds and enforces the complete raw schema.
func decodeSupportRaw(codecs *measurementv1.Codecs, raw string) error {
	_, _, err := codecs.Support.Decode([]byte(raw))
	return err
}

func validSupportDocument(kind, unit string, minimum, maximum string) string {
	var document strings.Builder
	document.WriteString(`{"state":{"measurement_kind":"`)
	document.WriteString(kind)
	document.WriteString(`","unit":"`)
	document.WriteString(unit)
	document.WriteString(`","minimum":`)
	document.WriteString(minimum)
	document.WriteString(`,"maximum":`)
	document.WriteString(maximum)
	document.WriteString(`},"operations":{}}`)
	return document.String()
}

// TestSupportSchemaAcceptsEveryCataloguedKindAndEnvelopeEdge pins the positive
// contract: each of the four kinds is accepted only with its canonical unit,
// and the kind-wide envelope limits are inclusive.
func TestSupportSchemaAcceptsEveryCataloguedKindAndEnvelopeEdge(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	cases := []struct {
		name string
		raw  string
	}{
		{name: "temperature envelope", raw: validSupportDocument("temperature", "Cel", "-273.15", "1000")},
		{name: "temperature inside envelope", raw: validSupportDocument("temperature", "Cel", "0", "40")},
		{name: "relative_humidity envelope", raw: validSupportDocument("relative_humidity", "%", "0", "100")},
		{name: "illuminance envelope", raw: validSupportDocument("illuminance", "lx", "0", "1000000000")},
		{name: "illuminance inside envelope", raw: validSupportDocument("illuminance", "lx", "5", "1000")},
		{name: "battery_level envelope", raw: validSupportDocument("battery_level", "%", "0", "100")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeSupportRaw(codecs, test.raw); err != nil {
				t.Fatalf("support %s rejected: %v", test.raw, err)
			}
		})
	}
}

// TestSupportSchemaRejectsWrongKindUnitPairs proves the schema, not a second Go
// table, owns kind/unit coherence: every catalogued kind rejects a unit that
// belongs to a different branch.
func TestSupportSchemaRejectsWrongKindUnitPairs(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	cases := []struct {
		name string
		raw  string
	}{
		{name: "temperature with percent", raw: validSupportDocument("temperature", "%", "0", "1000")},
		{name: "temperature with lux", raw: validSupportDocument("temperature", "lx", "0", "1000")},
		{name: "relative_humidity with Cel", raw: validSupportDocument("relative_humidity", "Cel", "0", "100")},
		{name: "relative_humidity with lux", raw: validSupportDocument("relative_humidity", "lx", "0", "100")},
		{name: "illuminance with Cel", raw: validSupportDocument("illuminance", "Cel", "0", "1000000000")},
		{name: "illuminance with percent", raw: validSupportDocument("illuminance", "%", "0", "1000000000")},
		{name: "battery_level with Cel", raw: validSupportDocument("battery_level", "Cel", "0", "100")},
		{name: "battery_level with lux", raw: validSupportDocument("battery_level", "lx", "0", "100")},
		{name: "canonical unit wrong case", raw: validSupportDocument("temperature", "cel", "-273.15", "1000")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeSupportRaw(codecs, test.raw); err == nil {
				t.Fatalf("wrong kind/unit pair accepted: %s", test.raw)
			}
		})
	}
}

// TestSupportSchemaRejectsOutOfEnvelopeBounds proves each kind's kind-wide
// physical envelope is enforced independently of the Entity's own bounds.
func TestSupportSchemaRejectsOutOfEnvelopeBounds(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	cases := []supportSchemaTest{
		{
			name: "temperature below absolute zero",
			raw:  validSupportDocument("temperature", "Cel", "-273.16", "1000"),
		},
		{
			name: "temperature above envelope",
			raw:  validSupportDocument("temperature", "Cel", "-273.15", "1000.01"),
		},
		{
			name: "relative_humidity below zero",
			raw:  validSupportDocument("relative_humidity", "%", "-0.1", "100"),
		},
		{
			name: "relative_humidity above one hundred",
			raw:  validSupportDocument("relative_humidity", "%", "0", "100.1"),
		},
		{
			name: "illuminance below zero",
			raw:  validSupportDocument("illuminance", "lx", "-1", "1000"),
		},
		{
			name: "illuminance above envelope",
			raw:  validSupportDocument("illuminance", "lx", "0", "1000000000.5"),
		},
		{
			name: "battery_level below zero",
			raw:  validSupportDocument("battery_level", "%", "-0.5", "100"),
		},
		{
			name: "battery_level above one hundred",
			raw:  validSupportDocument("battery_level", "%", "0", "100.5"),
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeSupportRaw(codecs, test.raw); err == nil {
				t.Fatalf("out-of-envelope bounds accepted: %s", test.raw)
			}
		})
	}
}

// TestSupportSchemaRejectsUnknownKinds proves the v1 catalog is closed: a kind
// outside the four released branches never decodes, including near misses that
// differ only by spelling.
func TestSupportSchemaRejectsUnknownKinds(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	cases := []supportSchemaTest{
		{name: "pressure", raw: validSupportDocument("pressure", "Pa", "0", "1000")},
		{name: "wind_speed", raw: validSupportDocument("wind_speed", "m/s", "0", "100")},
		{name: "temperature degree symbol", raw: validSupportDocument("°C", "Cel", "0", "100")},
		{name: "capitalized kind", raw: validSupportDocument("Temperature", "Cel", "0", "100")},
		{name: "empty kind", raw: validSupportDocument("", "Cel", "0", "100")},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeSupportRaw(codecs, test.raw); err == nil {
				t.Fatalf("unknown kind accepted: %s", test.raw)
			}
		})
	}
}

// TestSupportSchemaRejectsMissingRequiredFields proves every declared field is
// required, so no consumer can fall back to a zero-kind, zero-unit, or
// unbounded descriptor.
func TestSupportSchemaRejectsMissingRequiredFields(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	cases := []supportSchemaTest{
		{
			name: "missing measurement_kind",
			raw:  `{"state":{"unit":"Cel","minimum":0,"maximum":40},"operations":{}}`,
		},
		{
			name: "missing unit",
			raw:  `{"state":{"measurement_kind":"temperature","minimum":0,"maximum":40},"operations":{}}`,
		},
		{
			name: "missing minimum",
			raw:  `{"state":{"measurement_kind":"temperature","unit":"Cel","maximum":40},"operations":{}}`,
		},
		{
			name: "missing maximum",
			raw:  `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0},"operations":{}}`,
		},
		{
			name: "missing operations",
			raw:  `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0,"maximum":40}}`,
		},
		{
			name: "missing state",
			raw:  `{"operations":{}}`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if err := decodeSupportRaw(codecs, test.raw); err == nil {
				t.Fatalf("support missing a required field accepted: %s", test.raw)
			}
		})
	}
}

// TestSupportSchemaRejectsAdditionalProperties proves the support document is
// closed at every level, so future fields need an explicit contract change
// instead of silently riding along on retained descriptors.
func TestSupportSchemaRejectsAdditionalProperties(t *testing.T) {
	t.Parallel()
	cases := []supportSchemaTest{
		{
			name: "extra state property",
			raw: `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0,` +
				`"maximum":40,"display_unit":"°C"},"operations":{}}`,
		},
		{
			name: "extra top-level property",
			raw: `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0,` +
				`"maximum":40},"operations":{},"events":{}}`,
		},
		{
			name: "extra operations property",
			raw: `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":0,` +
				`"maximum":40},"operations":{"set":{}}}`,
		},
		{
			name: "wrong scalar type for minimum",
			raw: `{"state":{"measurement_kind":"temperature","unit":"Cel","minimum":"0",` +
				`"maximum":40},"operations":{}}`,
		},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			codecs := compileMeasurementCodecs(t)
			if err := decodeSupportRaw(codecs, test.raw); err == nil {
				t.Fatalf("closed support shape accepted: %s", test.raw)
			}
		})
	}
}

// TestSupportBehaviorRejectsInvertedBoundsSeparatelyFromSchema pins the seam
// the generator relies on: an in-envelope but inverted range schema-decodes,
// and the generated support_validation boundary is what rejects it. Schema-invalid
// supports never reach that boundary, which is why they cannot appear in the
// manifest's invalid_supports examples.
func TestSupportBehaviorRejectsInvertedBoundsSeparatelyFromSchema(t *testing.T) {
	t.Parallel()
	codecs := compileMeasurementCodecs(t)
	raw := validSupportDocument("temperature", "Cel", "1000", "-273.15")
	support, _, err := codecs.Support.Decode([]byte(raw))
	if err != nil {
		t.Fatalf("inverted in-envelope bounds should schema-decode: %v", err)
	}
	if validateErr := measurementv1.ValidateSupport(support); validateErr == nil {
		t.Fatal("inverted bounds unexpectedly passed generated support validation")
	}
}

// TestCodecsExposeAuthoritativeSchemaIDs keeps the schema identity the
// generator embeds aligned with the contract's declared $id values.
func TestCodecsExposeAuthoritativeSchemaIDs(t *testing.T) {
	t.Parallel()
	if measurementv1.StateSchemaID != "urn:hearth:schema:entity-type:measurement:v1:state" {
		t.Errorf("StateSchemaID = %q", measurementv1.StateSchemaID)
	}
	if measurementv1.SupportSchemaID != "urn:hearth:schema:entity-type:measurement:v1:support" {
		t.Errorf("SupportSchemaID = %q", measurementv1.SupportSchemaID)
	}
	if measurementv1.TypeID != "hearth.measurement/v1" {
		t.Errorf("TypeID = %q", measurementv1.TypeID)
	}
}
