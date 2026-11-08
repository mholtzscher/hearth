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

// historyTimeline coalesces last-value-held intervals from ascending rows.
// A row is held as pending until a later timestamp proves it was the last
// receive-ordered report at its instant, so ties can span read chunks.
type historyTimeline struct {
	plan       historyPlan
	evaluator  *historyEvaluator
	result     HistoryTimelineResult
	current    *exprvalue.Value
	previous   *exprvalue.Value
	start      time.Time
	pending    *historySelected
	valueBytes int
}

func newHistoryTimeline(
	plan historyPlan,
	evaluator *historyEvaluator,
	seed *EntityStateHistoryEntry,
) (*historyTimeline, error) {
	timeline := &historyTimeline{
		plan:      plan,
		evaluator: evaluator,
		start:     plan.query.Range.From,
		result: HistoryTimelineResult{
			Coverage:  HistoryTemporalCoverage{SeedAvailable: seed != nil},
			Intervals: make([]HistoryInterval, 0),
		},
	}
	if seed != nil {
		value, err := evaluator.selectValue(plan.selectProgram, *seed)
		if err != nil {
			return nil, err
		}
		timeline.current = &value
	}
	return timeline, nil
}

func (timeline *historyTimeline) add(row EntityStateHistoryEntry) error {
	value, err := timeline.evaluator.selectValue(timeline.plan.selectProgram, row)
	if err != nil {
		return err
	}
	if timeline.pending != nil && !timeline.pending.row.ObservedAt.Equal(row.ObservedAt) {
		if err = timeline.hold(); err != nil {
			return err
		}
	}
	timeline.pending = &historySelected{row: row, value: value}
	return nil
}

func (timeline *historyTimeline) finish() (HistoryResult, error) {
	if timeline.pending != nil {
		if err := timeline.hold(); err != nil {
			return nil, err
		}
	}
	if err := timeline.appendInterval(timeline.plan.query.Range.To); err != nil {
		return nil, err
	}
	return timeline.result, nil
}

// hold closes the interval before the pending report and makes it current.
func (timeline *historyTimeline) hold() error {
	if err := timeline.appendInterval(timeline.pending.row.ObservedAt); err != nil {
		return err
	}
	timeline.current = &timeline.pending.value
	timeline.pending = nil
	return nil
}

func (timeline *historyTimeline) appendInterval(end time.Time) error {
	if !timeline.start.Before(end) {
		return nil
	}
	start, current, previous := timeline.start, timeline.current, timeline.previous
	seconds := historySeconds(start, end)
	if current == nil {
		timeline.result.Coverage.UnknownSeconds += seconds
	} else {
		timeline.result.Coverage.InferredKnownSeconds += seconds
	}
	intervals := timeline.result.Intervals
	switch {
	case len(intervals) > 0 &&
		((current == nil && previous == nil) ||
			(current != nil && previous != nil && reflect.DeepEqual(current.Data, previous.Data))):
		last := len(intervals) - 1
		switch interval := intervals[last].(type) {
		case HistoryValueInterval:
			interval.Range.To = end
			intervals[last] = interval
		case HistoryUnknownInterval:
			interval.Range.To = end
			intervals[last] = interval
		}
	case current == nil:
		timeline.result.Intervals = append(
			timeline.result.Intervals,
			HistoryUnknownInterval{Range: HistoryRange{From: start, To: end}},
		)
	default:
		timeline.valueBytes += len(current.JSON)
		if timeline.valueBytes > HistoryMaxResultBytes {
			return historyError("query_limit", "result")
		}
		timeline.result.Intervals = append(
			timeline.result.Intervals,
			HistoryValueInterval{Range: HistoryRange{From: start, To: end}, Value: current.JSON},
		)
	}
	timeline.previous = current
	timeline.start = end
	return nil
}
