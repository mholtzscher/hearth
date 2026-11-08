package httpapi //nolint:testpackage // Exercise the registered transport boundary.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const (
	historyRangeJSON   = `"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"}`
	historyRequestBody = `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary",` +
		`"metrics":{"min":{"operation":"min"}}}}`
	historyOpenAPIPath = "https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/"
)

func historyPost(router http.Handler, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/entities/"+string(apiEntityID)+"/state/history",
		strings.NewReader(body),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

// historyPublishedSchema compiles one schema from the operation's published
// OpenAPI document, so tests check that runtime and publication agree.
func historyPublishedSchema(t *testing.T, api huma.API, pointer string) *jsonschema.Schema {
	t.Helper()
	published, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(published)))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://hearth.invalid/openapi", document))
	schema, err := compiler.Compile(historyOpenAPIPath + pointer)
	require.NoError(t, err)
	return schema
}

func historySummaryStub(called *bool) *stubDevices {
	return &stubDevices{
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			*called = true
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistorySummaryResult{
					Metrics: devices.HistoryMetricValues{"min": json.RawMessage("null")},
				},
			}, nil
		},
	}
}

func TestHistoryQueryDecodesSummaryRequest(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			require.Equal(t, devices.HistoryQuery{
				EntityID: apiEntityID,
				Range: devices.HistoryRange{
					From: time.Date(
						2026,
						11,
						1,
						9,
						0,
						0,
						0,
						time.UTC,
					),
					To: time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC),
				},
				Field: "/value",
				Metrics: map[string]devices.HistoryMetric{
					"low":  devices.HistoryMinimum{},
					"high": devices.HistoryMaximum{},
					"mean": devices.HistorySampleMean{},
				},
				Bucket: &devices.HistoryBucket{Duration: 30 * time.Minute, Alignment: "range_start"},
				Nulls:  devices.HistoryNullGap,
			}, q)
			return devices.HistoryQueryResult{
				EntityID: q.EntityID, Range: q.Range,
				Data: devices.HistorySummaryResult{Metrics: devices.HistoryMetricValues{}},
			}, nil
		},
	})
	body := `{` + historyRangeJSON + `,"select_field":"/value","result":{"kind":"summary","nulls":"gap","metrics":{` +
		`"low":{"operation":"min"},"high":{"operation":"max"},"mean":{"operation":"sample_mean"}},` +
		`"bucket":{"duration":"30m","alignment":"range_start"}}}`
	rec := historyPost(router, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

// Each request is checked by the production decoder and the published schema.
func TestHistoryQueryRequestContract(t *testing.T) {
	t.Parallel()
	const metrics = `"metrics":{"mean":{"operation":"sample_mean"}}`
	result := func(fields string) string {
		return `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary",` + fields + `}}`
	}
	for _, tc := range []struct {
		name, body string
		policy     devices.HistoryNullPolicy
		valid      bool
	}{
		{"default nulls", result(metrics), devices.HistoryNullReject, true},
		{"reject", result(`"nulls":"reject",` + metrics), devices.HistoryNullReject, true},
		{"gap", result(`"nulls":"gap",` + metrics), devices.HistoryNullGap, true},
		{"unknown nulls", result(`"nulls":"sentinel_secret",` + metrics), "", false},
		{"empty nulls", result(`"nulls":"",` + metrics), "", false},
		{"null nulls", result(`"nulls":null,` + metrics), "", false},
		{"no metrics", result(`"metrics":{}`), "", false},
		{"missing select_field", `{` + historyRangeJSON + `,"result":{"kind":"summary",` + metrics + `}}`, "", false},
		{"malformed select_field", strings.Replace(result(metrics), `"select_field":""`, `"select_field":"value"`, 1), "", false},
		// The removed expression query's request shapes.
		{"select_expr", strings.Replace(result(metrics), `"select_field":""`, `"select_expr":"sample.value"`, 1), "", false},
		{"select_expr beside select_field", strings.Replace(result(metrics), `"select_field":""`,
			`"select_field":"","select_expr":"sample.value"`, 1), "", false},
		{"observations", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"observations"}}`, "", false},
		{"timeline", `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"timeline"}}`, "", false},
		{"duration_where", result(`"metrics":{"s":{"operation":"duration_where","where_expr":"value","unit":"seconds"}}`),
			"", false},
		{"transitions_into", result(`"metrics":{"n":{"operation":"transitions_into","where_expr":"value"}}`), "", false},
		{"cursor", result(`"cursor":"sentinel_secret",` + metrics), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			router, api := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					called = true
					require.Equal(t, tc.policy, q.Nulls)
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistorySummaryResult{
							Metrics: devices.HistoryMetricValues{"mean": json.RawMessage("null")},
						},
					}, nil
				},
			})
			rec := historyPost(router, tc.body)
			require.Equal(t, tc.valid, called)
			if tc.valid {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			} else {
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
				require.NotContains(t, rec.Body.String(), "sentinel_secret")
			}
			schema := historyPublishedSchema(t, api, "requestBody/content/application~1json/schema")
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(tc.body))
			require.NoError(t, err)
			if tc.valid {
				require.NoError(t, schema.Validate(value))
			} else {
				require.Error(t, schema.Validate(value))
			}
		})
	}
}

func TestHistoryQueryInvalidBucketDurationIsSafe(t *testing.T) {
	t.Parallel()
	called := false
	router, _ := testAPI(t, historySummaryStub(&called))
	body := `{` + historyRangeJSON + `,"select_field":"","result":{"kind":"summary","metrics":{"a":{"operation":"min"}},` +
		`"bucket":{"duration":"sentinel_secret","alignment":"range_start"}}}`
	rec := historyPost(router, body)
	require.Equal(t, http.StatusBadRequest, rec.Code)
	var problem historyProblemError
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &problem))
	require.Equal(t, "invalid_query", problem.Code)
	require.Len(t, problem.Errors, 1)
	require.Equal(t, "body.result.bucket.duration", problem.Errors[0].Location)
	require.NotContains(t, rec.Body.String(), "sentinel_secret")
	require.False(t, called)
}

func TestHistoryQueryStrictNestedShapes(t *testing.T) {
	t.Parallel()
	const prefix = `{` + historyRangeJSON + `,"select_field":"","result":`
	for _, tc := range []struct{ name, body string }{
		{"root casing", strings.Replace(historyRequestBody, `"range"`, `"Range"`, 1)},
		{"range casing", strings.Replace(historyRequestBody, `"from"`, `"From"`, 1)},
		{"select_field casing", strings.Replace(historyRequestBody, `"select_field"`, `"Select_Field"`, 1)},
		{"null select_field", strings.Replace(historyRequestBody, `"select_field":""`, `"select_field":null`, 1)},
		{"result casing", prefix + `{"Kind":"summary","metrics":{"a":{"operation":"min"}}}}`},
		{"null bucket", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min"}},"bucket":null}}`},
		{"bucket casing", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min"}},` +
			`"bucket":{"Duration":"1h","alignment":"range_start"}}}`},
		{"metric null", prefix + `{"kind":"summary","metrics":{"a":null}}}`},
		{"metric casing", prefix + `{"kind":"summary","metrics":{"a":{"Operation":"min"}}}}`},
		{"irrelevant metric field", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min","unit":"sentinel_secret"}}}}`},
		{"unknown root field", strings.Replace(historyRequestBody, `"select_field"`, `"entity_id":"sentinel_secret","select_field"`, 1)},
		{"truncated", `{"sentinel_secret":`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(t, historySummaryStub(&called))
			rec := historyPost(router, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
			require.NotContains(t, rec.Body.String(), "sentinel_secret")
			require.False(t, called)
		})
	}
}

func TestHistoryQueryNormalizesExplicitOffsetsToUTC(t *testing.T) {
	t.Parallel()
	wantFrom := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	called := false
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			called = true
			require.Equal(t, devices.HistoryRange{From: wantFrom, To: wantTo}, q.Range)
			require.Equal(t, time.UTC, q.Range.From.Location())
			require.Equal(t, time.UTC, q.Range.To.Location())
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistorySummaryResult{
					Metrics: devices.HistoryMetricValues{"min": json.RawMessage("null")},
				},
			}, nil
		},
	})
	rec := historyPost(router, strings.Replace(historyRequestBody, historyRangeJSON,
		`"range":{"from":"2026-11-01T10:00:00+01:00","to":"2026-11-01T05:00:00-05:00"}`, 1))
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.True(t, called)
}

func TestHistoryQuerySummaryWireVariants(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	window := devices.HistoryRange{From: start, To: start.Add(time.Hour)}
	const envelope = `"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
		`"coverage":{"basis":"retained_observations","completeness":"unverified"}`
	for _, tc := range []struct {
		name string
		data devices.HistoryResult
		want string
	}{
		{
			name: "unbucketed",
			data: devices.HistorySummaryResult{Metrics: devices.HistoryMetricValues{
				"min": json.RawMessage(`9007199254740993`), "mean": json.RawMessage(`null`),
			}},
			want: `{` + envelope + `,"metrics":{"min":9007199254740993,"mean":null}}`,
		},
		{
			name: "buckets",
			data: devices.HistoryBucketedSummaryResult{
				Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
				Buckets: []devices.HistorySummaryBucket{{
					Range: window, Metrics: devices.HistoryMetricValues{"min": json.RawMessage(`null`)},
				}},
			},
			want: `{` + envelope + `,"bucket":{"duration":"1h0m0s","alignment":"range_start"},` +
				`"buckets":[{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"metrics":{"min":null}}]}`,
		},
		{
			name: "empty buckets",
			data: devices.HistoryBucketedSummaryResult{
				Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
			},
			want: `{` + envelope + `,"bucket":{"duration":"1h0m0s","alignment":"range_start"},"buckets":[]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, api := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{EntityID: q.EntityID, Range: window, Data: tc.data}, nil
				},
			})
			rec := historyPost(router, historyRequestBody)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			// Decode with UseNumber so the assertion also pins exact large numeric values.
			var got, want any
			for _, pair := range []struct {
				text string
				dst  *any
			}{{rec.Body.String(), &got}, {tc.want, &want}} {
				decoder := json.NewDecoder(strings.NewReader(pair.text))
				decoder.UseNumber()
				require.NoError(t, decoder.Decode(pair.dst))
			}
			require.Equal(t, want, got)
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(rec.Body.String()))
			require.NoError(t, err)
			require.NoError(
				t,
				historyPublishedSchema(t, api, "responses/200/content/application~1json/schema").Validate(value),
			)
		})
	}
}

func TestHistoryQueryPublishedSchemaRejectsContradictoryResults(t *testing.T) {
	t.Parallel()
	_, api := testAPI(t, &stubDevices{})
	schema := historyPublishedSchema(t, api, "responses/200/content/application~1json/schema")
	const envelope = `"kind":"summary","entity_id":"x",` +
		`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at"`
	for _, body := range []string{
		`{` + envelope + `,"coverage":{"basis":"retained_observations","completeness":"unverified"},` +
			`"metrics":{},"bucket":{"duration":"1h","alignment":"range_start"},"buckets":[]}`,
		`{"kind":"timeline",` + envelope[len(`"kind":"summary",`):] +
			`,"coverage":{"basis":"retained_observations","completeness":"unverified"},"intervals":[]}`,
		`{` + envelope + `,"coverage":{"basis":"retained_observations","completeness":"unverified",` +
			`"seed_available":false,"inferred_known_seconds":0,"unknown_seconds":3600},"metrics":{}}`,
	} {
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
		require.NoError(t, err)
		require.Error(t, schema.Validate(value), "schema accepted %s", body)
	}
}

func TestHistoryQueryErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err    error
		status int
		code   string
		field  string
	}{
		{&devices.HistoryQueryError{Code: "invalid_query", Field: "range"}, 422, "invalid_query", "range"},
		{&devices.HistoryQueryError{Code: "invalid_query", Field: "select_field"}, 422, "invalid_query", "select_field"},
		{&devices.HistoryQueryError{Code: "unsupported_source"}, 422, "unsupported_source", ""},
		{&devices.HistoryQueryError{Code: "query_limit", Field: "result.bucket"}, 422, "query_limit", "result.bucket"},
		{&devices.HistoryQueryError{Code: "value_type", Field: "result.metrics.mean"}, 422, "value_type", "result.metrics.mean"},
		{&devices.HistoryQueryError{Code: "expression_type", Field: "result.metrics.mean"}, 500, "internal_error", ""},
		{&devices.HistoryQueryError{Code: "unknown_private_failure"}, 500, "internal_error", ""},
		{devices.ErrEntityNotFound, 404, "entity_not_found", ""},
		{errors.New("sentinel_secret"), 500, "internal_error", ""},
	} {
		t.Run(tc.code+"/"+tc.field, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{}, fmt.Errorf("private-state: %w", tc.err)
				},
			})
			rec := historyPost(router, historyRequestBody)
			require.Equal(t, tc.status, rec.Code)
			var got historyProblemError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, tc.code, got.Code)
			if tc.field == "" {
				require.Empty(t, got.Errors)
			} else {
				require.Len(t, got.Errors, 1)
				require.Equal(t, "body."+tc.field, got.Errors[0].Location)
				require.Nil(t, got.Errors[0].Value)
			}
			require.NotContains(t, rec.Body.String(), "private")
			require.NotContains(t, rec.Body.String(), "sentinel_secret")
		})
	}
}

func TestHistoryQueryCallerCancellation(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(ctx context.Context, _ devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			return devices.HistoryQueryResult{}, fmt.Errorf("private: %w", ctx.Err())
		},
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/state/history",
		strings.NewReader(historyRequestBody)).WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	require.Equal(t, http.StatusServiceUnavailable, rec.Code)
	require.Contains(t, rec.Body.String(), "query_canceled")
	require.NotContains(t, rec.Body.String(), "private")
}

func TestHistoryQueryExactEncodedResultLimit(t *testing.T) {
	t.Parallel()
	// The independently specified wire envelope fixes overhead without invoking
	// the production mapper. JSON member ordering does not affect its byte size.
	const emptyResponse = `{"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
		`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
		`"coverage":{"basis":"retained_observations","completeness":"unverified"},"metrics":{"":1}}`
	for _, delta := range []int{-1, 0, 1} {
		t.Run(strconv.Itoa(delta), func(t *testing.T) {
			t.Parallel()
			name := strings.Repeat("x", devices.HistoryMaxResultBytes-len(emptyResponse)+delta)
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistorySummaryResult{
							Metrics: devices.HistoryMetricValues{name: json.RawMessage("1")},
						},
					}, nil
				},
			})
			rec := historyPost(router, historyRequestBody)
			if delta > 0 {
				require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
				require.Contains(t, rec.Body.String(), `"code":"query_limit"`)
				require.NotContains(t, rec.Body.String(), `"metrics"`)
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String()[:100])
			require.Equal(t, devices.HistoryMaxResultBytes+delta, rec.Body.Len())
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			require.NotContains(t, rec.Body.String(), `"$schema"`)
		})
	}
}

func TestHistoryQueryMediaTypeAndRequestSize(t *testing.T) {
	t.Parallel()
	for _, mediaType := range []string{"text/plain", "application/octet-stream", "", "application/json; charset=utf-8"} {
		t.Run(mediaType, func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(t, historySummaryStub(&called))
			request := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/state/history",
				strings.NewReader(historyRequestBody))
			request.Header.Set("Content-Type", mediaType)
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, request)
			if mediaType == "application/json; charset=utf-8" {
				require.Equal(t, http.StatusOK, rec.Code)
				require.True(t, called)
			} else {
				require.Equal(t, http.StatusUnsupportedMediaType, rec.Code)
				require.False(t, called)
			}
		})
	}
	router, _ := testAPI(t, &stubDevices{})
	rec := historyPost(router, strings.Repeat(" ", 1024*1024)+historyRequestBody)
	require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
	require.NotContains(t, rec.Body.String(), historyRequestBody)
	rec = historyPost(router, strings.Replace(historyRequestBody, `"range"`, `"range":{},"range"`, 1))
	require.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestHistoryQueryRequestSizeBoundary(t *testing.T) {
	t.Parallel()
	for _, size := range []int{1024*1024 - 1, 1024 * 1024, 1024*1024 + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(t, historySummaryStub(&called))
			rec := historyPost(router, historyRequestBody+strings.Repeat(" ", size-len(historyRequestBody)))
			if size <= 1024*1024 {
				require.Equal(t, http.StatusOK, rec.Code)
				require.True(t, called)
			} else {
				require.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
				require.False(t, called)
			}
		})
	}
}
