package zwavejs //nolint:testpackage // Exercises production plan construction and its Adapter-owned native translation.

import (
	"encoding/json"
	"strconv"
	"testing"
	"time"
)

// Synthetic native levels exercise the production plan's public contract boundaries,
// not hardware compatibility. The integer oracle is independent of float rounding.
func TestBrightnessPlanCanonicalPercentageExhaustive(t *testing.T) {
	t.Parallel()
	plan := fixtureBrightnessPlan(t, levelPairFixture(0))
	for percentage := range int64(101) {
		parameters := json.RawMessage(`{"value":` + strconv.FormatInt(percentage, 10) + `}`)
		prepared, err := plan.PrepareSet(parameters)
		if err != nil {
			t.Fatalf("prepare %d: %v", percentage, err)
		}
		native, err := decodeZwaveLevel(prepared.Value)
		wantNative := (percentage*99 + 50) / 100
		if err != nil || native != wantNative {
			t.Fatalf("prepare %d = %s, %v; want native %d", percentage, prepared.Value, err, wantNative)
		}
		observation, err := plan.Observe("brightness", time.Now().UTC(), prepared.Value)
		if err != nil {
			t.Fatal(err)
		}
		var observed int64
		if err = json.Unmarshal(observation.Value, &observed); err != nil {
			t.Fatal(err)
		}
		if observed < 0 || observed > 100 || observed-percentage < -1 || observed-percentage > 1 {
			t.Fatalf("round trip %d -> %d -> %d exceeds canonical tolerance", percentage, native, observed)
		}
		assertOnlyNativeBrightnessTargetMatches(t, prepared, native)
	}
}

func assertOnlyNativeBrightnessTargetMatches(t *testing.T, prepared preparedSet, native int64) {
	t.Helper()
	for candidate := range int64(100) {
		if got := prepared.Matches(json.RawMessage(strconv.FormatInt(candidate, 10))); got != (candidate == native) {
			t.Fatalf("native target %d matched candidate %d: %v", native, candidate, got)
		}
	}
}

func TestBrightnessPlanNormalizesEveryNativeLevel(t *testing.T) {
	t.Parallel()
	plan := fixtureBrightnessPlan(t, levelPairFixture(0))
	for native := range int64(100) {
		observation, err := plan.Observe("brightness", time.Now().UTC(), json.RawMessage(strconv.FormatInt(native, 10)))
		want := strconv.FormatInt((native*100+49)/99, 10)
		if err != nil || string(observation.Value) != want {
			t.Fatalf("observe native %d = %s, %v; want %s", native, observation.Value, err, want)
		}
	}
}

func TestBrightnessPlanRejectsInvalidPercentageRequests(t *testing.T) {
	t.Parallel()
	plan := fixtureBrightnessPlan(t, levelPairFixture(0))
	for _, invalid := range []string{`{"value":-1}`, `{"value":101}`, `{"value":50.5}`} {
		if _, err := plan.PrepareSet(json.RawMessage(invalid)); err == nil {
			t.Fatalf("accepted %s", invalid)
		}
	}
}
