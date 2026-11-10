package mcpapi //nolint:testpackage // Exercise registered MCP decoding and result validation.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	mcptools "github.com/mholtzscher/hearth/internal/platform/mcpapi"
)

func TestQueryEntityStateHistoryTool(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(&devices.State{Value: []byte(`{"active":true,"x":1000,"y":5000}`)})
	view.Entity.TypeID = devices.EntityTypeColorxyV1
	view.Entity.Support = []byte(`{}`)
	called := 0
	stub := &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) { return view, nil },
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			called++
			require.Equal(t, "/x", q.Field)
			require.Equal(t, devices.HistoryNullGap, q.Nulls)
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistoryBucketedSummaryResult{
					Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
					Buckets: []devices.HistorySummaryBucket{
						{
							Range: q.Range,
							Metrics: devices.HistoryMetricValues{
								"min":  json.RawMessage("1000"),
								"mean": json.RawMessage("null"),
							},
						},
					},
				},
			}, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, stub, testPresenter())
	base := `{"entity_id":"` + string(
		apiEntityID,
	) + `","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"select_field":"/x","result":{"kind":"summary","nulls":"gap","metrics":{"min":{"operation":"min"},"mean":{"operation":"sample_mean"}}}}`
	for _, tc := range []struct{ args, value, unit string }{
		{base, `"min":0.1`, `"unit":"1"`},
		{strings.TrimSuffix(base, "}") + `,"units":"canonical"}`, `"min":1000`, `"unit":"10*-4"`},
	} {
		result := callWire(t, server.HTTPHandler(), "query_entity_state_history", tc.args)
		require.False(t, result.IsError, string(result.Structured))
		require.Contains(t, string(result.Structured), tc.value)
		require.Contains(t, string(result.Structured), tc.unit)
		require.Contains(t, string(result.Structured), `"mean":null`)
		require.Contains(t, string(result.Structured), `"coverage"`)
	}
	require.Equal(t, 2, called)
	for _, args := range []string{
		strings.Replace(base, `"nulls":"gap"`, `"nulls":null`, 1),
		strings.Replace(base, `"operation":"min"`, `"Operation":"min"`, 1),
		strings.Replace(base, `"select_field":"/x"`, `"select_field":"/x","extra":"private_sentinel"`, 1),
		strings.Replace(base, `"metrics":{`, `"bucket":null,"metrics":{`, 1),
	} {
		result := callWire(t, server.HTTPHandler(), "query_entity_state_history", args)
		require.True(t, result.IsError)
		require.NotContains(t, string(result.Structured), "private_sentinel")
	}
	require.Equal(t, 2, called)
}

func TestQueryEntityStateHistoryFailureMapping(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err  error
		code string
	}{
		{devices.ErrEntityNotFound, "entity_not_found"},
		{&devices.HistoryQueryError{Code: "invalid_query"}, "invalid_query"},
		{&devices.HistoryQueryError{Code: "query_limit"}, "query_limit"},
		{&devices.HistoryQueryError{Code: "value_type"}, "value_type"},
		{&devices.HistoryQueryError{Code: "unsupported_source"}, "unsupported_source"},
		{errors.New("private_sentinel"), "internal_error"},
	} {
		stub := &stubDevices{
			queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
				return devices.HistoryQueryResult{}, tc.err
			},
		}
		server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
		Register(server, stub, testPresenter())
		args := `{"entity_id":"` + string(
			apiEntityID,
		) + `","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"select_field":"/x","result":{"kind":"summary","metrics":{"min":{"operation":"min"}}}}`
		result := callWire(t, server.HTTPHandler(), "query_entity_state_history", args)
		require.True(t, result.IsError)
		require.Contains(t, string(result.Structured), `"`+tc.code+`"`)
		require.NotContains(t, string(result.Structured), "private_sentinel")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := historyFailure(ctx, context.Canceled)
	var failure *mcptools.ToolError
	require.ErrorAs(t, err, &failure)
	require.Equal(t, "query_canceled", failure.Code)
}

func TestQueryEntityStateHistoryLimitIncludesQuantity(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(nil)
	view.Entity.TypeID = devices.EntityTypeColorxyV1
	view.Entity.Support = []byte(`{}`)
	stub := &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) { return view, nil },
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistorySummaryResult{
					Metrics: devices.HistoryMetricValues{
						strings.Repeat("a", devices.HistoryMaxResultBytes): json.RawMessage("1"),
					},
				},
			}, nil
		},
	}
	server := mcptools.New(mcptools.Config{Name: "test", Version: "1"})
	Register(server, stub, testPresenter())
	args := `{"entity_id":"` + string(
		apiEntityID,
	) + `","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"select_field":"/x","result":{"kind":"summary","metrics":{"min":{"operation":"min"}}}}`
	result := callWire(t, server.HTTPHandler(), "query_entity_state_history", args)
	require.True(t, result.IsError)
	require.Contains(t, string(result.Structured), `"query_limit"`)
}
