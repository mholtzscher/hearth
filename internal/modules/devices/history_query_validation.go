package devices

import (
	"regexp"
	"sort"
	"time"
)

var historyFieldPattern = regexp.MustCompile(`^(/[a-z][a-z0-9_]*)?$`)

// historyPlan is a validated query with its derived bucket ranges.
type historyPlan struct {
	query HistoryQuery
	names []string // metric names, sorted
	// ranges holds one range per bucket; the final one is clipped at To.
	ranges []HistoryRange
	// end is To rounded up to the cell grid: the rollup read's exclusive bound.
	end time.Time
}

// width is the bucket duration, or zero for the reader's overall summary.
func (plan historyPlan) width() time.Duration {
	if plan.query.Bucket != nil {
		return plan.query.Bucket.Duration
	}
	return 0
}

func historyError(code, field string) error { return &HistoryQueryError{Code: code, Field: field} }

func onHistoryCellGrid(at time.Time) bool {
	return at.Nanosecond() == 0 && at.Unix()%int64(HistoryRollupCellDuration/time.Second) == 0
}

// prepareHistoryQuery validates every query field before any read.
func prepareHistoryQuery(query HistoryQuery, now time.Time) (historyPlan, error) {
	plan := historyPlan{query: query}
	if _, err := ParseEntityID(string(query.EntityID)); err != nil {
		return plan, historyError("invalid_query", "entity_id")
	}
	if query.Range.From.Location() != time.UTC || query.Range.To.Location() != time.UTC ||
		!query.Range.From.Before(query.Range.To) || query.Range.To.After(now) ||
		!onHistoryCellGrid(query.Range.From) {
		return plan, historyError("invalid_query", "range")
	}
	if !historyFieldPattern.MatchString(query.Field) {
		return plan, historyError("invalid_query", "select_field")
	}
	switch query.Nulls {
	case "":
		plan.query.Nulls = HistoryNullReject
	case HistoryNullReject, HistoryNullGap:
	default:
		return plan, historyError("invalid_query", "result.nulls")
	}
	var err error
	if plan.names, err = historyMetricNames(query.Metrics); err != nil {
		return plan, err
	}
	if plan.ranges, err = historyBucketRanges(query.Range, query.Bucket); err != nil {
		return plan, err
	}
	plan.end = query.Range.To.Truncate(HistoryRollupCellDuration)
	if plan.end.Before(query.Range.To) {
		plan.end = plan.end.Add(HistoryRollupCellDuration)
	}
	return plan, nil
}

// historyMetricNames validates the closed metric variants and returns their
// names in sorted order.
func historyMetricNames(metrics map[string]HistoryMetric) ([]string, error) {
	if len(metrics) == 0 || len(metrics) > HistoryMaxMetrics {
		return nil, historyError("invalid_query", "result.metrics")
	}
	names := make([]string, 0, len(metrics))
	for name, metric := range metrics {
		if name == "" {
			return nil, historyError("invalid_query", "result.metrics")
		}
		switch metric.(type) {
		case HistoryMinimum, HistoryMaximum, HistorySampleMean:
		default:
			return nil, historyError("invalid_query", "result.metrics."+name)
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names, nil
}

// historyBucketRanges returns one range per bucket, clipping the final one at
// the range end; an unbucketed summary is a single range.
func historyBucketRanges(window HistoryRange, bucket *HistoryBucket) ([]HistoryRange, error) {
	if bucket == nil {
		return []HistoryRange{window}, nil
	}
	if bucket.Duration <= 0 || bucket.Duration%HistoryRollupCellDuration != 0 || bucket.Alignment != "range_start" {
		return nil, historyError("invalid_query", "result.bucket")
	}
	var ranges []HistoryRange
	for from := window.From; from.Before(window.To); {
		if len(ranges) == HistoryMaxBuckets {
			return nil, historyError("query_limit", "result.bucket")
		}
		to := from.Add(bucket.Duration)
		if to.After(window.To) {
			to = window.To
		}
		ranges = append(ranges, HistoryRange{From: from, To: to})
		from = to
	}
	return ranges, nil
}
