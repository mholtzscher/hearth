//nolint:testpackage // Exercises the Devices service and its reader seam together.
package devices

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// historyChunkedRows has a tie at minute 20 split across the first chunk
// boundary: the later receive-ordered report (false) is the held value, so a
// fold that judged the earlier report on its own would count a transition.
func historyChunkedRows() (EntityStateHistoryEntry, []EntityStateHistoryEntry) {
	seed := historyTestRow(-5, 1, "0")
	return seed, []EntityStateHistoryEntry{
		historyTestRow(5, 2, "1"),
		historyTestRow(20, 3, "4"),
		historyTestRow(20, 4, "0"),
		historyTestRow(35, 5, "2"),
		historyTestRow(40, 6, "3"),
		historyTestRow(55, 7, "0"),
	}
}

func historyChunkedQueries() map[string]HistoryQuery {
	bucket := &HistoryBucket{Duration: 20 * time.Minute, Alignment: "range_start"}
	return map[string]HistoryQuery{
		"timeline": historyTestQuery(HistoryTimeline{}),
		"bucketed summary": historyTestQuery(HistorySummary{Bucket: bucket, Metrics: map[string]HistoryMetric{
			"min":     HistoryMinimum{},
			"max":     HistoryMaximum{},
			"mean":    HistorySampleMean{},
			"high":    HistoryDurationWhere{WhereExpr: "value >= 3", Unit: "seconds"},
			"entries": HistoryTransitionsInto{WhereExpr: "value >= 3"},
		}}),
	}
}

func TestQueryHistoryChunkedReadMatchesSingleRead(t *testing.T) {
	t.Parallel()
	for name, query := range historyChunkedQueries() {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			seed, rows := historyChunkedRows()
			whole := &historyReaderDouble{window: HistoryWindow{Seed: &seed, HighWater: 9, Rows: rows}}
			want, err := historyTestService(whole).QueryHistory(context.Background(), query)
			require.NoError(t, err)

			chunked := &historyReaderDouble{windows: []HistoryWindow{
				{Seed: &seed, HighWater: 9, Rows: rows[:2], HasMore: true},
				{HighWater: 9, Rows: rows[2:5], HasMore: true},
				{HighWater: 9, Rows: rows[5:]},
			}}
			got, err := historyTestService(chunked).QueryHistory(context.Background(), query)
			require.NoError(t, err)
			require.Equal(t, want, got)

			require.Len(t, chunked.requests, 3)
			first := chunked.requests[0]
			require.Nil(t, first.Cursor)
			require.True(t, first.IncludeSeed, "both queries are temporal")
			for i, request := range chunked.requests[1:] {
				last := []EntityStateHistoryEntry{rows[1], rows[4]}[i]
				require.False(t, request.IncludeSeed)
				require.Equal(t, HistoryAscending, request.Order)
				require.Zero(t, request.SourceRowLimit)
				require.Equal(t, &HistoryReadCursor{
					ObservedAt: last.ObservedAt, ReceiveOrder: last.ReceiveOrder, HighWater: 9,
				}, request.Cursor)
			}
		})
	}
}

func TestQueryHistoryChunkedTieIsJudgedByItsLastReport(t *testing.T) {
	t.Parallel()
	seed, rows := historyChunkedRows()
	reader := &historyReaderDouble{windows: []HistoryWindow{
		{Seed: &seed, HighWater: 9, Rows: rows[:2], HasMore: true},
		{HighWater: 9, Rows: rows[2:]},
	}}
	query := historyTestQuery(HistorySummary{Metrics: map[string]HistoryMetric{
		"entries": HistoryTransitionsInto{WhereExpr: "value >= 3"},
	}})
	result, err := historyTestService(reader).QueryHistory(context.Background(), query)
	require.NoError(t, err)
	// Only minute 40 enters value >= 3; minute 20 settles at 0.
	require.Equal(t, "1", string(result.Data.(HistorySummaryResult).Metrics["entries"]))
}

func TestQueryHistoryChunkedReadWithoutProgressIsQueryLimit(t *testing.T) {
	t.Parallel()
	seed := historyTestRow(-1, 1, "true")
	reader := &historyReaderDouble{window: HistoryWindow{Seed: &seed, HighWater: 1, HasMore: true}}
	_, err := historyTestService(reader).QueryHistory(context.Background(), historyTestQuery(HistoryTimeline{}))
	requireHistoryError(t, err, "query_limit")
	var failure *HistoryQueryError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "range", failure.Field)
	require.Len(t, reader.requests, 1)
}
