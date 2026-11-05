package dbload_test

import (
	"math"
	"reflect"
	"strings"
	"testing"

	"github.com/mholtzscher/hearth/internal/dbload"
)

const (
	testSamples = 200
	testPairs   = 20
	testAlpha   = 0.05 / 3
)

func TestPercentileUsesNearestRankWithoutMutatingInput(t *testing.T) {
	t.Parallel()

	values := []float64{10, 1, 5, 2, 8}
	original := append([]float64(nil), values...)

	cases := []struct {
		name     string
		quantile float64
		want     float64
	}{
		{name: "minimum", quantile: 0, want: 1},
		{name: "median", quantile: 0.5, want: 5},
		{name: "p95", quantile: 0.95, want: 10},
		{name: "maximum", quantile: 1, want: 10},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			if got := dbload.Percentile(values, testCase.quantile); got != testCase.want {
				t.Fatalf("Percentile(%v, %g) = %g, want %g", values, testCase.quantile, got, testCase.want)
			}
		})
	}
	if !reflect.DeepEqual(values, original) {
		t.Fatalf("Percentile mutated input: got %v, want %v", values, original)
	}
}

func TestPercentileTailAndEmptyBehavior(t *testing.T) {
	t.Parallel()

	values := make([]float64, 100)
	for index := range values {
		values[index] = float64(index + 1)
	}
	if got := dbload.Percentile(values, 0.95); got != 95 {
		t.Fatalf("p95 = %g, want 95 (nearest rank)", got)
	}
	if got := dbload.Percentile(values, 0.99); got != 99 {
		t.Fatalf("p99 = %g, want 99 (nearest rank)", got)
	}
	if got := dbload.Percentile(values, 0.999); got != 100 {
		t.Fatalf("p99.9 = %g, want 100 (nearest rank)", got)
	}
	if got := dbload.Percentile(nil, 0.5); !math.IsNaN(got) {
		t.Fatalf("empty percentile = %g, want NaN", got)
	}
	if got := dbload.Percentile(values, math.NaN()); !math.IsNaN(got) {
		t.Fatalf("NaN quantile = %g, want NaN", got)
	}
}

func TestAnalyzeLargeEffectIsImproved(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	blocks := completePairs("entity-get", varyingRatios(0.45, 0.55))
	comparisons, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(comparisons) != 1 {
		t.Fatalf("comparisons = %d, want 1", len(comparisons))
	}
	comparison := comparisons[0]
	if comparison.Verdict != "improved" {
		t.Fatalf("verdict = %q, want improved (reason: %s)", comparison.Verdict, comparison.Reason)
	}
	if comparison.Pairs != testPairs {
		t.Fatalf("pairs = %d, want %d", comparison.Pairs, testPairs)
	}
	if comparison.Upper >= 1 || comparison.Lower >= comparison.Upper {
		t.Fatalf("interval = [%g, %g], want ordered and below 1", comparison.Lower, comparison.Upper)
	}
	if comparison.Ratio < 0.45 || comparison.Ratio > 0.55 {
		t.Fatalf("ratio = %g, want within the recorded 0.45-0.55 effect", comparison.Ratio)
	}
	if comparison.AdjustedP >= testAlpha {
		t.Fatalf("adjusted p = %g, want below %g", comparison.AdjustedP, testAlpha)
	}
	if comparison.BeforeMS <= 0 || comparison.AfterMS <= 0 {
		t.Fatalf("before/after medians = %g/%g, want recorded values", comparison.BeforeMS, comparison.AfterMS)
	}
	if comparison.AfterMS >= comparison.BeforeMS {
		t.Fatalf("after median %g should be below before median %g", comparison.AfterMS, comparison.BeforeMS)
	}
	if comparison.BeforeMin > comparison.BeforeMS || comparison.BeforeMS > comparison.BeforeMax {
		t.Fatalf(
			"before range [%g, %g] excludes median %g",
			comparison.BeforeMin,
			comparison.BeforeMax,
			comparison.BeforeMS,
		)
	}
	if blocks[0].Work != blocks[1].Work {
		t.Fatalf("fixture work differs within pair: %d/%d", blocks[0].Work, blocks[1].Work)
	}
}

func TestAnalyzeDistinguishesNarrowEquivalenceFromWideInconclusive(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	narrow := completePairs("entity-get", varyingRatios(0.98, 1.02))
	narrowComparison := analyzeSingle(t, registration, narrow)
	if narrowComparison.Verdict != "no practically meaningful difference detected" {
		t.Fatalf(
			"narrow verdict = %q (interval [%g, %g]), want no practical difference",
			narrowComparison.Verdict,
			narrowComparison.Lower,
			narrowComparison.Upper,
		)
	}
	if narrowComparison.Lower < 0.9 || narrowComparison.Upper > 1.1 {
		t.Fatalf("narrow interval [%g, %g] is not within 0.9-1.1", narrowComparison.Lower, narrowComparison.Upper)
	}

	wide := completePairs("entity-get", alternatingRatios(0.5, 1.5))
	wideComparison := analyzeSingle(t, registration, wide)
	if wideComparison.Verdict == "improved" || wideComparison.Verdict == "regressed" {
		t.Fatalf("wide verdict = %q, want inconclusive or equivalence", wideComparison.Verdict)
	}
	if wideComparison.Lower >= 0.9 && wideComparison.Upper <= 1.1 {
		t.Fatalf("wide interval [%g, %g] unexpectedly lies inside 0.9-1.1", wideComparison.Lower, wideComparison.Upper)
	}
	if wideComparison.Verdict != "inconclusive" {
		t.Fatalf("wide verdict = %q, want inconclusive", wideComparison.Verdict)
	}
}

func TestAnalyzeMissingPairsRemainInconclusive(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	blocks := completePairs("entity-get", constantRatios(0.5))
	blocks = blocks[:len(blocks)-1]
	comparison := analyzeSingle(t, registration, blocks)
	if comparison.Verdict != "inconclusive" {
		t.Fatalf("verdict = %q, want inconclusive", comparison.Verdict)
	}
	if comparison.Pairs != 19 {
		t.Fatalf("pairs = %d, want 19", comparison.Pairs)
	}
	if comparison.PValue != 1 || comparison.AdjustedP != 1 {
		t.Fatalf("p-values = %g/%g, want 1/1 for partial evidence", comparison.PValue, comparison.AdjustedP)
	}
	if !strings.Contains(comparison.Reason, "missing") && !strings.Contains(comparison.Reason, "want exactly") {
		t.Fatalf("reason = %q, want missing-pair explanation", comparison.Reason)
	}
	if comparison.BeforeMS <= 0 || comparison.AfterMS <= 0 {
		t.Fatalf("partial before/after context = %g/%g, want recorded medians", comparison.BeforeMS, comparison.AfterMS)
	}
}

func TestAnalyzeFailuresAndWorkMismatchAreInconclusive(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	blocks := completePairs("entity-get", constantRatios(0.5))
	blocks[0].Errors = 1
	blocks[2].Work++
	comparison := analyzeSingle(t, registration, blocks)
	if comparison.Verdict != "inconclusive" {
		t.Fatalf("verdict = %q, want inconclusive", comparison.Verdict)
	}
	if !strings.Contains(comparison.Reason, "errors") {
		t.Fatalf("reason = %q, want error count", comparison.Reason)
	}
	if !strings.Contains(comparison.Reason, "work differs") {
		t.Fatalf("reason = %q, want work mismatch", comparison.Reason)
	}
}

func TestAnalyzeExactSampleCountAndTimeoutAreRequired(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	short := completePairs("entity-get", constantRatios(0.5))
	short[1].Attempted--
	short[1].Completed--
	comparison := analyzeSingle(t, registration, short)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "want exactly") {
		t.Fatalf(
			"short-sample verdict = %q reason = %q, want exact-count inconclusive",
			comparison.Verdict,
			comparison.Reason,
		)
	}

	timedOut := completePairs("entity-get", constantRatios(0.5))
	timedOut[3].Timeouts = 1
	comparison = analyzeSingle(t, registration, timedOut)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "timeouts") {
		t.Fatalf("timeout verdict = %q reason = %q, want timeout inconclusive", comparison.Verdict, comparison.Reason)
	}
}

func TestAnalyzeKeepsDroppedRegisteredCellInHolmFamily(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get", "entity-list")
	blocks := completePairs("entity-get", constantRatios(0.5))
	comparisons, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(comparisons) != 2 {
		t.Fatalf("comparisons = %d, want 2 registered cells", len(comparisons))
	}
	present, missing := comparisons[0], comparisons[1]
	if present.Verdict != "improved" {
		t.Fatalf("present verdict = %q, want improved", present.Verdict)
	}
	wantAdjusted := math.Min(1, 2*present.PValue)
	if math.Abs(present.AdjustedP-wantAdjusted) > 1e-12 {
		t.Fatalf("present adjusted p = %g, want %g for a two-cell family", present.AdjustedP, wantAdjusted)
	}
	if missing.PValue != 1 || missing.AdjustedP != 1 {
		t.Fatalf("missing p-values = %g/%g, want 1/1", missing.PValue, missing.AdjustedP)
	}
	if missing.Verdict != "inconclusive" || !strings.Contains(missing.Reason, "no recorded blocks") {
		t.Fatalf("missing verdict = %q reason = %q", missing.Verdict, missing.Reason)
	}
	if !strings.Contains(missing.Case, "entity-list") {
		t.Fatalf("missing case = %q, want registered identity", missing.Case)
	}
}

func TestAnalyzeDuplicatePairsAndMetadataAreRejected(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")

	duplicated := completePairs("entity-get", constantRatios(0.5))
	duplicated = append(duplicated, duplicated[0])
	comparison := analyzeSingle(t, registration, duplicated)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "duplicate") {
		t.Fatalf(
			"duplicate verdict = %q reason = %q, want duplicate inconclusive",
			comparison.Verdict,
			comparison.Reason,
		)
	}

	repeatedOrder := completePairs("entity-get", constantRatios(0.5))
	for index := 0; index < len(repeatedOrder); index += 2 {
		repeatedOrder[index].Order = "AB"
		repeatedOrder[index+1].Order = "AB"
	}
	comparison = analyzeSingle(t, registration, repeatedOrder)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "repeats") {
		t.Fatalf(
			"order verdict = %q reason = %q, want alternating-order inconclusive",
			comparison.Verdict,
			comparison.Reason,
		)
	}

	mixedConcurrency := completePairs("entity-get", constantRatios(0.5))
	mixedConcurrency[1].Concurrency = 4
	comparison = analyzeSingle(t, registration, mixedConcurrency)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "mixes concurrency") {
		t.Fatalf(
			"concurrency verdict = %q reason = %q, want mixed-concurrency inconclusive",
			comparison.Verdict,
			comparison.Reason,
		)
	}
}

func TestAnalyzeRejectsInvalidRegistration(t *testing.T) {
	t.Parallel()

	valid := testRegistration("entity-get")
	cases := []struct {
		name   string
		mutate func(*dbload.Registration)
		want   string
	}{
		{name: "no cells", mutate: func(reg *dbload.Registration) { reg.Cells = nil }, want: "no cells"},
		{name: "empty cell", mutate: func(reg *dbload.Registration) { reg.Cells = []string{" "} }, want: "empty cell"},
		{
			name:   "duplicate cell",
			mutate: func(reg *dbload.Registration) { reg.Cells = []string{"a", "a"} },
			want:   "duplicated",
		},
		{name: "wrong pairs", mutate: func(reg *dbload.Registration) { reg.Pairs = 19 }, want: "pairs"},
		{name: "too few samples", mutate: func(reg *dbload.Registration) { reg.SamplesPerCell = 199 }, want: "samples"},
		{
			name:   "weak minimum gain",
			mutate: func(reg *dbload.Registration) { reg.MinimumGain = 0.05 },
			want:   "minimum gain",
		},
		{name: "bad alpha", mutate: func(reg *dbload.Registration) { reg.Alpha = 0 }, want: "alpha"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			registration := valid
			testCase.mutate(&registration)
			if _, err := dbload.Analyze(registration, nil); err == nil ||
				!strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("Analyze error = %v, want containing %q", err, testCase.want)
			}
		})
	}
}

func TestAnalyzeSeedReproducibility(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	blocks := completePairs("entity-get", varyingRatios(0.7, 1.1))
	first, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("first Analyze returned error: %v", err)
	}
	second, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("second Analyze returned error: %v", err)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("same seed produced different comparisons:\nfirst: %+v\nsecond: %+v", first, second)
	}

	registration.ResamplingSeed++
	different, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("different-seed Analyze returned error: %v", err)
	}
	if len(different) != 1 || different[0].PValue < 0 || different[0].PValue > 1 {
		t.Fatalf("different seed produced invalid p-value: %+v", different)
	}
}

func TestAnalyzeIgnoresUnexploredCasesAndInvalidBlocks(t *testing.T) {
	t.Parallel()

	registration := testRegistration("entity-get")
	blocks := completePairs("entity-get", constantRatios(0.5))
	blocks = append(blocks, completePairs("exploratory-list", constantRatios(2))...)
	comparisons, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(comparisons) != 1 || comparisons[0].Case != "entity-get" {
		t.Fatalf("comparisons = %+v, want only registered case", comparisons)
	}

	missingP95 := completePairs("entity-get", constantRatios(0.5))
	missingP95[2].P95MS = 0
	comparison := analyzeSingle(t, registration, missingP95)
	if comparison.Verdict != "inconclusive" || !strings.Contains(comparison.Reason, "p95") {
		t.Fatalf("missing p95 verdict = %q reason = %q", comparison.Verdict, comparison.Reason)
	}
}

func analyzeSingle(t *testing.T, registration dbload.Registration, blocks []dbload.Block) dbload.Comparison {
	t.Helper()
	comparisons, err := dbload.Analyze(registration, blocks)
	if err != nil {
		t.Fatalf("Analyze returned error: %v", err)
	}
	if len(comparisons) != 1 {
		t.Fatalf("comparisons = %d, want 1", len(comparisons))
	}
	return comparisons[0]
}

const testSuite = "family"

func testRegistration(cells ...string) dbload.Registration {
	return dbload.Registration{
		FormatVersion:  1,
		Slot:           1,
		BaselineHash:   "baseline-" + testSuite,
		CandidateHash:  "candidate-" + testSuite,
		SuiteHash:      "suite-" + testSuite,
		FixtureHash:    "fixture-" + testSuite,
		ScheduleHash:   "schedule-" + testSuite,
		Cells:          cells,
		Pairs:          testPairs,
		SamplesPerCell: testSamples,
		MinimumGain:    0.10,
		Alpha:          testAlpha,
		ContentHash:    "content-" + testSuite,
		ResamplingSeed: 424242,
	}
}

func completePairs(cell string, ratios []float64) []dbload.Block {
	blocks := make([]dbload.Block, 0, len(ratios)*2)
	for index, ratio := range ratios {
		pair := index + 1
		baselineP95 := 100 * (1 + 0.05*math.Sin(float64(index)))
		baselineOrder := "AB"
		candidateOrder := "AB"
		if pair%2 == 0 {
			baselineOrder = "BA"
			candidateOrder = "BA"
		}
		baseline := dbload.Block{
			Case:        cell,
			Scenario:    "population",
			Variant:     "baseline",
			Pair:        pair,
			Order:       baselineOrder,
			Concurrency: 1,
			Attempted:   testSamples,
			Completed:   testSamples,
			Work:        int64(1000 + index),
			P50MS:       baselineP95 * 0.75,
			P95MS:       baselineP95,
			P99MS:       baselineP95 * 1.3,
			WallMS:      baselineP95 * 20,
			PoolWaitMS:  1.5,
			Status:      "complete",
		}
		candidate := dbload.Block{
			Case:        cell,
			Scenario:    "population",
			Variant:     "candidate",
			Pair:        pair,
			Order:       candidateOrder,
			Concurrency: 1,
			Attempted:   testSamples,
			Completed:   testSamples,
			Work:        int64(1000 + index),
			P50MS:       baselineP95 * ratio * 0.6,
			P95MS:       baselineP95 * ratio,
			P99MS:       baselineP95 * ratio * 1.8,
			WallMS:      baselineP95 * ratio * 20,
			PoolWaitMS:  0.5,
			Status:      "complete",
		}
		blocks = append(blocks, baseline, candidate)
	}
	return blocks
}

func constantRatios(value float64) []float64 {
	ratios := make([]float64, testPairs)
	for index := range ratios {
		ratios[index] = value
	}
	return ratios
}

func varyingRatios(low, high float64) []float64 {
	ratios := make([]float64, testPairs)
	for index := range ratios {
		fraction := float64(index) / float64(len(ratios)-1)
		ratios[index] = low + (high-low)*fraction
	}
	return ratios
}

func alternatingRatios(low, high float64) []float64 {
	ratios := make([]float64, testPairs)
	for index := range ratios {
		if index%2 == 0 {
			ratios[index] = low
		} else {
			ratios[index] = high
		}
	}
	return ratios
}
