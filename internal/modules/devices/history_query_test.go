//nolint:testpackage // Exercises the Devices service and its rollup reader seam together.
package devices

import (
	"context"
	"math/big"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type historyRollupReaderDouble struct {
	buckets  []HistoryRollupBucket
	err      error
	requests []HistoryRollupRequest
}

func (r *historyRollupReaderDouble) ReadHistoryRollupBuckets(
	_ context.Context, request HistoryRollupRequest,
) ([]HistoryRollupBucket, error) {
	r.requests = append(r.requests, request)
	return r.buckets, r.err
}

const historyTestEntity EntityID = "ent_019a1b2c-3d4e-7000-8000-000000000001"

func historyTestStart() time.Time { return time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC) }

func historyTestService(reader *historyRollupReaderDouble) *Service {
	return NewService(
		Stores{HistoryRollups: reader},
		nil,
		nil,
		Dependencies{Now: func() time.Time { return historyTestStart().Add(24 * time.Hour) }},
	)
}

func historyTestQuery() HistoryQuery {
	return HistoryQuery{
		EntityID: historyTestEntity,
		Range:    HistoryRange{From: historyTestStart(), To: historyTestStart().Add(time.Hour)},
		Metrics: map[string]HistoryMetric{
			"min":  HistoryMinimum{},
			"max":  HistoryMaximum{},
			"mean": HistorySampleMean{},
		},
		Bucket: &HistoryBucket{Duration: 20 * time.Minute, Alignment: "range_start"},
	}
}

func historyIntBucket(index int, count, sum, minimum, maximum int64) HistoryRollupBucket {
	return HistoryRollupBucket{
		Index: index, Count: count,
		Sum: *big.NewRat(sum, 1), Min: HistoryIntNumber(minimum), Max: HistoryIntNumber(maximum),
	}
}

func historyMetricStrings(values HistoryMetricValues) map[string]string {
	rendered := map[string]string{}
	for name, value := range values {
		rendered[name] = string(value)
	}
	return rendered
}

func TestQueryHistoryRendersBucketsFromRollups(t *testing.T) {
	t.Parallel()
	reader := &historyRollupReaderDouble{buckets: []HistoryRollupBucket{
		historyIntBucket(0, 3, 62600, 20000, 21300),
		historyIntBucket(2, 1, 22000, 22000, 22000),
	}}
	query := historyTestQuery()
	query.Field = "/value"
	result, err := historyTestService(reader).QueryHistory(context.Background(), query)
	require.NoError(t, err)

	start := historyTestStart()
	require.Equal(t, []HistoryRollupRequest{{
		EntityID: historyTestEntity, Field: "/value", From: start, End: start.Add(time.Hour), Bucket: 20 * time.Minute,
	}}, reader.requests)
	data, ok := result.Data.(HistoryBucketedSummaryResult)
	require.True(t, ok, "%T", result.Data)
	require.Equal(t, *query.Bucket, data.Bucket)
	require.Len(t, data.Buckets, 3)
	for i, bucket := range data.Buckets {
		from := start.Add(time.Duration(i) * 20 * time.Minute)
		require.Equal(t, HistoryRange{From: from, To: from.Add(20 * time.Minute)}, bucket.Range)
	}
	// The mean divides the exact integer sum once: 62600 / 3.
	require.Equal(t, map[string]string{"min": "20000", "max": "21300", "mean": "20866.666666666668"},
		historyMetricStrings(data.Buckets[0].Metrics))
	require.Equal(t, map[string]string{"min": "null", "max": "null", "mean": "null"},
		historyMetricStrings(data.Buckets[1].Metrics), "a bucket without cells renders null")
	require.Equal(t, map[string]string{"min": "22000", "max": "22000", "mean": "22000"},
		historyMetricStrings(data.Buckets[2].Metrics))
}

func TestQueryHistoryKeepsRealAndIntegerForms(t *testing.T) {
	t.Parallel()
	reader := &historyRollupReaderDouble{buckets: []HistoryRollupBucket{{
		Index: 0, Count: 3,
		Sum: *big.NewRat(546, 1), Min: HistoryRealNumber(45), Max: HistoryRealNumber(250.5),
	}}}
	query := historyTestQuery()
	query.Bucket = nil
	// An unaligned To rounds the read up to the next cell boundary.
	query.Range.To = historyTestStart().Add(30*time.Minute + 30*time.Second)
	result, err := historyTestService(reader).QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, historyTestStart().Add(31*time.Minute), reader.requests[0].End)
	require.Zero(t, reader.requests[0].Bucket)
	data, ok := result.Data.(HistorySummaryResult)
	require.True(t, ok, "%T", result.Data)
	require.Equal(t, map[string]string{"min": "45", "max": "250.5", "mean": "182"}, historyMetricStrings(data.Metrics))
	require.Equal(t, query.Range, result.Range)
}

func TestQueryHistoryNullPolicies(t *testing.T) {
	t.Parallel()
	withNull := []HistoryRollupBucket{
		historyIntBucket(0, 2, 40, 10, 30),
		{
			Index: 1,
			Count: 1,
			Nulls: 1,
			Sum:   *big.NewRat(5, 1),
			Min:   HistoryIntNumber(5),
			Max:   HistoryIntNumber(5),
		},
		{Index: 2, Nulls: 2},
	}
	query := historyTestQuery()
	_, err := historyTestService(
		&historyRollupReaderDouble{buckets: withNull},
	).QueryHistory(context.Background(), query)
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	// Metrics are checked in sorted name order, so "max" is named.
	require.Equal(t, HistoryQueryError{Code: "value_type", Field: "result.metrics.max"}, *failure)

	query.Nulls = HistoryNullGap
	result, err := historyTestService(
		&historyRollupReaderDouble{buckets: withNull},
	).QueryHistory(context.Background(), query)
	require.NoError(t, err)
	buckets := result.Data.(HistoryBucketedSummaryResult).Buckets
	require.Equal(
		t,
		map[string]string{"min": "10", "max": "30", "mean": "20"},
		historyMetricStrings(buckets[0].Metrics),
	)
	for _, bucket := range buckets[1:] {
		require.Equal(
			t,
			map[string]string{"min": "null", "max": "null", "mean": "null"},
			historyMetricStrings(bucket.Metrics),
		)
	}

	invalid := []HistoryRollupBucket{{Index: 0, Invalid: 1}}
	_, err = historyTestService(&historyRollupReaderDouble{buckets: invalid}).QueryHistory(context.Background(), query)
	require.ErrorAs(t, err, &failure)
	require.Equal(t, HistoryQueryError{Code: "value_type", Field: "result.metrics.max"}, *failure,
		"gap never hides a non-numeric value")
}

func TestQueryHistoryValidatesBeforeReading(t *testing.T) {
	t.Parallel()
	start := historyTestStart()
	for _, test := range []struct {
		name   string
		mutate func(*HistoryQuery)
		want   HistoryQueryError
	}{
		{"entity", func(q *HistoryQuery) { q.EntityID = "bad" }, HistoryQueryError{"invalid_query", "entity_id"}},
		{"unaligned from", func(q *HistoryQuery) { q.Range.From = start.Add(30 * time.Second) },
			HistoryQueryError{"invalid_query", "range"}},
		{"sub-second from", func(q *HistoryQuery) { q.Range.From = start.Add(time.Nanosecond) },
			HistoryQueryError{"invalid_query", "range"}},
		{"empty range", func(q *HistoryQuery) { q.Range.To = q.Range.From }, HistoryQueryError{"invalid_query", "range"}},
		{"future to", func(q *HistoryQuery) { q.Range.To = start.Add(25 * time.Hour) },
			HistoryQueryError{"invalid_query", "range"}},
		{"non-UTC", func(q *HistoryQuery) { q.Range.From = q.Range.From.In(time.FixedZone("x", 3600)) },
			HistoryQueryError{"invalid_query", "range"}},
		{"field", func(q *HistoryQuery) { q.Field = "value" }, HistoryQueryError{"invalid_query", "select_field"}},
		{"nulls", func(q *HistoryQuery) { q.Nulls = "skip" }, HistoryQueryError{"invalid_query", "result.nulls"}},
		{"no metrics", func(q *HistoryQuery) { q.Metrics = nil }, HistoryQueryError{"invalid_query", "result.metrics"}},
		{"empty name", func(q *HistoryQuery) { q.Metrics = map[string]HistoryMetric{"": HistoryMinimum{}} },
			HistoryQueryError{"invalid_query", "result.metrics"}},
		{"nil metric", func(q *HistoryQuery) { q.Metrics = map[string]HistoryMetric{"m": nil} },
			HistoryQueryError{"invalid_query", "result.metrics.m"}},
		{"too many metrics", func(q *HistoryQuery) {
			q.Metrics = map[string]HistoryMetric{}
			for i := range HistoryMaxMetrics + 1 {
				q.Metrics[strings.Repeat("m", i+1)] = HistoryMinimum{}
			}
		}, HistoryQueryError{"invalid_query", "result.metrics"}},
		{"partial-minute bucket", func(q *HistoryQuery) { q.Bucket.Duration = 90 * time.Second },
			HistoryQueryError{"invalid_query", "result.bucket"}},
		{"zero bucket", func(q *HistoryQuery) { q.Bucket.Duration = 0 }, HistoryQueryError{"invalid_query", "result.bucket"}},
		{"alignment", func(q *HistoryQuery) { q.Bucket.Alignment = "calendar" },
			HistoryQueryError{"invalid_query", "result.bucket"}},
		{"too many buckets", func(q *HistoryQuery) {
			q.Range.To = start.Add(24 * time.Hour)
			q.Bucket.Duration = time.Minute
		}, HistoryQueryError{"query_limit", "result.bucket"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := &historyRollupReaderDouble{}
			query := historyTestQuery()
			test.mutate(&query)
			_, err := historyTestService(reader).QueryHistory(context.Background(), query)
			var failure *HistoryQueryError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, test.want, *failure)
			require.Empty(t, reader.requests, "invalid queries must not read")
		})
	}
}

func TestQueryHistoryReaderFailures(t *testing.T) {
	t.Parallel()
	reader := &historyRollupReaderDouble{err: ErrEntityNotFound}
	_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery())
	require.ErrorIs(t, err, ErrEntityNotFound)

	outside := &historyRollupReaderDouble{buckets: []HistoryRollupBucket{historyIntBucket(3, 1, 1, 1, 1)}}
	_, err = historyTestService(outside).QueryHistory(context.Background(), historyTestQuery())
	var failure *HistoryQueryError
	require.Error(t, err)
	require.NotErrorAs(t, err, &failure, "a corrupt read is an internal failure, not caller input")
}

func TestQueryHistoryResultSizeLimit(t *testing.T) {
	t.Parallel()
	query := historyTestQuery()
	query.Bucket = nil
	query.Metrics = map[string]HistoryMetric{}
	for i := range HistoryMaxMetrics {
		query.Metrics[strings.Repeat(string(rune('a'+i)), HistoryMaxResultBytes/HistoryMaxMetrics)] = HistoryMinimum{}
	}
	_, err := historyTestService(&historyRollupReaderDouble{}).QueryHistory(context.Background(), query)
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, HistoryQueryError{Code: "query_limit", Field: "result"}, *failure)
}
