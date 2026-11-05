package dbload

import (
	"fmt"
	"math/rand/v2"
)

const gateSeedStream = 0x4e50

// Gate evaluates a provisional absolute threshold separately from a speedup.
type Gate struct {
	Case        string  `json:"case"`
	Variant     string  `json:"variant"`
	Status      string  `json:"status"`
	ThresholdMS float64 `json:"threshold_ms"`
	LowerMS     float64 `json:"lower_ms"`
	UpperMS     float64 `json:"upper_ms"`
	Reason      string  `json:"reason"`
}

// EvaluateGate uses independent run blocks, not individual requests, for uncertainty.
func EvaluateGate(blocks []Block, seed int64) Gate {
	g := Gate{
		Status: "inconclusive",
		Reason: "requires 20 complete concurrency-1 blocks, at least 200 attempts each, without errors",
	}
	if len(blocks) != ConfirmationPairs {
		return g
	}
	first := blocks[0]
	g.Case = first.Case
	g.Variant = first.Variant
	switch first.Class {
	case classBoundedRead:
		g.ThresholdMS = 250
	case classWrite, classMaintenance:
		g.ThresholdMS = 1000
	default:
		g.Status = "not applicable"
		g.Reason = "unbounded work has no provisional interactive gate"
		return g
	}
	values := make([]float64, 0, len(blocks))
	for _, b := range blocks {
		if b.Case != first.Case || b.Variant != first.Variant || b.Scenario != first.Scenario || b.Concurrency != 1 ||
			b.Attempted < ConfirmationSamples ||
			b.Completed != b.Attempted ||
			b.Errors != 0 ||
			b.Timeouts != 0 ||
			b.Status != statusComplete ||
			b.P95MS <= 0 {
			return g
		}
		values = append(values, b.P95MS)
	}
	//nolint:gosec // Reproducible bootstrap sampling, not cryptography.
	rng := rand.New(rand.NewPCG(uint64(seed), gateSeedStream))
	medians := make([]float64, bootstrapSamples)
	sample := make([]float64, len(values))
	for i := range medians {
		for j := range sample {
			sample[j] = values[rng.IntN(len(values))]
		}
		medians[i] = Percentile(sample, percentileMedian)
	}
	g.LowerMS = Percentile(medians, confidenceLower)
	g.UpperMS = Percentile(medians, confidenceUpper)
	g.Status = "at threshold / inconclusive"
	if g.UpperMS < g.ThresholdMS {
		g.Status = "pass"
	} else if g.LowerMS > g.ThresholdMS {
		g.Status = "fail"
	}
	g.Reason = fmt.Sprintf(
		"median block p95 bootstrap95%%CI [%.3f,%.3f]ms versus provisional %.0fms",
		g.LowerMS,
		g.UpperMS,
		g.ThresholdMS,
	)
	return g
}
