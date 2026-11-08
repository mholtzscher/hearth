//nolint:testpackage // Exercises the Devices service and its reader seam together.
package devices

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type historyReaderDouble struct {
	window HistoryWindow
	// windows, when set, answers successive reads in order instead of window.
	windows  []HistoryWindow
	err      error
	request  HistoryReadRequest
	requests []HistoryReadRequest
}

func (r *historyReaderDouble) ReadHistoryWindow(_ context.Context, request HistoryReadRequest) (HistoryWindow, error) {
	r.request = request
	r.requests = append(r.requests, request)
	if len(r.windows) > 0 {
		window := r.windows[0]
		r.windows = r.windows[1:]
		return window, r.err
	}
	return r.window, r.err
}

//nolint:gochecknoglobals // Shared immutable fixture instant.
var historyTestStart = time.Date(
	2026,
	11,
	1,
	9,
	0,
	0,
	0,
	time.UTC,
)

const historyTestEntity EntityID = "ent_019a1b2c-3d4e-7000-8000-000000000001"

func historyTestRow(minute int, order int64, value string) EntityStateHistoryEntry {
	at := historyTestStart.Add(time.Duration(minute) * time.Minute)
	return EntityStateHistoryEntry{
		ObservationID:     ObservationID(fmt.Sprintf("obs_%d", order)),
		ObservedAt:        at,
		AdapterReceivedAt: at,
		ReceiveOrder:      order,
		Value:             Value(value),
		Disposition:       DispositionApplied,
	}
}
func historyTestService(reader *historyReaderDouble) *Service {
	return NewService(
		Stores{History: reader},
		nil,
		nil,
		Dependencies{Now: func() time.Time { return historyTestStart.Add(24 * time.Hour) }},
	)
}
func historyTestQuery(result HistoryResultSpec) HistoryQuery {
	return HistoryQuery{
		EntityID: historyTestEntity,
		Range:    HistoryRange{From: historyTestStart, To: historyTestStart.Add(time.Hour)},
		Result:   result,
	}
}
func TestQueryHistoryTemporalExampleAndBuckets(t *testing.T) {
	t.Parallel()
	seed := historyTestRow(-1, 1, "false")
	reader := &historyReaderDouble{
		window: HistoryWindow{
			Seed:      &seed,
			HighWater: 4,
			Rows: []EntityStateHistoryEntry{
				historyTestRow(10, 2, "true"),
				historyTestRow(12, 3, "true"),
				historyTestRow(15, 4, "false"),
			},
		},
	}
	service := historyTestService(reader)
	timeline, err := service.QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	require.NoError(t, err)
	data := timeline.Data.(HistoryTimelineResult)
	require.True(t, reader.request.IncludeSeed)
	require.InDelta(t, 3600.0, data.Coverage.InferredKnownSeconds, 0.000001)
	require.Len(t, data.Intervals, 3)
	require.Equal(t, "true", string(data.Intervals[1].(HistoryValueInterval).Value))
	metrics := map[string]HistoryMetric{
		"open_seconds": HistoryDurationWhere{WhereExpr: "value == true", Unit: "seconds"},
		"openings":     HistoryTransitionsInto{WhereExpr: "value == true"},
	}
	summary, err := service.QueryHistory(context.Background(), historyTestQuery(HistorySummary{Metrics: metrics}))
	require.NoError(t, err)
	require.Equal(t, "300", string(summary.Data.(HistorySummaryResult).Metrics["open_seconds"]))
	require.Equal(t, "1", string(summary.Data.(HistorySummaryResult).Metrics["openings"]))
	bucketed, err := service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: metrics,
				Bucket:  &HistoryBucket{Duration: 10 * time.Minute, Alignment: "range_start"},
			},
		),
	)
	require.NoError(t, err)
	buckets := bucketed.Data.(HistoryBucketedSummaryResult).Buckets
	require.Len(t, buckets, 6)
	require.Equal(t, "300", string(buckets[1].Metrics["open_seconds"]))
	require.Equal(t, "1", string(buckets[1].Metrics["openings"]))
	require.Equal(t, "0", string(buckets[0].Metrics["openings"]))
}

func TestQueryHistoryUnknownNullTiesAndBoundary(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{
			Rows: []EntityStateHistoryEntry{
				historyTestRow(10, 1, "false"),
				historyTestRow(10, 2, "null"),
				historyTestRow(20, 3, "true"),
			},
		},
	}
	service := historyTestService(reader)
	result, err := service.QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	require.NoError(t, err)
	data := result.Data.(HistoryTimelineResult)
	require.Len(t, data.Intervals, 3)
	require.IsType(t, HistoryUnknownInterval{}, data.Intervals[0])
	require.Equal(t, "null", string(data.Intervals[1].(HistoryValueInterval).Value))
	require.InDelta(t, 600.0, data.Coverage.UnknownSeconds, 0.000001)
	result, err = service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: map[string]HistoryMetric{"open": HistoryTransitionsInto{WhereExpr: "value == true"}},
			},
		),
	)
	require.NoError(t, err)
	require.Equal(t, "1", string(result.Data.(HistorySummaryResult).Metrics["open"]))
	reader.window.Rows = []EntityStateHistoryEntry{historyTestRow(0, 1, "true")}
	result, err = service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: map[string]HistoryMetric{"open": HistoryTransitionsInto{WhereExpr: "value == true"}},
			},
		),
	)
	require.NoError(t, err)
	require.Equal(t, "0", string(result.Data.(HistorySummaryResult).Metrics["open"]))
	seed := historyTestRow(-1, 2, "false")
	reader.window.Seed = &seed
	result, err = service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: map[string]HistoryMetric{"open": HistoryTransitionsInto{WhereExpr: "value == true"}},
			},
		),
	)
	require.NoError(t, err)
	require.Equal(t, "1", string(result.Data.(HistorySummaryResult).Metrics["open"]))
}

func TestQueryHistoryStructuredSelectionAndNumericPrecision(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{
			Rows: []EntityStateHistoryEntry{
				historyTestRow(0, 1, `{"v":{"a":9007199254740993,"b":[1,2]},"other":1}`),
				historyTestRow(10, 2, `{"other":2,"v":{"b":[1,2],"a":9007199254740993}}`),
			},
		},
	}
	service := historyTestService(reader)
	query := historyTestQuery(HistoryTimeline{})
	query.SelectExpr = new("sample.value.v")
	result, err := service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, result.Data.(HistoryTimelineResult).Intervals, 1)
	require.JSONEq(
		t,
		`{"a":9007199254740993,"b":[1,2]}`,
		string(result.Data.(HistoryTimelineResult).Intervals[0].(HistoryValueInterval).Value),
	)
	reader.window.Rows = []EntityStateHistoryEntry{
		historyTestRow(0, 1, "9007199254740993"),
		historyTestRow(10, 2, "9007199254740992.0"),
		historyTestRow(20, 3, "9007199254740995"),
	}
	result, err = service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: map[string]HistoryMetric{
					"min":  HistoryMinimum{},
					"max":  HistoryMaximum{},
					"mean": HistorySampleMean{},
				},
			},
		),
	)
	require.NoError(t, err)
	values := result.Data.(HistorySummaryResult).Metrics
	require.Equal(t, "9007199254740992", string(values["min"]))
	require.Equal(t, "9007199254740995", string(values["max"]))
	require.Nil(t, result.Data.(HistorySummaryResult).Coverage)
	require.False(t, reader.request.IncludeSeed)
}

func TestQueryHistoryObservationCursorAndErrors(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{
		window: HistoryWindow{
			HighWater: 3,
			Rows: []EntityStateHistoryEntry{
				historyTestRow(0, 1, "false"),
				historyTestRow(1, 2, "true"),
				historyTestRow(2, 3, "true"),
			},
		},
	}
	service := historyTestService(reader)
	query := historyTestQuery(HistoryObservations{WhereExpr: new("value"), Limit: 1})
	result, err := service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	page := result.Data.(HistoryObservationsResult)
	require.Len(t, page.Items, 1)
	require.Equal(t, "obs_2", string(page.Items[0].ObservationID))
	require.NotEmpty(t, page.NextCursor)
	spec := query.Result.(HistoryObservations)
	spec.Cursor = page.NextCursor
	query.Result = spec
	reader.window.Rows = []EntityStateHistoryEntry{historyTestRow(2, 3, "true")}
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, int64(2), reader.request.Cursor.ReceiveOrder)
	require.Empty(t, result.Data.(HistoryObservationsResult).NextCursor)
	spec.Limit = 2
	query.Result = spec
	_, err = service.QueryHistory(context.Background(), query)
	requireHistoryError(t, err, "invalid_cursor")
	query = historyTestQuery(HistoryObservations{WhereExpr: new("false")})
	reader.window.HasMore = true
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Empty(t, result.Data.(HistoryObservationsResult).Items)
	require.NotEmpty(t, result.Data.(HistoryObservationsResult).NextCursor)
	reader.err = errors.New("storage failure")
	_, err = service.QueryHistory(context.Background(), query)
	require.ErrorIs(t, err, reader.err)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.QueryHistory(ctx, query)
	require.ErrorIs(t, err, context.Canceled)
}

func TestQueryHistoryValidationAndFailures(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{}
	service := historyTestService(reader)
	var nilSummary *HistorySummary
	for _, spec := range []HistoryResultSpec{nil, nilSummary, HistorySummary{Metrics: map[string]HistoryMetric{"bad": (*HistoryMinimum)(nil)}}, HistoryObservations{Order: "bad"}} {
		_, err := service.QueryHistory(context.Background(), historyTestQuery(spec))
		require.Error(t, err)
	}
	reader.window.Rows = []EntityStateHistoryEntry{historyTestRow(0, 1, "true")}
	_, err := service.QueryHistory(
		context.Background(),
		historyTestQuery(HistoryObservations{WhereExpr: new("1")}),
	)
	requireHistoryError(t, err, "expression_type")
	reader.window.Rows[0].Value = Value("{")
	_, err = service.QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	var failure *HistoryQueryError
	require.Error(t, err)
	require.NotErrorAs(t, err, &failure)
	reader.window = HistoryWindow{HasMore: true}
	_, err = service.QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	requireHistoryError(t, err, "query_limit")
	query := historyTestQuery(HistoryObservations{Cursor: strings.Repeat("!", 3)})
	_, err = service.QueryHistory(context.Background(), query)
	requireHistoryError(t, err, "invalid_cursor")
}

func requireHistoryError(t *testing.T, err error, code string) {
	t.Helper()
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, code, failure.Code)
}

func TestQueryHistoryExpandedValuesAreReducedBeforeRetention(t *testing.T) {
	t.Parallel()
	value := `"` + strings.Repeat("x", 32000) + `"`
	rows := make([]EntityStateHistoryEntry, 100)
	for i := range rows {
		rows[i] = historyTestRow(i%60, int64(i+1), value)
		rows[i].ObservedAt = historyTestStart.Add(time.Duration(i) * time.Second)
	}
	reader := &historyReaderDouble{window: HistoryWindow{HighWater: 100, Rows: rows}}
	service := historyTestService(reader)
	query := historyTestQuery(HistoryTimeline{})
	query.SelectExpr = new("sample.value + sample.value")
	result, err := service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Len(t, result.Data.(HistoryTimelineResult).Intervals, 1)
	query.Result = HistorySummary{
		Metrics: map[string]HistoryMetric{"seconds": HistoryDurationWhere{WhereExpr: `value != ""`, Unit: "seconds"}},
	}
	result, err = service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.Equal(t, "3600", string(result.Data.(HistorySummaryResult).Metrics["seconds"]))
	query.Result = HistoryObservations{Limit: 100}
	_, err = service.QueryHistory(context.Background(), query)
	requireHistoryError(t, err, "query_limit")
}

func TestQueryHistoryEvaluationBudgetAndEmptyNumericBuckets(t *testing.T) {
	t.Parallel()
	reader := &historyReaderDouble{}
	service := historyTestService(reader)
	result, err := service.QueryHistory(
		context.Background(),
		historyTestQuery(
			HistorySummary{
				Metrics: map[string]HistoryMetric{"min": HistoryMinimum{}},
				Bucket:  &HistoryBucket{Duration: 20 * time.Minute, Alignment: "range_start"},
			},
		),
	)
	require.NoError(t, err)
	buckets := result.Data.(HistoryBucketedSummaryResult).Buckets
	require.Len(t, buckets, 3)
	for _, bucket := range buckets {
		require.Equal(t, "null", string(bucket.Metrics["min"]))
		require.Nil(t, bucket.Coverage)
	}
	metrics := make(map[string]HistoryMetric)
	for i := range 16 {
		metrics[fmt.Sprintf("m%d", i)] = HistoryTransitionsInto{WhereExpr: "value"}
	}
	// 17 evaluations per row: the selection plus 16 predicates.
	reader.window.Rows = make([]EntityStateHistoryEntry, HistoryMaxEvaluations/17+1)
	for i := range reader.window.Rows {
		row := historyTestRow(0, int64(i+1), "true")
		row.ObservedAt = historyTestStart.Add(time.Duration(i) * time.Millisecond)
		reader.window.Rows[i] = row
	}
	_, err = service.QueryHistory(context.Background(), historyTestQuery(HistorySummary{Metrics: metrics}))
	requireHistoryError(t, err, "query_limit")
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "range", failure.Field)
}

func TestQueryHistoryLongRangeAndPreReadValidation(t *testing.T) {
	t.Parallel()
	from := time.Date(1600, 1, 1, 0, 0, 0, 0, time.UTC)
	to := time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC)
	reader := &historyReaderDouble{}
	service := historyTestService(reader)
	query := HistoryQuery{
		EntityID: historyTestEntity,
		Range:    HistoryRange{From: from, To: to},
		Result:   HistoryTimeline{},
	}
	result, err := service.QueryHistory(context.Background(), query)
	require.NoError(t, err)
	require.InDelta(
		t,
		float64(to.Unix()-from.Unix()),
		result.Data.(HistoryTimelineResult).Coverage.UnknownSeconds,
		0.000001,
	)
	reader.request = HistoryReadRequest{}
	query.SelectExpr = new("missing.root")
	_, err = service.QueryHistory(context.Background(), query)
	requireHistoryError(t, err, "expression_type")
	require.Empty(t, reader.request.EntityID)
}
