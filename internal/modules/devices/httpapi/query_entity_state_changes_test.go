package httpapi //nolint:testpackage // Exercise the registered transport boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	changesTimelineBody = `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"timeline"}}`
	changesOpenAPIPath  = "https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1changes/post/"
)

func changesPost(router http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/state/changes",
		strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func changesPublishedSchema(t *testing.T, api huma.API, pointer string) *jsonschema.Schema {
	t.Helper()
	published, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(published)))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://hearth.invalid/openapi", document))
	schema, err := compiler.Compile(changesOpenAPIPath + pointer)
	require.NoError(t, err)
	return schema
}

func changesWindow() devices.HistoryRange {
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	return devices.HistoryRange{From: start, To: start.Add(time.Hour)}
}

func TestStateChangesDecodesTimelineAndSummaryRequests(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		body string
		want devices.StateChangeQuery
	}{
		{
			name: "timeline",
			body: `{"range":{"from":"2026-11-01T10:00:00+01:00","to":"2026-11-01T10:00:00Z"},` +
				`"select_field":"/mode","result":{"kind":"timeline"}}`,
			want: devices.StateChangeQuery{EntityID: apiEntityID, Field: "/mode", Range: changesWindow()},
		},
		{
			name: "summary",
			body: `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary",` +
				`"bucket":{"duration":"1h","alignment":"range_start"}}}`,
			want: devices.StateChangeQuery{
				EntityID: apiEntityID, Range: changesWindow(),
				Bucket: &devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, &stubDevices{
				queryStateChanges: func(_ context.Context, q devices.StateChangeQuery) (devices.StateChangeQueryResult, error) {
					require.Equal(t, tc.want, q)
					return devices.StateChangeQueryResult{
						EntityID: q.EntityID, Range: q.Range, Data: devices.StateChangeTimeline{},
					}, nil
				},
			})
			rec := changesPost(router, tc.body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
		})
	}
}

func TestStateChangesRejectsMalformedRequestsBeforeTheService(t *testing.T) {
	t.Parallel()
	router, api := testAPI(t, &stubDevices{})
	request := changesPublishedSchema(t, api, "requestBody/content/application~1json/schema")
	for _, tc := range []struct {
		name          string
		body          string
		schemaRejects bool
	}{
		{"unknown kind", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"observations"}}`, true},
		{"unknown property", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"timeline"},"x":1}`, true},
		{"null bucket", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"timeline","bucket":null}}`, true},
		{"missing select_field", `{` + historyRangeJSON + `,"result":{"kind":"timeline"}}`, true},
		{"case variant key", `{` + historyRangeJSON + `,"Select_field":"","result":{"kind":"timeline"}}`, true},
		{"summary without bucket", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary"}}`, false},
		{"timeline with bucket", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"timeline",` +
			`"bucket":{"duration":"1h","alignment":"range_start"}}}`, false},
		{"bad duration", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary",` +
			`"bucket":{"duration":"an hour","alignment":"range_start"}}}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := changesPost(router, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.body))
			require.NoError(t, err)
			if tc.schemaRejects {
				require.Error(t, request.Validate(value), "published schema accepted %s", tc.body)
			}
		})
	}
}

func TestStateChangesWireResultsMatchThePublishedSchema(t *testing.T) {
	t.Parallel()
	window := changesWindow()
	half := devices.HistoryRange{From: window.From, To: window.From.Add(30 * time.Minute)}
	rest := devices.HistoryRange{From: half.To, To: window.To}
	const envelope = `"entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","select_field":"",` +
		`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",`
	for _, tc := range []struct {
		name   string
		result devices.StateChangeQueryResult
		want   string
	}{
		{
			name: "timeline",
			result: devices.StateChangeQueryResult{Data: devices.StateChangeTimeline{Intervals: []devices.StateChangeInterval{
				{Range: half}, {Range: rest, Known: true, Value: json.RawMessage("null")},
			}}},
			want: `{"kind":"timeline",` + envelope +
				`"coverage":{"basis":"retained_changes","completeness":"unverified","seeded":false},"intervals":[` +
				`{"kind":"unknown","from":"2026-11-01T09:00:00Z","to":"2026-11-01T09:30:00Z"},` +
				`{"kind":"value","from":"2026-11-01T09:30:00Z","to":"2026-11-01T10:00:00Z","value":null}]}`,
		},
		{
			name: "summary",
			result: devices.StateChangeQueryResult{Seeded: true, Data: devices.StateChangeSummary{
				Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
				Buckets: []devices.StateChangeBucket{
					{Range: window, SecondsTrue: 90.5, TransitionsIntoTrue: 2, KnownSeconds: 3600},
				},
			}},
			want: `{"kind":"summary",` + envelope +
				`"coverage":{"basis":"retained_changes","completeness":"unverified","seeded":true},` +
				`"bucket":{"duration":"1h0m0s","alignment":"range_start"},"buckets":[{"range":` +
				`{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"seconds_true":90.5,` +
				`"transitions_into_true":2,"known_seconds":3600}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, api := testAPI(t, &stubDevices{
				queryStateChanges: func(_ context.Context, q devices.StateChangeQuery) (devices.StateChangeQueryResult, error) {
					result := tc.result
					result.EntityID, result.Range = q.EntityID, window
					return result, nil
				},
			})
			rec := changesPost(router, changesTimelineBody)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			require.JSONEq(t, tc.want, rec.Body.String())
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(rec.Body.String()))
			require.NoError(t, err)
			require.NoError(
				t,
				changesPublishedSchema(t, api, "responses/200/content/application~1json/schema").Validate(value),
			)
		})
	}
}

func TestStateChangesErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{devices.ErrEntityNotFound, http.StatusNotFound, "entity_not_found"},
		{&devices.HistoryQueryError{Code: "invalid_query", Field: "select_field"}, http.StatusUnprocessableEntity, "invalid_query"},
		{&devices.HistoryQueryError{Code: "unsupported_source"}, http.StatusUnprocessableEntity, "unsupported_source"},
		{&devices.HistoryQueryError{Code: "value_type"}, http.StatusUnprocessableEntity, "value_type"},
		{&devices.HistoryQueryError{Code: "query_limit", Field: "range"}, http.StatusUnprocessableEntity, "query_limit"},
		{errors.New("disk on fire"), http.StatusInternalServerError, "internal_error"},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, &stubDevices{
				queryStateChanges: func(context.Context, devices.StateChangeQuery) (devices.StateChangeQueryResult, error) {
					return devices.StateChangeQueryResult{}, tc.err
				},
			})
			rec := changesPost(router, changesTimelineBody)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"code":"`+tc.code+`"`)
			require.NotContains(t, rec.Body.String(), "disk on fire")
		})
	}
}

func TestStateChangesRequiresJSON(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	req := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/state/changes",
		strings.NewReader(changesTimelineBody))
	req.Header.Set("Content-Type", "text/plain")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusUnsupportedMediaType, rec.Code, rec.Body.String())
}
