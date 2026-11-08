//nolint:testpackage // Exercises null policy through the Devices query entry point.
package devices

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func historyNullTestMetrics() map[string]HistoryMetric {
	return map[string]HistoryMetric{
		"min": HistoryMinimum{}, "max": HistoryMaximum{}, "mean": HistorySampleMean{},
	}
}

//nolint:gocognit // One decision matrix crosses reports, policies, and bucket shapes at their owner.
func TestHistorySummaryNullPolicyMatrix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		values []string
		want   []string
		strict bool
		wrong  bool
	}{
		{name: "numbers", values: []string{"40", "60"}, want: []string{"40", "60", "50"}},
		{name: "mixed", values: []string{"40", "null", "60"}, want: []string{"null", "null", "null"}, strict: true},
		{name: "null first", values: []string{"null", "40", "60"}, want: []string{"null", "null", "null"}, strict: true},
		{name: "null last", values: []string{"40", "60", "null"}, want: []string{"null", "null", "null"}, strict: true},
		{name: "null only", values: []string{"null"}, want: []string{"null", "null", "null"}, strict: true},
		{name: "empty", want: []string{"null", "null", "null"}},
		{name: "zero", values: []string{"0"}, want: []string{"0", "0", "0"}},
		{name: "string", values: []string{"40", `"60"`}, wrong: true},
		{name: "boolean", values: []string{"40", "true"}, wrong: true},
		{name: "object", values: []string{"40", `{"value":60}`}, wrong: true},
		{name: "array", values: []string{"40", "[60]"}, wrong: true},
		{name: "string after null", values: []string{"40", "null", `"60"`}, strict: true, wrong: true},
		{name: "boolean after null", values: []string{"40", "null", "true"}, strict: true, wrong: true},
		{name: "object after null", values: []string{"40", "null", `{"value":60}`}, strict: true, wrong: true},
		{name: "array after null", values: []string{"null", "[60]"}, strict: true, wrong: true},
	} {
		for _, policy := range []HistoryNullPolicy{"", HistoryNullReject, HistoryNullGap} {
			for _, bucketed := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/bucketed=%t", tc.name, policy, bucketed), func(t *testing.T) {
					t.Parallel()
					rows := make([]EntityStateHistoryEntry, len(tc.values))
					for i, value := range tc.values {
						rows[i] = historyTestRow(i, int64(i+1), value)
					}
					reader := &historyReaderDouble{window: HistoryWindow{Rows: rows}}
					spec := HistorySummary{Metrics: historyNullTestMetrics(), Nulls: policy}
					if bucketed {
						spec.Bucket = &HistoryBucket{Duration: time.Hour, Alignment: "range_start"}
					}
					result, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(spec))
					if tc.wrong || tc.strict && policy != HistoryNullGap {
						requireHistoryError(t, err, "expression_type")
						return
					}
					require.NoError(t, err)
					var values HistoryMetricValues
					if bucketed {
						data := result.Data.(HistoryBucketedSummaryResult)
						require.Len(t, data.Buckets, 1)
						require.Nil(t, data.Coverage)
						require.Nil(t, data.Buckets[0].Coverage)
						values = data.Buckets[0].Metrics
					} else {
						data := result.Data.(HistorySummaryResult)
						require.Nil(t, data.Coverage)
						values = data.Metrics
					}
					for i, label := range []string{"min", "max", "mean"} {
						require.Equal(t, tc.want[i], string(values[label]), label)
					}
					require.False(t, reader.request.IncludeSeed)
				})
			}
		}
	}
}

func TestHistorySummaryNullPolicyValidation(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		spec HistorySummary
	}{
		{"unknown", HistorySummary{Metrics: historyNullTestMetrics(), Nulls: "skip"}},
		{"case sensitive", HistorySummary{Metrics: historyNullTestMetrics(), Nulls: "GAP"}},
		{"duration only", HistorySummary{Nulls: HistoryNullGap, Metrics: map[string]HistoryMetric{
			"seconds": HistoryDurationWhere{WhereExpr: "value != nil", Unit: "seconds"},
		}}},
		{"transitions only", HistorySummary{Nulls: HistoryNullGap, Metrics: map[string]HistoryMetric{
			"entries": HistoryTransitionsInto{WhereExpr: "value != nil"},
		}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &historyReaderDouble{}
			_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(&tc.spec))
			var failure *HistoryQueryError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "invalid_query", failure.Code)
			require.Equal(t, "result.nulls", failure.Field)
			require.Empty(t, reader.request.EntityID)
		})
	}
}

func TestHistorySummaryNullGapBucketBoundaries(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{window: HistoryWindow{Rows: []EntityStateHistoryEntry{
		historyTestRow(0, 1, "40"), historyTestRow(9, 2, "null"), historyTestRow(10, 3, "60"),
		historyTestRow(20, 4, "null"), historyTestRow(30, 5, "80"),
	}}}
	spec := HistorySummary{
		Metrics: historyNullTestMetrics(), Nulls: HistoryNullGap,
		Bucket: &HistoryBucket{Duration: 10 * time.Minute, Alignment: "range_start"},
	}
	result, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(spec))
	require.NoError(t, err)
	buckets := result.Data.(HistoryBucketedSummaryResult).Buckets
	require.Len(t, buckets, 6)
	for i, want := range []string{"null", "60", "null", "80", "null", "null"} {
		require.Equal(t, historyTestStart.Add(time.Duration(i)*10*time.Minute), buckets[i].Range.From)
		require.Equal(t, historyTestStart.Add(time.Duration(i+1)*10*time.Minute), buckets[i].Range.To)
		for _, label := range []string{"min", "max", "mean"} {
			require.Equal(t, want, string(buckets[i].Metrics[label]))
		}
	}
}

func TestHistorySummaryNullGapMissingSelection(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{window: HistoryWindow{Rows: []EntityStateHistoryEntry{
		historyTestRow(0, 1, `{"mode":"value","value":40}`),
		historyTestRow(10, 2, `{"mode":"choice","choice":"auto"}`),
		historyTestRow(20, 3, `{"mode":"value","value":60}`),
	}}}
	query := historyTestQuery(HistorySummary{Metrics: historyNullTestMetrics(), Nulls: HistoryNullGap})
	query.SelectExpr = new("sample.value.value")
	result, err := historyTestService(reader).QueryHistory(context.Background(), query)
	require.NoError(t, err)
	for _, value := range result.Data.(HistorySummaryResult).Metrics {
		require.Equal(t, "null", string(value))
	}
	query.Result = HistoryObservations{}
	result, err = historyTestService(reader).QueryHistory(context.Background(), query)
	require.NoError(t, err)
	items := result.Data.(HistoryObservationsResult).Items
	require.Len(t, items, 3)
	for i, want := range []string{"40", "null", "60"} {
		require.Equal(t, want, string(items[i].Value))
	}
}

func TestHistorySummaryNullGapMixedTemporalAndSeed(t *testing.T) {
	t.Parallel()
	seed := historyTestRow(-1, 1, "null")
	reader := &historyReaderDouble{window: HistoryWindow{Seed: &seed, Rows: []EntityStateHistoryEntry{
		historyTestRow(10, 2, "40"), historyTestRow(20, 3, "null"), historyTestRow(30, 4, "60"),
	}}}
	spec := HistorySummary{
		Nulls: HistoryNullGap, Metrics: historyNullTestMetrics(),
		Bucket: &HistoryBucket{Duration: 10 * time.Minute, Alignment: "range_start"},
	}
	spec.Metrics["seconds"] = HistoryDurationWhere{WhereExpr: "value != nil", Unit: "seconds"}
	spec.Metrics["entries"] = HistoryTransitionsInto{WhereExpr: "value != nil"}
	service := historyTestService(reader)
	result, err := service.QueryHistory(context.Background(), historyTestQuery(spec))
	require.NoError(t, err)
	require.True(t, reader.request.IncludeSeed)
	data := result.Data.(HistoryBucketedSummaryResult)
	require.True(t, data.Coverage.SeedAvailable)
	require.InDelta(t, 3600, data.Coverage.InferredKnownSeconds, 1e-9)
	require.Zero(t, data.Coverage.UnknownSeconds)
	for i, want := range []string{"null", "40", "null", "60", "null", "null"} {
		require.Equal(t, want, string(data.Buckets[i].Metrics["mean"]))
	}
	for i, want := range []string{"0", "600", "0", "600", "600", "600"} {
		require.Equal(t, want, string(data.Buckets[i].Metrics["seconds"]))
	}
	for i, want := range []string{"0", "1", "0", "1", "0", "0"} {
		require.Equal(t, want, string(data.Buckets[i].Metrics["entries"]))
	}
	unbucketed := spec
	unbucketed.Bucket = nil
	whole, err := service.QueryHistory(context.Background(), historyTestQuery(unbucketed))
	require.NoError(t, err)
	values := whole.Data.(HistorySummaryResult).Metrics
	for _, label := range []string{"min", "max", "mean"} {
		require.Equal(t, "null", string(values[label]))
	}
	require.Equal(t, "2400", string(values["seconds"]))
	require.Equal(t, "2", string(values["entries"]))
	// Removing numeric metrics and their policy leaves temporal output unchanged.
	spec.Nulls = HistoryNullReject
	delete(spec.Metrics, "min")
	delete(spec.Metrics, "max")
	delete(spec.Metrics, "mean")
	temporal, err := service.QueryHistory(context.Background(), historyTestQuery(spec))
	require.NoError(t, err)
	baseline := temporal.Data.(HistoryBucketedSummaryResult)
	require.Equal(t, baseline.Coverage, data.Coverage)
	for i, bucket := range baseline.Buckets {
		require.Equal(t, bucket.Coverage, data.Buckets[i].Coverage)
		for label, value := range bucket.Metrics {
			require.Equal(t, value, data.Buckets[i].Metrics[label])
		}
	}
	// A temporal predicate still sees known null and must return a boolean itself.
	spec.Nulls = HistoryNullGap
	spec.Metrics["mean"] = HistorySampleMean{}
	spec.Metrics["seconds"] = HistoryDurationWhere{WhereExpr: "value", Unit: "seconds"}
	_, err = service.QueryHistory(context.Background(), historyTestQuery(spec))
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "expression_type", failure.Code)
	require.Equal(t, "result.metrics.seconds.where_expr", failure.Field)
}

func TestHistorySummaryNullSeedDoesNotPoisonNumericReports(t *testing.T) {
	t.Parallel()
	seed := historyTestRow(-1, 1, "null")
	for _, policy := range []HistoryNullPolicy{HistoryNullReject, HistoryNullGap} {
		t.Run(string(policy), func(t *testing.T) {
			t.Parallel()
			reader := &historyReaderDouble{window: HistoryWindow{Seed: &seed, Rows: []EntityStateHistoryEntry{
				historyTestRow(10, 2, "40"), historyTestRow(20, 3, "60"),
			}}}
			metrics := historyNullTestMetrics()
			metrics["seconds"] = HistoryDurationWhere{WhereExpr: "value != nil", Unit: "seconds"}
			result, err := historyTestService(
				reader,
			).QueryHistory(context.Background(), historyTestQuery(HistorySummary{
				Nulls: policy, Metrics: metrics,
			}))
			require.NoError(t, err)
			data := result.Data.(HistorySummaryResult)
			require.True(t, data.Coverage.SeedAvailable)
			for label, want := range map[string]string{"min": "40", "max": "60", "mean": "50", "seconds": "3000"} {
				require.Equal(t, want, string(data.Metrics[label]))
			}
		})
	}
}

func TestHistorySummaryNullGapDoesNotHideLaterFailures(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, expression, code string
	}{
		{"nonfinite", "sample.value == nil ? nil : 1 / sample.value", "expression_value_invalid"},
		{"evaluation", "sample.value == nil ? nil : sample.value[0]", "expression_evaluation"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &historyReaderDouble{window: HistoryWindow{Rows: []EntityStateHistoryEntry{
				historyTestRow(0, 1, "null"), historyTestRow(1, 2, "0"),
			}}}
			query := historyTestQuery(HistorySummary{Nulls: HistoryNullGap, Metrics: historyNullTestMetrics()})
			query.SelectExpr = &tc.expression
			_, err := historyTestService(reader).QueryHistory(context.Background(), query)
			requireHistoryError(t, err, tc.code)
		})
	}
}

func TestHistorySummaryNullGapPreservesLimits(t *testing.T) {
	t.Parallel()
	t.Run("source rows", func(t *testing.T) {
		t.Parallel()
		// A read that cannot advance its cursor exhausts the source budget.
		reader := &historyReaderDouble{window: HistoryWindow{HasMore: true}}
		query := historyTestQuery(HistorySummary{Nulls: HistoryNullGap, Metrics: historyNullTestMetrics()})
		_, err := historyTestService(reader).QueryHistory(context.Background(), query)
		requireHistoryError(t, err, "query_limit")
	})
	t.Run("evaluations after null", func(t *testing.T) {
		t.Parallel()
		metrics := map[string]HistoryMetric{"mean": HistorySampleMean{}}
		for i := range 15 {
			metrics[fmt.Sprintf("entries%d", i)] = HistoryTransitionsInto{WhereExpr: "value == nil"}
		}
		// 16 evaluations per row: the selection plus 15 predicates.
		rows := make([]EntityStateHistoryEntry, HistoryMaxEvaluations/16+1)
		for i := range rows {
			rows[i] = historyTestRow(0, int64(i+1), "null")
			rows[i].ObservedAt = historyTestStart.Add(time.Duration(i) * time.Millisecond)
		}
		reader := &historyReaderDouble{window: HistoryWindow{Rows: rows}}
		_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(HistorySummary{
			Nulls: HistoryNullGap, Metrics: metrics,
		}))
		requireHistoryError(t, err, "query_limit")
	})
	t.Run("result bytes", func(t *testing.T) {
		t.Parallel()
		reader := &historyReaderDouble{
			window: HistoryWindow{Rows: []EntityStateHistoryEntry{historyTestRow(0, 1, "null")}},
		}
		_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(HistorySummary{
			Nulls: HistoryNullGap, Metrics: map[string]HistoryMetric{strings.Repeat("x", 5000): HistoryMinimum{}},
			Bucket: &HistoryBucket{Duration: 4 * time.Second, Alignment: "range_start"},
		}))
		requireHistoryError(t, err, "query_limit")
	})
}
