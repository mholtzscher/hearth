package dbload

import (
	"fmt"
	"math/rand/v2"
)

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
	case "bounded-read":
		g.ThresholdMS = 250
	case "write", "maintenance":
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
			b.Status != "complete" ||
			b.P95MS <= 0 {
			return g
		}
		values = append(values, b.P95MS)
	}
	//nolint:gosec // Reproducible bootstrap sampling, not cryptography.
	rng := rand.New(rand.NewPCG(uint64(seed), 0x4e50))
	medians := make([]float64, 10000)
	sample := make([]float64, len(values))
	for i := range medians {
		for j := range sample {
			sample[j] = values[rng.IntN(len(values))]
		}
		medians[i] = Percentile(sample, 0.5)
	}
	g.LowerMS = Percentile(medians, 0.025)
	g.UpperMS = Percentile(medians, 0.975)
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
