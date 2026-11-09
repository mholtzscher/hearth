//nolint:testpackage // Exercises the Devices service and its State change reader seam together.
package devices

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type stateChangeReaderDouble struct {
	rows  StateChangeRows
	err   error
	reads []StateChangeRead
}

func (r *stateChangeReaderDouble) ReadStateChanges(_ context.Context, read StateChangeRead) (StateChangeRows, error) {
	r.reads = append(r.reads, read)
	return r.rows, r.err
}

func stateChangeTestService(reader *stateChangeReaderDouble) *Service {
	return NewService(Stores{StateChanges: reader}, nil, nil,
		Dependencies{Now: func() time.Time { return historyTestStart().Add(24 * time.Hour) }})
}

// at returns the test start plus minutes.
func at(minutes int) time.Time { return historyTestStart().Add(time.Duration(minutes) * time.Minute) }

func change(minutes int, value string) StateChangeRow {
	return StateChangeRow{ObservedAt: at(minutes), Value: json.RawMessage(value)}
}

type wantInterval struct {
	from, to int
	known    bool
	value    string
}

func requireIntervals(t *testing.T, got StateChangeTimeline, want []wantInterval) {
	t.Helper()
	rendered := make([]wantInterval, len(got.Intervals))
	for i, interval := range got.Intervals {
		rendered[i] = wantInterval{
			from:  int(interval.Range.From.Sub(historyTestStart()) / time.Minute),
			to:    int(interval.Range.To.Sub(historyTestStart()) / time.Minute),
			known: interval.Known, value: string(interval.Value),
		}
	}
	require.Equal(t, want, rendered)
}

func TestStateChangeTimelineHoldsEachValueUntilTheNextChange(t *testing.T) {
	t.Parallel()
	window := HistoryRange{From: at(0), To: at(60)}
	tests := []struct {
		name        string
		rows        StateChangeRows
		want        []wantInterval
		transitions []int
	}{
		{
			name: "unseeded range starts unknown and never counts unknown to true",
			rows: StateChangeRows{Rows: []StateChangeRow{change(10, "true"), change(20, "false"), change(30, "true")}},
			want: []wantInterval{
				{0, 10, false, ""}, {10, 20, true, "true"}, {20, 30, true, "false"}, {30, 60, true, "true"},
			},
			transitions: []int{30},
		},
		{
			name: "seed sets the opening value and equal neighbors coalesce",
			rows: StateChangeRows{
				Seed: &StateChangeRow{ObservedAt: at(-90), Value: json.RawMessage("false")},
				Rows: []StateChangeRow{change(15, "false"), change(40, "true")},
			},
			want:        []wantInterval{{0, 40, true, "false"}, {40, 60, true, "true"}},
			transitions: []int{40},
		},
		{
			name: "a change at the range start replaces the seed and counts as a transition",
			rows: StateChangeRows{
				Seed: &StateChangeRow{ObservedAt: at(-1), Value: json.RawMessage("false")},
				Rows: []StateChangeRow{change(0, "true")},
			},
			want:        []wantInterval{{0, 60, true, "true"}},
			transitions: []int{0},
		},
		{
			name: "a known null is a value, and null to true is established",
			rows: StateChangeRows{
				Seed: &StateChangeRow{ObservedAt: at(-5), Value: json.RawMessage("null")},
				Rows: []StateChangeRow{change(30, "true")},
			},
			want:        []wantInterval{{0, 30, true, "null"}, {30, 60, true, "true"}},
			transitions: []int{30},
		},
		{
			name: "simultaneous changes collapse without a zero-length interval",
			rows: StateChangeRows{
				Seed: &StateChangeRow{ObservedAt: at(-5), Value: json.RawMessage(`"Heat"`)},
				Rows: []StateChangeRow{change(20, `"Eco"`), change(20, `"Heat"`), change(45, `"Off"`)},
			},
			want: []wantInterval{{0, 45, true, `"Heat"`}, {45, 60, true, `"Off"`}},
		},
		{
			name: "no changes at all is one unknown interval",
			rows: StateChangeRows{},
			want: []wantInterval{{0, 60, false, ""}},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			timeline, transitions := assembleStateChangeTimeline(window, test.rows)
			requireIntervals(t, timeline, test.want)
			minutes := make([]int, len(transitions))
			for i, instant := range transitions {
				minutes[i] = int(instant.Sub(historyTestStart()) / time.Minute)
			}
			if test.transitions == nil {
				test.transitions = []int{}
			}
			require.Equal(t, test.transitions, minutes)
		})
	}
}

func TestQueryStateChangesSummarizesTimeTrueAndTransitionsPerBucket(t *testing.T) {
	t.Parallel()
	reader := &stateChangeReaderDouble{rows: StateChangeRows{Rows: []StateChangeRow{
		change(10, "true"), change(25, "false"), change(30, "true"), change(50, "false"),
	}}}
	service := stateChangeTestService(reader)
	bucket := HistoryBucket{Duration: 20 * time.Minute, Alignment: "range_start"}
	result, err := service.QueryStateChanges(t.Context(), StateChangeQuery{
		EntityID: historyTestEntity, Field: "", Range: HistoryRange{From: at(0), To: at(55)}, Bucket: &bucket,
	})
	require.NoError(t, err)
	require.Equal(t, []StateChangeRead{
		{
			EntityID: historyTestEntity,
			Range:    HistoryRange{From: at(0), To: at(55)},
			Boolean:  true,
			Limit:    StateChangeMaxRows,
		},
	}, reader.reads)
	require.False(t, result.Seeded)
	summary, ok := result.Data.(StateChangeSummary)
	require.True(t, ok)
	require.Equal(t, bucket, summary.Bucket)
	// 0–10 is unknown; 10 to true is from unknown, so only 30 is established.
	require.Equal(t, []StateChangeBucket{
		{Range: HistoryRange{From: at(0), To: at(20)}, SecondsTrue: 600, KnownSeconds: 600},
		{
			Range:               HistoryRange{From: at(20), To: at(40)},
			SecondsTrue:         300 + 600,
			TransitionsIntoTrue: 1,
			KnownSeconds:        1200,
		},
		{Range: HistoryRange{From: at(40), To: at(55)}, SecondsTrue: 600, KnownSeconds: 900},
	}, summary.Buckets)
}

func TestQueryStateChangesReturnsATimelineAndReportsTheSeed(t *testing.T) {
	t.Parallel()
	reader := &stateChangeReaderDouble{rows: StateChangeRows{
		Seed: &StateChangeRow{ObservedAt: at(-30), Value: json.RawMessage(`"Eco"`)},
		Rows: []StateChangeRow{change(30, `"Heat"`)},
	}}
	result, err := stateChangeTestService(reader).QueryStateChanges(t.Context(), StateChangeQuery{
		EntityID: historyTestEntity, Field: "/mode", Range: HistoryRange{From: at(0), To: at(60)},
	})
	require.NoError(t, err)
	require.False(t, reader.reads[0].Boolean)
	require.Equal(t, "/mode", result.Field)
	require.True(t, result.Seeded)
	timeline, ok := result.Data.(StateChangeTimeline)
	require.True(t, ok)
	requireIntervals(t, timeline, []wantInterval{{0, 30, true, `"Eco"`}, {30, 60, true, `"Heat"`}})
}

func TestQueryStateChangesRejectsInvalidQueriesBeforeReading(t *testing.T) {
	t.Parallel()
	valid := func() StateChangeQuery {
		return StateChangeQuery{EntityID: historyTestEntity, Range: HistoryRange{From: at(0), To: at(60)}}
	}
	tests := []struct {
		name   string
		mutate func(*StateChangeQuery)
		code   string
		field  string
	}{
		{"bad entity", func(q *StateChangeQuery) { q.EntityID = "nope" }, "invalid_query", "entity_id"},
		{"empty range", func(q *StateChangeQuery) { q.Range.To = q.Range.From }, "invalid_query", "range"},
		{"future end", func(q *StateChangeQuery) { q.Range.To = at(48 * 60) }, "invalid_query", "range"},
		{"non-UTC", func(q *StateChangeQuery) { q.Range.From = q.Range.From.In(time.FixedZone("x", 3600)) },
			"invalid_query", "range"},
		{"field syntax", func(q *StateChangeQuery) { q.Field = "mode" }, "invalid_query", "select_field"},
		{"bucket alignment", func(q *StateChangeQuery) {
			q.Bucket = &HistoryBucket{Duration: time.Hour, Alignment: "calendar"}
		}, "invalid_query", "result.bucket"},
		{"sub-minute bucket", func(q *StateChangeQuery) {
			q.Bucket = &HistoryBucket{Duration: 30 * time.Second, Alignment: "range_start"}
		}, "invalid_query", "result.bucket"},
		{"too many buckets", func(q *StateChangeQuery) {
			q.Range.To = at(1001)
			q.Bucket = &HistoryBucket{Duration: time.Minute, Alignment: "range_start"}
		}, "query_limit", "result.bucket"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			reader := &stateChangeReaderDouble{}
			query := valid()
			test.mutate(&query)
			_, err := stateChangeTestService(reader).QueryStateChanges(t.Context(), query)
			var failure *HistoryQueryError
			require.ErrorAs(t, err, &failure)
			require.Equal(t, test.code, failure.Code)
			require.Equal(t, test.field, failure.Field)
			require.Empty(t, reader.reads)
		})
	}
}

func TestQueryStateChangesPassesReaderFailuresThrough(t *testing.T) {
	t.Parallel()
	reader := &stateChangeReaderDouble{err: ErrEntityNotFound}
	_, err := stateChangeTestService(reader).QueryStateChanges(t.Context(), StateChangeQuery{
		EntityID: historyTestEntity, Range: HistoryRange{From: at(0), To: at(60)},
	})
	require.ErrorIs(t, err, ErrEntityNotFound)
}
