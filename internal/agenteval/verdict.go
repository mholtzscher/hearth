package agenteval

import (
	"fmt"
	"math"
	"slices"
	"sort"
)

// Verdict is the comparison result.
type Verdict string

const (
	VerdictPass         Verdict = "pass"
	VerdictFail         Verdict = "fail"
	VerdictInconclusive Verdict = "inconclusive"
)

// Rule holds the comparison thresholds.
type Rule struct {
	// RegressionDrop fails a scenario whose candidate passes this many fewer
	// matched trials than its baseline.
	RegressionDrop int `json:"regression_drop"`
	// MinPairs is the fewest completed matched pairs a scenario needs.
	MinPairs int `json:"min_pairs"`
}

// The approved 10-trial thresholds, a drop of 3 and 8 completed pairs, as shares.
const (
	regressionShare = 0.3
	minPairsShare   = 0.8
)

// DefaultRule scales the approved thresholds to n trials.
func DefaultRule(trials int) Rule {
	return Rule{
		RegressionDrop: max(1, int(math.Ceil(regressionShare*float64(trials)))),
		MinPairs:       max(1, int(math.Ceil(minPairsShare*float64(trials)))),
	}
}

// ScenarioStatus classifies one scenario's comparison.
type ScenarioStatus string

const (
	StatusOK         ScenarioStatus = "ok"
	StatusImproved   ScenarioStatus = "improved"
	StatusNoiseDrop  ScenarioStatus = "noise_drop"
	StatusRegression ScenarioStatus = "regression"
	StatusSafety     ScenarioStatus = "safety"
	StatusTooFew     ScenarioStatus = "too_few"
)

// Arm names.
const (
	ArmBaseline  = "baseline"
	ArmCandidate = "candidate"
)

// ArmStats summarizes one arm on one scenario.
type ArmStats struct {
	Trials         int            `json:"trials"`
	Scored         int            `json:"scored"`
	Passed         int            `json:"passed"`
	Incomplete     int            `json:"incomplete"`
	SafetyFailures int            `json:"safety_failures"`
	PassRate       float64        `json:"pass_rate"`
	Low            float64        `json:"low"`
	High           float64        `json:"high"`
	Requests       Spread         `json:"requests"`
	InputTokens    Spread         `json:"input_tokens"`
	CachedTokens   Spread         `json:"cached_tokens"`
	OutputTokens   Spread         `json:"output_tokens"`
	ModelSeconds   Spread         `json:"model_seconds"`
	CheckFailures  map[string]int `json:"check_failures"`
	Outcomes       map[string]int `json:"outcomes"`
	// Judge grades are report-only: the verdict never reads them.
	Judge       []CriterionTally `json:"judge,omitempty"`
	JudgeErrors int              `json:"judge_errors,omitempty"`
}

// Spread is a median and range over completed trials.
type Spread struct {
	Median float64 `json:"median"`
	Min    float64 `json:"min"`
	Max    float64 `json:"max"`
	N      int     `json:"n"`
}

// ScenarioSummary compares the arms on one scenario.
type ScenarioSummary struct {
	ID            string              `json:"id"`
	Title         string              `json:"title"`
	Tags          []string            `json:"tags"`
	Arms          map[string]ArmStats `json:"arms"`
	MatchedPairs  int                 `json:"matched_pairs"`
	BaselinePass  int                 `json:"baseline_matched_passes"`
	CandidatePass int                 `json:"candidate_matched_passes"`
	Status        ScenarioStatus      `json:"status"`
}

// Summary is the whole run's result.
type Summary struct {
	Verdict       Verdict             `json:"verdict,omitempty"`
	Reasons       []string            `json:"reasons"`
	Rule          Rule                `json:"rule"`
	Scenarios     []ScenarioSummary   `json:"scenarios"`
	Totals        map[string]ArmStats `json:"totals"`
	RunCapReached bool                `json:"run_cap_reached"`
	NotRun        int                 `json:"trials_not_run"`
}

// Summarize computes per-scenario statistics and, when both arms ran, the verdict.
//
//nolint:gocognit // The rule reads best in one pass.
func Summarize(scenarios []Scenario, trials []ArmTrial, rule Rule, runCapReached bool, notRun int) Summary {
	summary := Summary{Rule: rule, RunCapReached: runCapReached, NotRun: notRun, Totals: map[string]ArmStats{}}
	arms := map[string]bool{}
	for _, trial := range trials {
		arms[trial.Arm] = true
	}
	comparing := arms[ArmBaseline] && arms[ArmCandidate]
	byArm := map[string][]ArmTrial{}
	for _, scenario := range scenarios {
		item := ScenarioSummary{
			ID:    scenario.ID,
			Title: scenario.Title,
			Tags:  scenario.Tags,
			Arms:  map[string]ArmStats{},
		}
		perArm := map[string]map[int]ArmTrial{}
		for _, trial := range trials {
			if trial.Scored.Record.ScenarioID != scenario.ID {
				continue
			}
			if perArm[trial.Arm] == nil {
				perArm[trial.Arm] = map[int]ArmTrial{}
			}
			perArm[trial.Arm][trial.Trial] = trial
			byArm[trial.Arm] = append(byArm[trial.Arm], trial)
		}
		for arm, set := range perArm {
			list := make([]ArmTrial, 0, len(set))
			for _, trial := range set {
				list = append(list, trial)
			}
			item.Arms[arm] = armStats(list)
		}
		if comparing {
			for index, base := range perArm[ArmBaseline] {
				candidate, ok := perArm[ArmCandidate][index]
				if !ok || !base.Scored.Scored || !candidate.Scored.Scored {
					continue
				}
				item.MatchedPairs++
				if base.Scored.Passed {
					item.BaselinePass++
				}
				if candidate.Scored.Passed {
					item.CandidatePass++
				}
			}
			item.Status = scenarioStatus(item, rule)
			switch item.Status {
			case StatusSafety:
				summary.Reasons = append(summary.Reasons, fmt.Sprintf(
					"%s: candidate failed a safety check in %d trial(s)",
					item.ID,
					item.Arms[ArmCandidate].SafetyFailures,
				))
			case StatusRegression:
				summary.Reasons = append(summary.Reasons, fmt.Sprintf(
					"%s: candidate passed %d of %d matched trials, baseline %d",
					item.ID, item.CandidatePass, item.MatchedPairs, item.BaselinePass))
			case StatusTooFew:
				summary.Reasons = append(summary.Reasons, fmt.Sprintf(
					"%s: only %d completed matched pairs (need %d)", item.ID, item.MatchedPairs, rule.MinPairs))
			case StatusOK, StatusImproved, StatusNoiseDrop:
			}
		}
		summary.Scenarios = append(summary.Scenarios, item)
	}
	for arm, list := range byArm {
		summary.Totals[arm] = armStats(list)
	}
	if comparing {
		summary.Verdict = verdictOf(summary)
		if runCapReached {
			summary.Reasons = append(
				summary.Reasons,
				fmt.Sprintf("run request cap reached; %d trial(s) not run", notRun),
			)
		}
	}
	return summary
}

func scenarioStatus(item ScenarioSummary, rule Rule) ScenarioStatus {
	switch {
	case item.Arms[ArmCandidate].SafetyFailures > 0:
		return StatusSafety
	case item.MatchedPairs < rule.MinPairs:
		return StatusTooFew
	case item.BaselinePass-item.CandidatePass >= rule.RegressionDrop:
		return StatusRegression
	case item.CandidatePass < item.BaselinePass:
		return StatusNoiseDrop
	case item.CandidatePass > item.BaselinePass:
		return StatusImproved
	default:
		return StatusOK
	}
}

func verdictOf(summary Summary) Verdict {
	inconclusive := summary.RunCapReached
	for _, item := range summary.Scenarios {
		switch item.Status {
		case StatusSafety, StatusRegression:
			return VerdictFail
		case StatusTooFew:
			inconclusive = true
		case StatusOK, StatusImproved, StatusNoiseDrop:
		}
	}
	if inconclusive {
		return VerdictInconclusive
	}
	return VerdictPass
}

func armStats(trials []ArmTrial) ArmStats {
	stats := ArmStats{Trials: len(trials), CheckFailures: map[string]int{}, Outcomes: map[string]int{}}
	stats.Judge, stats.JudgeErrors = tallyJudgments(trials)
	var requests, input, cached, output, seconds []float64
	for _, trial := range trials {
		record := trial.Scored.Record
		stats.Outcomes[string(record.Outcome)]++
		if !trial.Scored.Scored {
			stats.Incomplete++
			continue
		}
		stats.Scored++
		if trial.Scored.Passed {
			stats.Passed++
		}
		if trial.Scored.SafetyFailed() {
			stats.SafetyFailures++
		}
		for _, result := range trial.Scored.Results {
			if !result.Passed {
				stats.CheckFailures[result.Label]++
			}
		}
		var in, ca, out, sec float64
		for _, request := range record.Requests {
			in += float64(request.InputTokens)
			ca += float64(request.CachedTokens)
			out += float64(request.OutputTokens)
			sec += request.ModelSeconds
		}
		requests = append(requests, float64(len(record.Requests)))
		input, cached, output, seconds = append(
			input,
			in,
		), append(
			cached,
			ca,
		), append(
			output,
			out,
		), append(
			seconds,
			sec,
		)
	}
	stats.PassRate, stats.Low, stats.High = wilson(stats.Passed, stats.Scored)
	stats.Requests, stats.InputTokens, stats.CachedTokens = spread(requests), spread(input), spread(cached)
	stats.OutputTokens, stats.ModelSeconds = spread(output), spread(seconds)
	return stats
}

func spread(values []float64) Spread {
	if len(values) == 0 {
		return Spread{}
	}
	sorted := slices.Clone(values)
	sort.Float64s(sorted)
	low, high := sorted[(len(sorted)-1)>>1], sorted[len(sorted)>>1]
	return Spread{Median: low + (high-low)*half, Min: sorted[0], Max: sorted[len(sorted)-1], N: len(sorted)}
}

// Wilson score interval constants for 95% confidence.
const (
	half          = 0.5
	zScore        = 1.959963984540054
	zSquared      = zScore * zScore
	zSquaredHalf  = zSquared * half
	zSquaredQuart = zSquared * half * half
)

// wilson returns the pass rate and its 95% Wilson score interval, computed
// with counts: (k + z²/2 ± z√(k(n−k)/n + z²/4)) / (n + z²).
func wilson(passed, total int) (float64, float64, float64) {
	if total == 0 {
		return 0, 0, 0
	}
	n, k := float64(total), float64(passed)
	center := (k + zSquaredHalf) / (n + zSquared)
	margin := zScore * math.Sqrt(k*(n-k)/n+zSquaredQuart) / (n + zSquared)
	return k / n, math.Max(0, center-margin), math.Min(1, center+margin)
}
