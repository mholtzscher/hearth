//nolint:testpackage // Tests the service through its owned reader boundary.
package devices

import (
	"context"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHistoryTimelineExactBoundsAndCoverage(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{
			Rows: []EntityStateHistoryEntry{historyTestRow(10, 1, "null"), historyTestRow(20, 2, "true")},
		},
	}
	result, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	require.NoError(t, err)
	data := result.Data.(HistoryTimelineResult)
	require.Equal(t, []HistoryInterval{
		HistoryUnknownInterval{Range: HistoryRange{From: historyTestStart, To: historyTestStart.Add(10 * time.Minute)}},
		HistoryValueInterval{
			Range: HistoryRange{
				From: historyTestStart.Add(10 * time.Minute),
				To:   historyTestStart.Add(20 * time.Minute),
			},
			Value: []byte("null"),
		},
		HistoryValueInterval{
			Range: HistoryRange{From: historyTestStart.Add(20 * time.Minute), To: historyTestStart.Add(time.Hour)},
			Value: []byte("true"),
		},
	}, data.Intervals)
	require.InDelta(t, 600, data.Coverage.UnknownSeconds, 1e-9)
	require.InDelta(t, 3000, data.Coverage.InferredKnownSeconds, 1e-9)
	require.InDelta(t, 3600, data.Coverage.UnknownSeconds+data.Coverage.InferredKnownSeconds, 1e-9)
}

func TestHistoryTemporalEmptySeedAndBucketCarry(t *testing.T) {
	t.Parallel()
	metrics := map[string]HistoryMetric{
		"duration":    HistoryDurationWhere{WhereExpr: "value", Unit: "seconds"},
		"transitions": HistoryTransitionsInto{WhereExpr: "value"},
	}
	reader := &historyReaderDouble{}
	service := historyTestService(reader)
	query := historyTestQuery(HistorySummary{Metrics: metrics})
	result, err := service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	data := result.Data.(HistorySummaryResult)
	require.Equal(t, "null", string(data.Metrics["duration"]))
	require.Equal(t, "0", string(data.Metrics["transitions"]))
	require.InDelta(t, 3600, data.Coverage.UnknownSeconds, 1e-9)
	seed := historyTestRow(-1, 1, "true")
	reader.window.Seed = &seed
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	data = result.Data.(HistorySummaryResult)
	require.Equal(t, "3600", string(data.Metrics["duration"]))
	require.Equal(t, "0", string(data.Metrics["transitions"]))
	reader.window.Rows = []EntityStateHistoryEntry{historyTestRow(25, 2, "false"), historyTestRow(40, 3, "true")}
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, "2700", string(result.Data.(HistorySummaryResult).Metrics["duration"]))
	whole, err := strconv.ParseFloat(string(result.Data.(HistorySummaryResult).Metrics["duration"]), 64)
	require.NoError(t, err)
	query.Result = HistorySummary{
		Metrics: metrics,
		Bucket:  &HistoryBucket{Duration: 22 * time.Minute, Alignment: "range_start"},
	}
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	buckets := result.Data.(HistoryBucketedSummaryResult).Buckets
	require.Len(t, buckets, 3)
	require.Equal(t, "1320", string(buckets[0].Metrics["duration"]))
	require.Equal(t, "420", string(buckets[1].Metrics["duration"]))
	require.Equal(t, "960", string(buckets[2].Metrics["duration"]))
	require.Equal(t, "0", string(buckets[0].Metrics["transitions"]))
	require.Equal(t, "1", string(buckets[1].Metrics["transitions"]))
	require.Equal(t, "0", string(buckets[2].Metrics["transitions"]))
	require.Equal(t, historyTestStart.Add(44*time.Minute), buckets[2].Range.From)
	require.Equal(t, historyTestStart.Add(time.Hour), buckets[2].Range.To)
	var known, unknown float64
	var duration float64
	for _, bucket := range buckets {
		known += bucket.Coverage.InferredKnownSeconds
		unknown += bucket.Coverage.UnknownSeconds
		seconds, parseErr := strconv.ParseFloat(string(bucket.Metrics["duration"]), 64)
		require.NoError(t, parseErr)
		duration += seconds
	}
	require.InDelta(t, 3600, known, 1e-9)
	require.InDelta(t, 0, unknown, 1e-9)
	require.InDelta(t, whole, duration, 1e-9)
}

func TestHistoryNumericReportsIncludeUnchangedAndRepeated(t *testing.T) {
	t.Parallel()
	rows := []EntityStateHistoryEntry{historyTestRow(0, 1, "2"), historyTestRow(1, 2, "2"), historyTestRow(2, 3, "8")}
	rows[1].Disposition = DispositionUnchanged
	reader := &historyReaderDouble{window: HistoryWindow{Rows: rows}}
	result, err := historyTestService(
		reader,
	).QueryHistory(context.Background(), historyTestQuery(HistorySummary{Metrics: map[string]HistoryMetric{"mean": HistorySampleMean{}}}))
	require.NoError(t, err)
	require.Equal(t, "4", string(result.Data.(HistorySummaryResult).Metrics["mean"]))
}

func TestHistorySelectionMissingNullAndBoundedEnvironment(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{Rows: []EntityStateHistoryEntry{historyTestRow(0, 1, `{"present":null}`)}},
	}
	service := historyTestService(reader)
	for _, tc := range []struct{ expression, expected string }{{"sample.value.missing", "null"}, {"sample.value.present", "null"}, {`{missing: sample.value.missing, present: sample.value.present}`, `{"missing":null,"present":null}`}} {
		query := historyTestQuery(HistoryObservations{})
		query.SelectExpr = &tc.expression
		result, err := service.QueryHistory(context.Background(), query)
		require.NoError(t, err)
		require.Equal(t, tc.expected, string(result.Data.(HistoryObservationsResult).Items[0].Value))
	}
	for _, raw := range []string{`"` + strings.Repeat("x", 65534) + `"`, strings.Repeat("[", 15) + "null" + strings.Repeat("]", 15)} {
		reader.window.Rows[0].Value = Value(raw)
		result, err := service.QueryHistory(context.Background(), historyTestQuery(HistoryObservations{}))
		require.NoError(t, err)
		require.Equal(t, raw, string(result.Data.(HistoryObservationsResult).Items[0].Value))
	}
}

func TestHistoryExpressionErrorsAreSafeAndFieldLocal(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		query HistoryQuery
		field string
	}{
		{"selection", HistoryQuery{SelectExpr: new("private_missing.source"), Result: HistoryTimeline{}}, "select_expr"},
		{"filter", HistoryQuery{Result: HistoryObservations{WhereExpr: new("private_missing.source")}}, "result.where_expr"},
		{"duration", HistoryQuery{Result: HistorySummary{Metrics: map[string]HistoryMetric{"duration": HistoryDurationWhere{WhereExpr: "private_missing.source", Unit: "seconds"}}}}, "result.metrics.duration.where_expr"},
		{"transition", HistoryQuery{Result: HistorySummary{Metrics: map[string]HistoryMetric{"transition": HistoryTransitionsInto{WhereExpr: "private_missing.source"}}}}, "result.metrics.transition.where_expr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &historyReaderDouble{}
			tc.query.EntityID = historyTestEntity
			tc.query.Range = historyTestQuery(HistoryTimeline{}).Range
			_, err := historyTestService(reader).QueryHistory(context.Background(), tc.query)
			var failure *HistoryQueryError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, "expression_type", failure.Code)
			require.Equal(t, tc.field, failure.Field)
			require.NotContains(t, err.Error(), "private_missing")
			require.Empty(t, reader.request.EntityID)
		})
	}
	reader := &historyReaderDouble{
		window: HistoryWindow{Rows: []EntityStateHistoryEntry{historyTestRow(0, 1, `"private-state"`)}},
	}
	_, err := historyTestService(
		reader,
	).QueryHistory(context.Background(), historyTestQuery(HistorySummary{Metrics: map[string]HistoryMetric{"number": HistoryMinimum{}}}))
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "expression_type", failure.Code)
	require.Equal(t, "result.metrics.number", failure.Field)
	require.NotContains(t, err.Error(), "private-state")
	query := historyTestQuery(
		HistorySummary{
			Metrics: map[string]HistoryMetric{"predicate": HistoryDurationWhere{WhereExpr: "value", Unit: "seconds"}},
		},
	)
	_, err = historyTestService(reader).QueryHistory(context.Background(), query)
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "expression_type", failure.Code)
	require.Equal(t, "result.metrics.predicate.where_expr", failure.Field)
	require.NotContains(t, err.Error(), "private-state")
	query = historyTestQuery(HistoryObservations{})
	query.SelectExpr = new("1 / 0")
	_, err = historyTestService(reader).QueryHistory(context.Background(), query)
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "expression_value_invalid", failure.Code)
	require.Equal(t, "select_expr", failure.Field)
	require.NotContains(t, err.Error(), "private-state")
}

func TestHistoryCursorDefaultsDescendingAndInvalidTokens(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{
			Rows:      []EntityStateHistoryEntry{historyTestRow(2, 3, "true")},
			HighWater: 4,
			HasMore:   true,
		},
	}
	service := historyTestService(reader)
	result, err := service.QueryHistory(context.Background(), historyTestQuery(HistoryObservations{}))
	require.NoError(t, err)
	token := result.Data.(HistoryObservationsResult).NextCursor
	query := historyTestQuery(HistoryObservations{Order: HistoryAscending, Limit: 50})
	query.SelectExpr = new("sample.value")
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, token, result.Data.(HistoryObservationsResult).NextCursor)
	query.Result = HistoryObservations{Order: HistoryAscending, Limit: 50, Cursor: token}
	_, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, int64(4), reader.request.Cursor.HighWater)
	result, err = service.QueryHistory(
		context.Background(),
		historyTestQuery(HistoryObservations{Order: HistoryDescending, Limit: 1}),
	)
	require.NoError(t, err)
	descToken := result.Data.(HistoryObservationsResult).NextCursor
	query = historyTestQuery(HistoryObservations{Order: HistoryDescending, Limit: 1, Cursor: descToken})
	_, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, HistoryDescending, reader.request.Order)
	require.Equal(t, int64(3), reader.request.Cursor.ReceiveOrder)
	require.Equal(t, int64(4), reader.request.Cursor.HighWater)
	decoded, err := base64.RawURLEncoding.DecodeString(descToken)
	require.NoError(t, err)
	for _, invalid := range []string{descToken + "=", "!", base64.RawURLEncoding.EncodeToString(append(decoded, []byte(` {}`)...)), base64.RawURLEncoding.EncodeToString(append([]byte(" "), decoded...))} {
		query.Result = HistoryObservations{Order: HistoryDescending, Limit: 1, Cursor: invalid}
		_, err = service.QueryHistory(context.Background(), query)
		requireHistoryError(t, err, "invalid_cursor")
	}
	query.Result = HistoryObservations{Order: HistoryAscending, Limit: 1, Cursor: descToken}
	_, err = service.QueryHistory(context.Background(), query)
	requireHistoryError(t, err, "invalid_cursor")
}

func TestHistorySummaryOutputLowerBound(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		label string
		fail  bool
	}{{"small", "minimum", false}, {"large", strings.Repeat("x", 5000), true}} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			service := historyTestService(&historyReaderDouble{})
			query := historyTestQuery(
				HistorySummary{
					Metrics: map[string]HistoryMetric{tc.label: HistoryMinimum{}},
					Bucket:  &HistoryBucket{Duration: 4 * time.Second, Alignment: "range_start"},
				},
			)
			result, err := service.QueryHistory(context.Background(), query)
			if tc.fail {
				requireHistoryError(t, err, "query_limit")
				return
			}
			require.NoError(t, err)
			buckets := result.Data.(HistoryBucketedSummaryResult).Buckets
			require.Len(t, buckets, 900)
			require.Equal(t, "null", string(buckets[899].Metrics[tc.label]))
		})
	}
}

func TestHistoryCorruptRetainedValueRemainsInternal(t *testing.T) {
	t.Parallel()
	for _, raw := range []string{`{"x":1,"x":2}`, `"invalid`, "\"\xff\""} {
		reader := &historyReaderDouble{
			window: HistoryWindow{Rows: []EntityStateHistoryEntry{historyTestRow(0, 1, raw)}},
		}
		_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(HistoryObservations{}))
		require.Error(t, err)
		var queryError *HistoryQueryError
		require.NotErrorAs(t, err, &queryError, "corrupt source must not blame the query")
		require.NotContains(t, err.Error(), raw)
	}
}

func TestHistoryUnfilteredPagesRequestOnlyNeededPrefix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		result HistoryResultSpec
		limit  int
	}{
		{HistoryObservations{}, 51},
		{HistoryObservations{Limit: 200, Order: HistoryDescending}, 201},
		{HistoryObservations{Limit: 2, WhereExpr: new("value == true")}, 0},
		{HistoryTimeline{}, 0},
	} {
		reader := &historyReaderDouble{}
		_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(tc.result))
		require.NoError(t, err)
		require.Equal(t, tc.limit, reader.request.SourceRowLimit)
	}
}
