package devices

import (
	"reflect"
	"time"

	"github.com/mholtzscher/hearth/internal/platform/exprvalue"
)

// Seconds is computed without time.Sub's roughly 292-year saturation.
func historySeconds(from, to time.Time) float64 {
	return float64(to.Unix()-from.Unix()) + float64(to.Nanosecond()-from.Nanosecond())/1e9
}

//nolint:gocognit,gocritic // Interval coalescing and output accounting share the same state.
func makeHistoryTimeline(
	plan historyPlan,
	evaluator *historyEvaluator,
	window HistoryWindow,
) (HistoryTimelineResult, error) {
	r := plan.query.Range
	result := HistoryTimelineResult{
		Coverage:  HistoryTemporalCoverage{SeedAvailable: window.Seed != nil},
		Intervals: make([]HistoryInterval, 0),
	}
	var current *exprvalue.Value
	if window.Seed != nil {
		value, err := evaluator.selectValue(plan.selectProgram, *window.Seed)
		if err != nil {
			return result, err
		}
		current = &value
	}
	start := r.From
	var previous *exprvalue.Value
	valueBytes := 0
	appendInterval := func(end time.Time) error {
		if !start.Before(end) {
			return nil
		}
		seconds := historySeconds(start, end)
		if current == nil {
			result.Coverage.UnknownSeconds += seconds
		} else {
			result.Coverage.InferredKnownSeconds += seconds
		}
		if len(result.Intervals) > 0 &&
			((current == nil && previous == nil) || (current != nil && previous != nil && reflect.DeepEqual(current.Data, previous.Data))) {
			last := len(result.Intervals) - 1
			switch interval := result.Intervals[last].(type) {
			case HistoryValueInterval:
				interval.Range.To = end
				result.Intervals[last] = interval
			case HistoryUnknownInterval:
				interval.Range.To = end
				result.Intervals[last] = interval
			}
		} else if current == nil {
			result.Intervals = append(
				result.Intervals,
				HistoryUnknownInterval{Range: HistoryRange{From: start, To: end}},
			)
		} else {
			valueBytes += len(current.JSON)
			if valueBytes > HistoryMaxResultBytes {
				return historyError("query_limit", "result")
			}
			result.Intervals = append(
				result.Intervals,
				HistoryValueInterval{Range: HistoryRange{From: start, To: end}, Value: current.JSON},
			)
		}
		previous = current
		start = end
		return nil
	}
	var pending *historySelected
	for _, row := range window.Rows {
		value, err := evaluator.selectValue(plan.selectProgram, row)
		if err != nil {
			return HistoryTimelineResult{}, err
		}
		if pending != nil && !pending.row.ObservedAt.Equal(row.ObservedAt) {
			if err = appendInterval(pending.row.ObservedAt); err != nil {
				return HistoryTimelineResult{}, err
			}
			current = &pending.value
		}
		pending = &historySelected{row: row, value: value}
	}
	if pending != nil {
		if err := appendInterval(pending.row.ObservedAt); err != nil {
			return HistoryTimelineResult{}, err
		}
		current = &pending.value
	}
	if err := appendInterval(r.To); err != nil {
		return HistoryTimelineResult{}, err
	}
	return result, nil
}
