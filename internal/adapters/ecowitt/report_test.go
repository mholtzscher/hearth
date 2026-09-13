package ecowitt //nolint:testpackage // Parser tests exercise the package-private wire model.

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

// fixtureKeyCount is the checked-in fixture's unique key count, an
// independent oracle read from the captured real payload. The decoded Field
// map is one smaller because the PASSKEY is deliberately dropped.
const (
	fixtureKeyCount      = 44
	fixtureDecodedFields = fixtureKeyCount - 1
)

// TestParseStationReportAcceptsSanitizedFixture protects the real captured
// wire shape. It fails if form decoding, '+' handling, percent decoding, the
// required identity fields, or the optional source time regress.
func TestParseStationReportAcceptsSanitizedFixture(t *testing.T) {
	t.Parallel()

	payload := loadFixture(t, "gw2000-ws90-report.txt")
	if len(payload) > maximumReportBytes {
		t.Fatalf("fixture is %d bytes, over the wire bound", len(payload))
	}
	report, err := parseStationReport(payload, fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatalf("parse fixture: %v", err)
	}
	if report.StationType != "GW2000B_V3.3.2" {
		t.Fatalf("station type = %q, want the captured GW2000B firmware identity", report.StationType)
	}
	if report.Model != "GW2000B" {
		t.Fatalf("model = %q, want GW2000B", report.Model)
	}
	if report.DateUTC != "2026-09-12 14:30:00" {
		t.Fatalf("decoded dateutc = %q, want the '+' and percent-decoded station date", report.DateUTC)
	}
	if report.Passkey != sanitizedPasskey(t) {
		t.Fatal("decoded PASSKEY does not match the configured secret")
	}
	if report.SourceTime == nil {
		t.Fatal("sane fixture source time is absent")
	}
	wantSourceTime := time.Date(2026, time.September, 12, 14, 30, 0, 0, time.UTC)
	if !report.SourceTime.Equal(wantSourceTime) {
		t.Fatalf("source time = %s, want %s", report.SourceTime, wantSourceTime)
	}
	if len(report.Fields) != fixtureDecodedFields {
		t.Fatalf("decoded field count = %d, want %d", len(report.Fields), fixtureDecodedFields)
	}
	if _, leaked := report.Fields[passkeyField]; leaked {
		t.Fatal("decoded report retains the PASSKEY in its field map")
	}
	for _, expected := range []struct{ key, value string }{
		{"tempinf", "71.96"},
		{"humidityin", "43"},
		{"baromrelin", "29.046"},
		{"tempf", "65.84"},
		{"humidity", "85"},
		{"winddir", "44"},
		{"windspeedmph", "3.13"},
		{"windgustmph", "4.03"},
		{"maxdailygust", "6.04"},
		{"solarradiation", "98.26"},
		{"uv", "0"},
		{"rrain_piezo", "0.000"},
		{"erain_piezo", "0.150"},
		{"hrain_piezo", "0.000"},
		{"drain_piezo", "0.000"},
		{"wrain_piezo", "0.000"},
		{"mrain_piezo", "1.390"},
		{"yrain_piezo", "32.866"},
	} {
		if report.Fields[expected.key] != expected.value {
			t.Errorf("field %s = %q, want %q", expected.key, report.Fields[expected.key], expected.value)
		}
	}
	if _, present := report.Fields["last24hrain_piezo"]; !present {
		t.Error("unknown-but-present field was not retained in the decoded field set")
	}
}

// TestParseStationReportRejectsInvalidPayloads protects the bounded parser
// contract. Every case must produce one of the fixed sentinels, which keeps a
// rejection diagnostic free of payload content.
func TestParseStationReportRejectsInvalidPayloads(t *testing.T) {
	t.Parallel()

	repeated := strings.Repeat("a=1&", maximumReportFields)
	oversized := "PASSKEY=" + strings.Repeat("0", maximumReportBytes)

	var distinctKeys strings.Builder
	for index := range maximumReportFields + 1 {
		if index > 0 {
			distinctKeys.WriteByte('&')
		}
		fmt.Fprintf(&distinctKeys, "field%d=1", index)
	}
	overFieldLimit := distinctKeys.String() +
		"&PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00"

	for _, testCase := range []struct {
		name    string
		payload string
		want    error
	}{
		{
			name:    "oversized payload",
			payload: oversized,
			want:    errReportTooLarge,
		},
		{
			name:    "malformed percent escape",
			payload: "PASSKEY=%zz&stationtype=GW2000B&dateutc=2026-09-12",
			want:    errReportMalformed,
		},
		{
			name:    "semicolon separator",
			payload: "PASSKEY=" + sanitizedPasskeyHex + ";stationtype=GW2000B",
			want:    errReportMalformed,
		},
		{
			name:    "empty key",
			payload: "=1&PASSKEY=" + sanitizedPasskeyHex,
			want:    errReportMalformed,
		},
		{
			name:    "over field limit",
			payload: overFieldLimit,
			want:    errReportFieldLimit,
		},
		{
			name:    "repeated value for one key",
			payload: repeated + "PASSKEY=" + sanitizedPasskeyHex,
			want:    errReportDuplicateField,
		},
		{
			name:    "duplicate key",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&PASSKEY=" + sanitizedPasskeyHex,
			want:    errReportDuplicateField,
		},
		{
			name:    "duplicate measurement key",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=x&tempf=1&tempf=2",
			want:    errReportDuplicateField,
		},
		{
			name:    "missing passkey",
			payload: "stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00",
			want:    errReportMissingIdentity,
		},
		{
			name:    "empty passkey",
			payload: "PASSKEY=&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00",
			want:    errReportMissingIdentity,
		},
		{
			name:    "short passkey",
			payload: "PASSKEY=0123456789abcdef&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00",
			want:    errReportMalformed,
		},
		{
			name:    "non hexadecimal passkey",
			payload: "PASSKEY=zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz&stationtype=GW2000B&dateutc=x",
			want:    errReportMalformed,
		},
		{
			name:    "wrong passkey",
			payload: "PASSKEY=ffffffffffffffffffffffffffffffff&stationtype=GW2000B&dateutc=x",
			want:    errReportWrongPasskey,
		},
		{
			name:    "missing station type",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&dateutc=2026-09-12+14%3A30%3A00",
			want:    errReportMissingIdentity,
		},
		{
			name:    "empty station type",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=&dateutc=2026-09-12+14%3A30%3A00",
			want:    errReportMissingIdentity,
		},
		{
			name:    "foreign station family",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW1100A_V2.2.9&dateutc=x",
			want:    errReportUnexpectedStationType,
		},
		{
			name:    "missing dateutc",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B",
			want:    errReportMissingIdentity,
		},
		{
			name:    "empty dateutc",
			payload: "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=",
			want:    errReportMissingIdentity,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			payload := []byte(testCase.payload)
			report, err := parseStationReport(payload, fixtureReceivedAt, sanitizedPasskey(t))
			if !errors.Is(err, testCase.want) {
				t.Fatalf("parse error = %v, want %v", err, testCase.want)
			}
			if report.StationType != "" || report.DateUTC != "" || report.Fields != nil {
				t.Fatalf("rejected report leaked partial state: %#v", report)
			}
			if err != nil && strings.Contains(err.Error(), sanitizedPasskeyHex) {
				t.Fatal("parse error exposed the PASSKEY")
			}
		})
	}
}

// TestParseStationReportRequiresEveryCatalogIdentityField protects the
// compatibility boundary that a report without the exact configured topic
// identity is rejected, while a compatible report is accepted even when every
// measurement is absent.
func TestParseStationReportRequiresEveryCatalogIdentityField(t *testing.T) {
	t.Parallel()

	identityOnly := "PASSKEY=" + sanitizedPasskeyHex +
		"&stationtype=GW2000B_V3.3.2&dateutc=2026-09-12+14%3A30%3A00"
	report, err := parseStationReport([]byte(identityOnly), fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatalf("identity-only report rejected: %v", err)
	}
	if len(report.Fields) != 2 {
		t.Fatalf("identity-only report fields = %#v, want only stationtype and dateutc", report.Fields)
	}
	if _, leaked := report.Fields[passkeyField]; leaked {
		t.Fatal("identity-only report retains the PASSKEY in its field map")
	}
	if report.Model != "" {
		t.Fatalf("absent model = %q, want empty", report.Model)
	}
	if report.SourceTime == nil {
		t.Fatal("identity-only report lost its sane source time")
	}
}

// TestParseStationReportEnforcesExactSizeAndFieldBounds protects the two
// decoding bounds at their exact edges: a 64 KiB payload is still parsed and a
// 256-key form is still accepted, while one byte or one key more is rejected.
func TestParseStationReportEnforcesExactSizeAndFieldBounds(t *testing.T) {
	t.Parallel()

	identity := "PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00"
	base := identity + "&pad="
	atLimit := base + strings.Repeat("x", maximumReportBytes-len(base))
	if len(atLimit) != maximumReportBytes {
		t.Fatalf("built payload is %d bytes, want exactly %d", len(atLimit), maximumReportBytes)
	}
	report, err := parseStationReport([]byte(atLimit), fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatalf("payload of exactly %d bytes was rejected: %v", maximumReportBytes, err)
	}
	if len(report.Fields) != 3 {
		t.Fatalf("decoded field count = %d, want the decoded stationtype, dateutc, and pad", len(report.Fields))
	}
	if _, err = parseStationReport(
		[]byte(atLimit+"x"), fixtureReceivedAt, sanitizedPasskey(t),
	); !errors.Is(err, errReportTooLarge) {
		t.Fatalf("payload of %d bytes error = %v, want the size rejection", len(atLimit)+1, err)
	}

	var keys strings.Builder
	keys.WriteString(identity)
	// Three identity keys plus 253 more is exactly the accepted maximum.
	for index := range maximumReportFields - 3 {
		fmt.Fprintf(&keys, "&key%d=1", index)
	}
	atFieldLimit := keys.String()
	if fieldCount := strings.Count(atFieldLimit, "&") + 1; fieldCount != maximumReportFields {
		t.Fatalf("built payload has %d keys, want exactly %d", fieldCount, maximumReportFields)
	}
	if _, err = parseStationReport(
		[]byte(atFieldLimit), fixtureReceivedAt, sanitizedPasskey(t),
	); err != nil {
		t.Fatalf("payload of exactly %d keys was rejected: %v", maximumReportFields, err)
	}
	if _, err = parseStationReport(
		[]byte(atFieldLimit+"&extra=1"), fixtureReceivedAt, sanitizedPasskey(t),
	); !errors.Is(err, errReportFieldLimit) {
		t.Fatalf("payload of %d keys error = %v, want the field-limit rejection", maximumReportFields+1, err)
	}
}

// TestParseSourceTimeRules protects the source-time plausibility window with
// hand-written boundaries rather than values read from production code.
func TestParseSourceTimeRules(t *testing.T) {
	t.Parallel()

	receivedAt := fixtureReceivedAt
	for _, testCase := range []struct {
		name  string
		value string
		want  bool
	}{
		{"typical", "2026-09-12 14:30:00", true},
		{"exactly five minutes ahead", "2026-09-12 14:35:04", true},
		{"one second past the window", "2026-09-12 14:35:05", false},
		{"millennium floor", "2000-01-01 00:00:00", true},
		{"one second before the floor", "1999-12-31 23:59:59", false},
		{"epoch", "1970-01-01 00:00:00", false},
		{"not a date", "not-a-date", false},
		{"date only", "2026-09-12", false},
		{"trailing text", "2026-09-12 14:30:00 extra", false},
		{"leap second shape", "2026-09-12 14:30:60", false},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			parsed := parseSourceTime(testCase.value, receivedAt)
			if (parsed != nil) != testCase.want {
				t.Fatalf("source time %q parsed = %v, want present = %v", testCase.value, parsed, testCase.want)
			}
			if parsed != nil && parsed.Location() != time.UTC {
				t.Fatalf("source time location = %v, want UTC", parsed.Location())
			}
		})
	}
}

// TestReportSignatureUsesDateAndPayloadHash protects connection-local
// duplicate identity. The same decoded date with different payload bytes must
// stay distinct so a real value change is never suppressed.
func TestReportSignatureUsesDateAndPayloadHash(t *testing.T) {
	t.Parallel()

	first := []byte(
		"PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00&tempf=65.84",
	)
	second := []byte(
		"PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A00&tempf=65.85",
	)
	later := []byte(
		"PASSKEY=" + sanitizedPasskeyHex + "&stationtype=GW2000B&dateutc=2026-09-12+14%3A30%3A08&tempf=65.84",
	)

	firstReport, err := parseStationReport(first, fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatal(err)
	}
	laterReport, err := parseStationReport(later, fixtureReceivedAt, sanitizedPasskey(t))
	if err != nil {
		t.Fatal(err)
	}
	samePayload := newReportSignature(firstReport, first)
	if samePayload != newReportSignature(firstReport, first) {
		t.Fatal("identical payload and date produced different signatures")
	}
	if samePayload == newReportSignature(firstReport, second) {
		t.Fatal("different payload bytes produced the same duplicate signature")
	}
	if samePayload == newReportSignature(laterReport, later) {
		t.Fatal("a later station date produced the same duplicate signature")
	}
}
