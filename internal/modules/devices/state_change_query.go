package devices

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sort"
	"time"
)

// QueryStateChanges answers a timeline or a yes/no summary from recorded
// changes. Every change in [From, To) and the newest change before From are
// read from one snapshot; nothing fills time before the first known value.
func (service *Service) QueryStateChanges(ctx context.Context, query StateChangeQuery) (StateChangeQueryResult, error) {
	ranges, err := prepareStateChangeQuery(query, service.dependencies.Now())
	if err != nil {
		return StateChangeQueryResult{}, err
	}
	if service.stores.StateChanges == nil {
		return StateChangeQueryResult{}, errors.New("state change reader not configured")
	}
	rows, err := service.stores.StateChanges.ReadStateChanges(ctx, StateChangeRead{
		EntityID: query.EntityID, Field: query.Field, Range: query.Range,
		Boolean: query.Bucket != nil, Limit: StateChangeMaxRows,
	})
	if err != nil {
		return StateChangeQueryResult{}, err
	}
	timeline, transitions := assembleStateChangeTimeline(query.Range, rows)
	result := StateChangeQueryResult{
		EntityID: query.EntityID, Field: query.Field, Range: query.Range, Seeded: rows.Seed != nil, Data: timeline,
	}
	if query.Bucket != nil {
		result.Data = summarizeStateChanges(*query.Bucket, ranges, timeline, transitions)
	}
	return result, nil
}

// prepareStateChangeQuery validates every query field before any read and
// returns the summary's bucket ranges, if any.
func prepareStateChangeQuery(query StateChangeQuery, now time.Time) ([]HistoryRange, error) {
	if _, err := ParseEntityID(string(query.EntityID)); err != nil {
		return nil, historyError("invalid_query", "entity_id")
	}
	if query.Range.From.Location() != time.UTC || query.Range.To.Location() != time.UTC ||
		!query.Range.From.Before(query.Range.To) || query.Range.To.After(now) {
		return nil, historyError("invalid_query", "range")
	}
	if !historyFieldPattern.MatchString(query.Field) {
		return nil, historyError("invalid_query", "select_field")
	}
	if query.Bucket == nil {
		return nil, nil
	}
	return historyBucketRanges(query.Range, query.Bucket)
}

var jsonTrue = json.RawMessage("true") //nolint:gochecknoglobals // Immutable literal.

// assembleStateChangeTimeline turns ordered changes into contiguous intervals
// over the range, coalescing equal values, and returns the instants of
// established changes into true: from a known false or null, never from unknown.
func assembleStateChangeTimeline(window HistoryRange, rows StateChangeRows) (StateChangeTimeline, []time.Time) {
	var timeline StateChangeTimeline
	var transitions []time.Time
	current := StateChangeInterval{Range: HistoryRange{From: window.From}}
	if rows.Seed != nil {
		current.Known, current.Value = true, rows.Seed.Value
	}
	closeAt := func(at time.Time) {
		if !current.Range.From.Before(at) {
			return
		}
		current.Range.To = at
		timeline.Intervals = append(timeline.Intervals, current)
	}
	for _, row := range rows.Rows {
		if current.Known && bytes.Equal(current.Value, row.Value) {
			continue
		}
		if current.Known && bytes.Equal(row.Value, jsonTrue) {
			transitions = append(transitions, row.ObservedAt)
		}
		closeAt(row.ObservedAt)
		// A change at the same instant as the open interval's start replaces it.
		if last := len(timeline.Intervals) - 1; last >= 0 && !current.Range.From.Before(row.ObservedAt) &&
			timeline.Intervals[last].Known && bytes.Equal(timeline.Intervals[last].Value, row.Value) {
			current = timeline.Intervals[last]
			timeline.Intervals = timeline.Intervals[:last]
			continue
		}
		start := current.Range.From
		if start.Before(row.ObservedAt) {
			start = row.ObservedAt
		}
		current = StateChangeInterval{Range: HistoryRange{From: start}, Known: true, Value: row.Value}
	}
	closeAt(window.To)
	return timeline, transitions
}

// summarizeStateChanges adds each interval's overlap to the buckets it spans
// and counts each change into true in the bucket containing its instant.
func summarizeStateChanges(
	bucket HistoryBucket, ranges []HistoryRange, timeline StateChangeTimeline, transitions []time.Time,
) StateChangeSummary {
	summary := StateChangeSummary{Bucket: bucket, Buckets: make([]StateChangeBucket, len(ranges))}
	for i, bucketRange := range ranges {
		summary.Buckets[i].Range = bucketRange
	}
	// containing returns the bucket whose half-open range contains at.
	containing := func(at time.Time) int {
		return sort.Search(len(ranges), func(i int) bool { return at.Before(ranges[i].To) })
	}
	for _, interval := range timeline.Intervals {
		if !interval.Known {
			continue
		}
		isTrue := bytes.Equal(interval.Value, jsonTrue)
		for i := containing(interval.Range.From); i < len(ranges) && ranges[i].From.Before(interval.Range.To); i++ {
			from, to := interval.Range.From, interval.Range.To
			if from.Before(ranges[i].From) {
				from = ranges[i].From
			}
			if to.After(ranges[i].To) {
				to = ranges[i].To
			}
			seconds := to.Sub(from).Seconds()
			summary.Buckets[i].KnownSeconds += seconds
			if isTrue {
				summary.Buckets[i].SecondsTrue += seconds
			}
		}
	}
	for _, at := range transitions {
		if i := containing(at); i < len(ranges) {
			summary.Buckets[i].TransitionsIntoTrue++
		}
	}
	return summary
}
