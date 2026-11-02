package devices

import (
	"encoding/json"
	"math/big"
	"sort"
	"strconv"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

type historyReduction struct {
	extreme     *big.Rat
	extremeJSON json.RawMessage
	sum         big.Rat
	count       int64
	seconds     float64
	transitions int
}

type historySummaryEngine struct {
	plan       historyPlan
	evaluator  *historyEvaluator
	ranges     []HistoryRange
	coverage   []HistoryDurationCoverage
	reductions [][]historyReduction
	known      bool
	matches    []bool
	at         time.Time
}

func (engine *historySummaryEngine) bucket(at time.Time) int {
	return sort.Search(len(engine.ranges), func(i int) bool { return at.Before(engine.ranges[i].To) })
}

func (engine *historySummaryEngine) addInterval(end time.Time) {
	for start := engine.at; start.Before(end); {
		i := engine.bucket(start)
		to := end
		if to.After(engine.ranges[i].To) {
			to = engine.ranges[i].To
		}
		seconds := historySeconds(start, to)
		if engine.known {
			engine.coverage[i].InferredKnownSeconds += seconds
		} else {
			engine.coverage[i].UnknownSeconds += seconds
		}
		for m, metric := range engine.plan.metrics {
			if _, ok := metric.metric.(HistoryDurationWhere); ok && engine.known && engine.matches[m] {
				engine.reductions[i][m].seconds += seconds
			}
		}
		start = to
	}
	engine.at = end
}

func (engine *historySummaryEngine) temporalValue(at time.Time, value exprvalue.Value, seed bool) error {
	if !seed {
		engine.addInterval(at)
	}
	for i, metric := range engine.plan.metrics {
		if metric.predicate == nil {
			continue
		}
		match, err := engine.evaluator.predicate(metric.predicate, "result.metrics."+metric.name+".where_expr", value)
		if err != nil {
			return err
		}
		if !seed && engine.known && !engine.matches[i] && match {
			if _, ok := metric.metric.(HistoryTransitionsInto); ok {
				engine.reductions[engine.bucket(at)][i].transitions++
			}
		}
		engine.matches[i] = match
	}
	engine.known = true
	return nil
}

func (engine *historySummaryEngine) numericValue(at time.Time, value exprvalue.Value) error {
	i := engine.bucket(at)
	for m, metric := range engine.plan.metrics {
		if metric.predicate != nil {
			continue
		}
		number := new(big.Rat)
		switch v := value.Data.(type) {
		case int64:
			number.SetInt64(v)
		case float64:
			number.SetFloat64(v)
		default:
			return historyError("expression_type", "result.metrics."+metric.name)
		}
		reduction := &engine.reductions[i][m]
		if reduction.extreme == nil {
			reduction.extreme = new(big.Rat).Set(number)
			reduction.extremeJSON = value.JSON
		} else {
			comparison := number.Cmp(reduction.extreme)
			_, minimum := metric.metric.(HistoryMinimum)
			_, maximum := metric.metric.(HistoryMaximum)
			if minimum && comparison < 0 || maximum && comparison > 0 {
				reduction.extreme.Set(number)
				reduction.extremeJSON = value.JSON
			}
		}
		reduction.sum.Add(&reduction.sum, number)
		reduction.count++
	}
	return nil
}

//nolint:gocognit // A single pass retains only the current effective value.
func makeHistorySummary(plan historyPlan, evaluator *historyEvaluator, window HistoryWindow) (HistoryResult, error) {
	spec, ok := plan.query.Result.(HistorySummary)
	if !ok {
		return nil, historyError("invalid_query", "result")
	}
	engine := historySummaryEngine{
		plan:      plan,
		evaluator: evaluator,
		at:        plan.query.Range.From,
		matches:   make([]bool, len(plan.metrics)),
	}
	if spec.Bucket == nil {
		engine.ranges = []HistoryRange{plan.query.Range}
	} else {
		for from := plan.query.Range.From; from.Before(plan.query.Range.To); {
			to := from.Add(spec.Bucket.Duration)
			if to.After(plan.query.Range.To) {
				to = plan.query.Range.To
			}
			engine.ranges = append(engine.ranges, HistoryRange{From: from, To: to})
			from = to
		}
	}
	engine.coverage = make([]HistoryDurationCoverage, len(engine.ranges))
	engine.reductions = make([][]historyReduction, len(engine.ranges))
	for i := range engine.reductions {
		engine.reductions[i] = make([]historyReduction, len(plan.metrics))
	}
	if window.Seed != nil {
		value, err := evaluator.selectValue(plan.selectProgram, *window.Seed)
		if err != nil {
			return nil, err
		}
		if err = engine.temporalValue(engine.at, value, true); err != nil {
			return nil, err
		}
	}
	var pending *historySelected
	for _, row := range window.Rows {
		value, err := evaluator.selectValue(plan.selectProgram, row)
		if err != nil {
			return nil, err
		}
		if err = engine.numericValue(row.ObservedAt, value); err != nil {
			return nil, err
		}
		if plan.temporal {
			if pending != nil && !pending.row.ObservedAt.Equal(row.ObservedAt) {
				if err = engine.temporalValue(pending.row.ObservedAt, pending.value, false); err != nil {
					return nil, err
				}
			}
			pending = &historySelected{row: row, value: value}
		}
	}
	if plan.temporal {
		if pending != nil {
			if err := engine.temporalValue(pending.row.ObservedAt, pending.value, false); err != nil {
				return nil, err
			}
		}
		engine.addInterval(plan.query.Range.To)
	}
	return engine.result(spec, window.Seed != nil)
}

//nolint:gocognit // Each closed metric variant has a distinct output rule.
func (engine *historySummaryEngine) result(spec HistorySummary, seeded bool) (HistoryResult, error) {
	var coverage *HistoryTemporalCoverage
	if engine.plan.temporal {
		coverage = &HistoryTemporalCoverage{SeedAvailable: seeded}
		for _, item := range engine.coverage {
			coverage.InferredKnownSeconds += item.InferredKnownSeconds
			coverage.UnknownSeconds += item.UnknownSeconds
		}
	}
	buckets := make([]HistorySummaryBucket, len(engine.ranges))
	valueBytes := 0
	for i, r := range engine.ranges {
		if err := engine.evaluator.ctx.Err(); err != nil {
			return nil, err
		}
		values := make(HistoryMetricValues, len(engine.plan.metrics))
		for m, metric := range engine.plan.metrics {
			reduction := &engine.reductions[i][m]
			result := json.RawMessage("null")
			switch metric.metric.(type) {
			case HistoryMinimum, HistoryMaximum:
				if reduction.count > 0 {
					result = reduction.extremeJSON
				}
			case HistorySampleMean:
				if reduction.count > 0 {
					mean := new(big.Rat).Quo(&reduction.sum, new(big.Rat).SetInt64(reduction.count))
					f, _ := mean.Float64()
					result = json.RawMessage(strconv.FormatFloat(f, 'g', -1, 64))
				}
			case HistoryDurationWhere:
				if engine.coverage[i].InferredKnownSeconds > 0 {
					result = json.RawMessage(strconv.FormatFloat(reduction.seconds, 'g', -1, 64))
				}
			case HistoryTransitionsInto:
				result = json.RawMessage(strconv.Itoa(reduction.transitions))
			}
			valueBytes += len(metric.name) + len(result)
			if valueBytes > HistoryMaxResultBytes {
				return nil, historyError("query_limit", "result")
			}
			values[metric.name] = result
		}
		buckets[i] = HistorySummaryBucket{Range: r, Metrics: values}
		if engine.plan.temporal {
			c := engine.coverage[i]
			buckets[i].Coverage = &c
		}
	}
	if spec.Bucket == nil {
		return HistorySummaryResult{Coverage: coverage, Metrics: buckets[0].Metrics}, nil
	}
	return HistoryBucketedSummaryResult{Coverage: coverage, Bucket: *spec.Bucket, Buckets: buckets}, nil
}
