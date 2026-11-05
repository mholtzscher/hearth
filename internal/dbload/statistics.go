package dbload

import (
	"cmp"
	"errors"
	"fmt"
	"math"
	"math/rand/v2"
	"slices"
	"strings"
)

const (
	confirmationPairs = 20
	confirmationSlots = 3
	familyAlpha       = 0.05
	minimumSamples    = 200
	minimumGain       = 0.10
	equivalenceLow    = 0.9
	equivalenceHigh   = 1.1
	bootstrapSamples  = 10_000
	signSamples       = 100_000
	maxReportedIssues = 5
	confidenceLower   = 0.025
	confidenceUpper   = 0.975
	percentileMedian  = 0.50
	percentileP95     = 0.95
	percentileP99     = 0.99
	percentScale      = 100
	signChoices       = 2
	medianDivisor     = 2
	blocksPerPair     = 2

	variantBaseline  = "baseline"
	variantCandidate = "candidate"

	verdictImproved     = "improved"
	verdictRegressed    = "regressed"
	verdictNoDifference = "no practically meaningful difference detected"
	verdictInconclusive = "inconclusive"

	seedStreamBootstrap = 0x9e3779b97f4a7c15
	seedStreamSign      = 0xd1b54a32d192ed03
)

// Percentile returns the nearest-rank percentile of values without changing the
// caller's slice. The rank is ceil(quantile*n), clamped to the sample range, so
// 0.95 over 20 samples selects the 19th value and 0.99 selects the 20th. The
// caller supplies finite values. An empty sample or NaN quantile yields NaN.
func Percentile(values []float64, quantile float64) float64 {
	if len(values) == 0 || math.IsNaN(quantile) {
		return math.NaN()
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	if quantile <= 0 {
		return ordered[0]
	}
	if quantile >= 1 {
		return ordered[len(ordered)-1]
	}
	rank := int(math.Ceil(quantile * float64(len(ordered))))
	rank = max(rank, 1)
	rank = min(rank, len(ordered))
	return ordered[rank-1]
}

// Analyze evaluates one preregistered confirmation family. Cells must equal
// Block.Case exactly; the caller makes each case identifier unique for its
// parameter schedule and concurrency, and a registered cell must not mix
// scenarios or concurrency values. Every registered cell produces a
// Comparison, including missing or invalid evidence, which is retained with a
// raw p-value of 1 for the family-wide Holm adjustment. Only complete 20-pair
// cells with exact samples, no failures, equal block work, and consistent AB/BA
// metadata can receive an improved, regressed, or equivalence verdict.
func Analyze(reg Registration, blocks []Block) ([]Comparison, error) {
	if err := validateRegistration(reg); err != nil {
		return nil, err
	}
	registered := make(map[string]struct{}, len(reg.Cells))
	for _, cell := range reg.Cells {
		registered[cell] = struct{}{}
	}
	byCase := make(map[string][]Block, len(reg.Cells))
	for _, block := range blocks {
		if _, ok := registered[block.Case]; ok {
			byCase[block.Case] = append(byCase[block.Case], block)
		}
	}
	comparisons := make([]Comparison, len(reg.Cells))
	for index, cell := range reg.Cells {
		comparisons[index] = analyzeCell(reg, cell, byCase[cell])
	}
	applyHolm(comparisons)
	for index := range comparisons {
		if comparisons[index].Pairs == reg.Pairs && comparisons[index].Ratio > 0 {
			comparisons[index].Verdict, comparisons[index].Reason = decideVerdict(comparisons[index], reg)
		}
	}
	return comparisons, nil
}

func validateRegistration(reg Registration) error {
	if reg.FormatVersion != FormatVersion || reg.ContentHash == "" {
		return errors.New("registration format/hash is missing or unsupported")
	}
	if len(reg.Cells) == 0 {
		return errors.New("registration has no cells")
	}
	seen := make(map[string]struct{}, len(reg.Cells))
	for _, cell := range reg.Cells {
		if strings.TrimSpace(cell) == "" {
			return errors.New("registration contains an empty cell")
		}
		if _, ok := seen[cell]; ok {
			return fmt.Errorf("registration cell %q is duplicated", cell)
		}
		seen[cell] = struct{}{}
	}
	if reg.Pairs != confirmationPairs {
		return fmt.Errorf("registration pairs = %d, want exactly %d", reg.Pairs, confirmationPairs)
	}
	if reg.SamplesPerCell < minimumSamples {
		return fmt.Errorf(
			"registration samples per cell = %d, want at least %d",
			reg.SamplesPerCell,
			minimumSamples,
		)
	}
	if !(reg.MinimumGain >= minimumGain && reg.MinimumGain < 1) {
		return fmt.Errorf(
			"registration minimum gain = %g, want at least %g and below 1",
			reg.MinimumGain,
			minimumGain,
		)
	}
	if !(reg.Alpha > 0 && reg.Alpha < 1) {
		return fmt.Errorf("registration alpha = %g, want between 0 and 1", reg.Alpha)
	}
	return nil
}

type pairGroup struct {
	baseline  []int
	candidate []int
}

type cellClassification struct {
	validPairs []pairedBlock
	before     []float64
	after      []float64
	issues     issueList
}

type pairedBlock struct {
	baseline  Block
	candidate Block
}

type issueList struct {
	items   []string
	omitted int
}

func (issues *issueList) add(message string) {
	if len(issues.items) < maxReportedIssues {
		issues.items = append(issues.items, message)
		return
	}
	issues.omitted++
}

func (issues *issueList) reason() string {
	if len(issues.items) == 0 {
		return ""
	}
	reason := strings.Join(issues.items, "; ")
	if issues.omitted > 0 {
		reason += fmt.Sprintf("; and %d more issue(s)", issues.omitted)
	}
	return reason
}

func analyzeCell(reg Registration, cell string, blocks []Block) Comparison {
	comparison := Comparison{
		Case:    cell,
		Verdict: verdictInconclusive,
	}
	if len(blocks) == 0 {
		comparison.PValue = 1
		comparison.Reason = "no recorded blocks for registered cell"
		return comparison
	}
	classification := classifyCell(reg, blocks)
	comparison.Pairs = len(classification.validPairs)
	comparison.BeforeMS, comparison.BeforeMin, comparison.BeforeMax = distribution(classification.before)
	comparison.AfterMS, comparison.AfterMin, comparison.AfterMax = distribution(classification.after)
	if reason := classification.issues.reason(); reason != "" ||
		len(classification.validPairs) != reg.Pairs {
		comparison.PValue = 1
		if reason == "" {
			reason = fmt.Sprintf(
				"valid pairs = %d, want exactly %d",
				len(classification.validPairs),
				reg.Pairs,
			)
		}
		comparison.Reason = reason
		return comparison
	}

	comparison.Ratio, comparison.Lower, comparison.Upper, comparison.PValue = analyzePairs(
		classification.validPairs,
		reg.ResamplingSeed,
	)
	return comparison
}

func classifyCell(reg Registration, blocks []Block) cellClassification {
	var classification cellClassification
	groups := make(map[int]*pairGroup)
	valid := make(map[int]bool, len(blocks))
	scenarios := make(map[string]struct{})
	concurrencies := make(map[int]struct{})
	baselineOrders := make(map[int]string)
	pairIDs := make([]int, 0, len(blocks)/blocksPerPair)

	for index, block := range blocks {
		scenarios[block.Scenario] = struct{}{}
		concurrencies[block.Concurrency] = struct{}{}
		if block.Pair <= 0 {
			classification.issues.add(
				fmt.Sprintf("block with non-positive pair %d", block.Pair),
			)
			continue
		}
		variant := normalizeVariant(block.Variant)
		if variant == "" {
			classification.issues.add(
				fmt.Sprintf("pair %d has unknown variant %q", block.Pair, block.Variant),
			)
			continue
		}
		group, ok := groups[block.Pair]
		if !ok {
			group = &pairGroup{}
			groups[block.Pair] = group
			pairIDs = append(pairIDs, block.Pair)
		}
		if variant == variantBaseline {
			group.baseline = append(group.baseline, index)
			if order := normalizeOrder(block.Order); order != "" {
				baselineOrders[block.Pair] = order
			}
		} else {
			group.candidate = append(group.candidate, index)
		}
		if message := validateBlock(reg, block); message != "" {
			classification.issues.add(
				fmt.Sprintf("pair %d %s %s", block.Pair, variant, message),
			)
			continue
		}
		valid[index] = true
	}

	checkCellMetadata(&classification.issues, scenarios, concurrencies)
	slices.Sort(pairIDs)
	if len(pairIDs) != reg.Pairs {
		classification.issues.add(
			fmt.Sprintf("recorded pairs = %d, want exactly %d", len(pairIDs), reg.Pairs),
		)
	}
	for _, pair := range pairIDs {
		classification.addPair(pair, groups[pair], blocks, valid)
	}
	checkAlternatingOrders(&classification.issues, pairIDs, baselineOrders)
	if len(classification.validPairs) != reg.Pairs {
		classification.issues.add(
			fmt.Sprintf(
				"valid pairs = %d, want exactly %d",
				len(classification.validPairs),
				reg.Pairs,
			),
		)
	}
	return classification
}

func (classification *cellClassification) addPair(
	pair int,
	group *pairGroup,
	blocks []Block,
	valid map[int]bool,
) {
	if group == nil {
		classification.issues.add(fmt.Sprintf("pair %d is missing", pair))
		return
	}
	if len(group.baseline) != 1 || len(group.candidate) != 1 {
		if len(group.baseline) > 1 || len(group.candidate) > 1 {
			classification.issues.add(
				fmt.Sprintf(
					"pair %d has duplicate blocks (baseline=%d, candidate=%d)",
					pair,
					len(group.baseline),
					len(group.candidate),
				),
			)
			return
		}
		classification.issues.add(
			fmt.Sprintf(
				"pair %d is missing a %s block",
				pair,
				missingVariant(group),
			),
		)
		return
	}
	baselineIndex := group.baseline[0]
	candidateIndex := group.candidate[0]
	if !valid[baselineIndex] || !valid[candidateIndex] {
		return
	}
	baseline := blocks[baselineIndex]
	candidate := blocks[candidateIndex]
	classification.before = append(classification.before, baseline.P95MS)
	classification.after = append(classification.after, candidate.P95MS)
	if baseline.Work != candidate.Work {
		classification.issues.add(
			fmt.Sprintf(
				"pair %d block work differs (baseline=%d, candidate=%d)",
				pair,
				baseline.Work,
				candidate.Work,
			),
		)
		return
	}
	if normalizeOrder(baseline.Order) != normalizeOrder(candidate.Order) {
		classification.issues.add(
			fmt.Sprintf(
				"pair %d has inconsistent whole-pair AB/BA order (baseline=%q, candidate=%q)",
				pair,
				baseline.Order,
				candidate.Order,
			),
		)
		return
	}
	classification.validPairs = append(
		classification.validPairs,
		pairedBlock{baseline: baseline, candidate: candidate},
	)
}

func missingVariant(group *pairGroup) string {
	if len(group.baseline) == 0 {
		return variantBaseline
	}
	return variantCandidate
}

func checkCellMetadata(
	issues *issueList,
	scenarios map[string]struct{},
	concurrencies map[int]struct{},
) {
	if len(concurrencies) != 1 {
		values := make([]int, 0, len(concurrencies))
		for concurrency := range concurrencies {
			values = append(values, concurrency)
		}
		slices.Sort(values)
		issues.add(fmt.Sprintf("cell mixes concurrency values %v", values))
	}
	if len(scenarios) != 1 {
		values := make([]string, 0, len(scenarios))
		for scenario := range scenarios {
			values = append(values, scenario)
		}
		slices.Sort(values)
		issues.add(fmt.Sprintf("cell mixes scenarios %v", values))
	}
}

func checkAlternatingOrders(
	issues *issueList,
	pairIDs []int,
	baselineOrders map[int]string,
) {
	for index := 1; index < len(pairIDs); index++ {
		previous, previousOK := baselineOrders[pairIDs[index-1]]
		current, currentOK := baselineOrders[pairIDs[index]]
		if previousOK && currentOK && previous == current {
			issues.add(
				fmt.Sprintf(
					"baseline order %s repeats between pairs %d and %d",
					previous,
					pairIDs[index-1],
					pairIDs[index],
				),
			)
		}
	}
}

func validateBlock(reg Registration, block Block) string {
	switch {
	case block.RegistrationHash != reg.ContentHash:
		return "registration hash does not match the frozen family"
	case block.Concurrency <= 0:
		return "concurrency is not positive"
	case normalizeOrder(block.Order) == "":
		return fmt.Sprintf("order %q is not AB or BA", block.Order)
	case !completeStatus(block.Status):
		return fmt.Sprintf("status %q is not complete", block.Status)
	case block.Attempted != reg.SamplesPerCell:
		return fmt.Sprintf(
			"attempted %d operations, want exactly %d",
			block.Attempted,
			reg.SamplesPerCell,
		)
	case block.Completed != block.Attempted:
		return fmt.Sprintf(
			"completed %d of %d attempts",
			block.Completed,
			block.Attempted,
		)
	case block.Errors != 0:
		return fmt.Sprintf("recorded %d errors", block.Errors)
	case block.Timeouts != 0:
		return fmt.Sprintf("recorded %d timeouts", block.Timeouts)
	case block.Work < 0:
		return "recorded negative work"
	case !positiveFinite(block.P95MS):
		return "p95 is missing or non-positive"
	case !orderedQuantiles(block):
		return "p50/p95/p99 values are not ordered"
	}
	return ""
}

func completeStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case statusComplete, "completed", "ok", "valid", "success", "passed":
		return true
	default:
		return false
	}
}

func positiveFinite(value float64) bool {
	return value > 0 && !math.IsInf(value, 0) && !math.IsNaN(value)
}

func orderedQuantiles(block Block) bool {
	if block.P50MS > 0 && block.P50MS > block.P95MS {
		return false
	}
	return !(block.P99MS > 0 && block.P99MS < block.P95MS)
}

func normalizeVariant(variant string) string {
	switch strings.ToLower(strings.TrimSpace(variant)) {
	case variantBaseline:
		return variantBaseline
	case variantCandidate:
		return variantCandidate
	default:
		return ""
	}
}

func normalizeOrder(order string) string {
	switch strings.ToUpper(strings.TrimSpace(order)) {
	case "AB":
		return "AB"
	case "BA":
		return "BA"
	default:
		return ""
	}
}

func analyzePairs(pairs []pairedBlock, seed int64) (float64, float64, float64, float64) {
	logs := make([]float64, len(pairs))
	for index, pair := range pairs {
		logs[index] = math.Log(pair.candidate.P95MS / pair.baseline.P95MS)
	}
	ratio := math.Exp(mean(logs))
	lower, upper := bootstrapInterval(logs, seed)
	pValue := signRandomization(logs, seed)
	return ratio, lower, upper, pValue
}

func mean(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	total := 0.0
	for _, value := range values {
		total += value
	}
	return total / float64(len(values))
}

func bootstrapInterval(logs []float64, seed int64) (float64, float64) {
	//nolint:gosec // Deterministic Monte Carlo resampling, not cryptography.
	rng := rand.New(rand.NewPCG(uint64(seed), seedStreamBootstrap))
	samples := make([]float64, bootstrapSamples)
	for sample := range samples {
		total := 0.0
		for range logs {
			total += logs[rng.IntN(len(logs))]
		}
		samples[sample] = math.Exp(total / float64(len(logs)))
	}
	return Percentile(samples, confidenceLower), Percentile(samples, confidenceUpper)
}

func signRandomization(logs []float64, seed int64) float64 {
	//nolint:gosec // Deterministic Monte Carlo sign flips, not cryptography.
	rng := rand.New(rand.NewPCG(uint64(seed), seedStreamSign))
	observed := math.Abs(mean(logs))
	extreme := 0
	for range signSamples {
		total := 0.0
		for _, value := range logs {
			if rng.IntN(signChoices) == 0 {
				total -= value
			} else {
				total += value
			}
		}
		if math.Abs(total/float64(len(logs))) >= observed {
			extreme++
		}
	}
	return float64(extreme+1) / float64(signSamples+1)
}

func distribution(values []float64) (float64, float64, float64) {
	if len(values) == 0 {
		return 0, 0, 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	middle := len(ordered) / medianDivisor
	var median float64
	if len(ordered)%medianDivisor == 0 {
		median = (ordered[middle-1] + ordered[middle]) / medianDivisor
	} else {
		median = ordered[middle]
	}
	return median, ordered[0], ordered[len(ordered)-1]
}

func decideVerdict(comparison Comparison, reg Registration) (string, string) {
	improvement := 1 - comparison.Ratio
	if comparison.AdjustedP < reg.Alpha &&
		comparison.Upper < 1 &&
		improvement >= reg.MinimumGain {
		return verdictImproved, fmt.Sprintf(
			"candidate p95 geometric mean is %.1f%% lower; 95%% CI [%.3f, %.3f] excludes 1; Holm-adjusted p=%.3g < alpha=%.3g",
			improvement*percentScale,
			comparison.Lower,
			comparison.Upper,
			comparison.AdjustedP,
			reg.Alpha,
		)
	}
	if comparison.AdjustedP < reg.Alpha && comparison.Lower > 1 {
		return verdictRegressed, fmt.Sprintf(
			"candidate p95 geometric mean is %.1f%% higher; 95%% CI [%.3f, %.3f] excludes 1; Holm-adjusted p=%.3g < alpha=%.3g",
			(comparison.Ratio-1)*percentScale,
			comparison.Lower,
			comparison.Upper,
			comparison.AdjustedP,
			reg.Alpha,
		)
	}
	if comparison.Lower >= equivalenceLow && comparison.Upper <= equivalenceHigh {
		return verdictNoDifference, fmt.Sprintf(
			"95%% CI [%.3f, %.3f] lies wholly inside the %.1f-%.1f practical band",
			comparison.Lower,
			comparison.Upper,
			equivalenceLow,
			equivalenceHigh,
		)
	}
	if comparison.AdjustedP >= reg.Alpha {
		return verdictInconclusive, fmt.Sprintf(
			"Holm-adjusted p=%.3g does not clear alpha=%.3g; 95%% CI [%.3f, %.3f]",
			comparison.AdjustedP,
			reg.Alpha,
			comparison.Lower,
			comparison.Upper,
		)
	}
	if comparison.Upper < 1 {
		return verdictInconclusive, fmt.Sprintf(
			"observed improvement is below the %.0f%% practical threshold; 95%% CI [%.3f, %.3f]",
			reg.MinimumGain*percentScale,
			comparison.Lower,
			comparison.Upper,
		)
	}
	return verdictInconclusive, fmt.Sprintf(
		"95%% CI [%.3f, %.3f] does not establish a practical change",
		comparison.Lower,
		comparison.Upper,
	)
}

func applyHolm(comparisons []Comparison) {
	order := make([]int, len(comparisons))
	for index := range order {
		order[index] = index
	}
	slices.SortStableFunc(order, func(left, right int) int {
		return cmp.Compare(comparisons[left].PValue, comparisons[right].PValue)
	})
	previous := 0.0
	for rank, index := range order {
		adjusted := float64(len(comparisons)-rank) * comparisons[index].PValue
		adjusted = min(adjusted, 1)
		adjusted = max(adjusted, previous)
		comparisons[index].AdjustedP = adjusted
		previous = adjusted
	}
}
