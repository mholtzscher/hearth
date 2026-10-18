package adapters_test

import (
	"math"
	"strings"
	"testing"
	"time"

	"github.com/mholtzscher/hearth/internal/modules/adapters"
)

func TestHealthSnapshotCanonicalWireEvidence(t *testing.T) {
	t.Parallel()
	// Independent wire fixture protects exact int64 fidelity, nullable runtime,
	// lexical keys and equivalent zero-offset timestamp normalization.
	input := ` {"status":"unknown", "revision":9223372036854775807,"runtime_id":null,"reason_code":"hearth.awaiting_health","source":"core","adapter_id":"bridge","evidence_at":"2026-10-18T12:00:00.000+00:00","changed_at":"2026-10-18T12:00:00Z"} `
	snapshot, err := adapters.DecodeHealthSnapshot([]byte(input))
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Revision != adapters.HealthRevision(math.MaxInt64) || snapshot.RuntimeID != nil ||
		snapshot.ChangedAt.Location() != time.UTC {
		t.Fatalf("decoded snapshot = %#v", snapshot)
	}
	body, err := adapters.EncodeHealthSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"adapter_id":"bridge","changed_at":"2026-10-18T12:00:00Z","evidence_at":"2026-10-18T12:00:00Z","reason_code":"hearth.awaiting_health","revision":9223372036854775807,"runtime_id":null,"source":"core","status":"unknown"}`
	if string(body) != want {
		t.Fatalf("canonical snapshot = %s; want %s", body, want)
	}
	runtime := adapters.RuntimeID("run_01890f47-7a6b-7c4d-8e9f-0123456789ab")
	snapshot.RuntimeID = &runtime
	source := snapshot.EvidenceAt
	snapshot.SourceObservedAt = &source
	normalized, err := adapters.NormalizeHealthSnapshot(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	runtime = "changed"
	snapshot.Reason.Code = "changed"
	source = time.Time{}
	if *normalized.RuntimeID != "run_01890f47-7a6b-7c4d-8e9f-0123456789ab" ||
		normalized.Reason.Code != "hearth.awaiting_health" ||
		normalized.SourceObservedAt.IsZero() {
		t.Fatalf("normalized snapshot aliases input: %#v", normalized)
	}
}

func TestHealthSnapshotRejectsInvalidEvidence(t *testing.T) {
	t.Parallel()
	const valid = `{"adapter_id":"bridge","changed_at":"2026-10-18T12:00:00Z","evidence_at":"2026-10-18T12:00:00Z","reason_code":"hearth.awaiting_health","revision":1,"runtime_id":null,"source":"core","status":"unknown"}`
	cases := map[string]string{
		"duplicate key":   strings.Replace(valid, `"revision":1`, `"revision":1,"revision":2`, 1),
		"missing runtime": strings.Replace(valid, `"runtime_id":null,`, "", 1),
		"optional null": strings.Replace(
			valid,
			`"reason_code":"hearth.awaiting_health"`,
			`"reason_code":null`,
			1,
		),
		"zero revision":           strings.Replace(valid, `"revision":1`, `"revision":0`, 1),
		"fractional revision":     strings.Replace(valid, `"revision":1`, `"revision":1.0`, 1),
		"overflow revision":       strings.Replace(valid, `"revision":1`, `"revision":9223372036854775808`, 1),
		"unknown field":           strings.Replace(valid, `"revision":1`, `"revision":1,"extra":true`, 1),
		"non UTC":                 strings.ReplaceAll(valid, "12:00:00Z", "13:00:00+01:00"),
		"healthy without runtime": strings.Replace(valid, `"status":"unknown"`, `"status":"healthy"`, 1),
		"unknown without reason":  strings.Replace(valid, `"reason_code":"hearth.awaiting_health",`, "", 1),
		"unbounded reason": strings.Replace(
			valid,
			"hearth.awaiting_health",
			"hearth."+strings.Repeat("a", 129),
			1,
		),
		"wrong namespace": strings.Replace(valid, "hearth.awaiting_health", "vendor.offline", 1),
		"invalid runtime": strings.Replace(valid, `"runtime_id":null`, `"runtime_id":"run_bad"`, 1),
		"trailing value":  valid + `{}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := adapters.DecodeHealthSnapshot([]byte(body)); err == nil {
				t.Fatalf("accepted %s", body)
			}
		})
	}
}
