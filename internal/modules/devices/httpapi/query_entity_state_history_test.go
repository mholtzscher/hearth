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

	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
)

const historyRequestBody = `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"result":{"kind":"observations"}}`

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

func TestHistoryQueryObservationLimitNumbers(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		field  string
		status int
		limit  int
	}{
		{name: "omitted", status: http.StatusOK},
		{name: "integer", field: `,"limit":1`, status: http.StatusOK, limit: 1},
		{name: "decimal integer", field: `,"limit":1.0`, status: http.StatusOK, limit: 1},
		{name: "exponent integer", field: `,"limit":1e2`, status: http.StatusOK, limit: 100},
		{name: "negative exponent integer", field: `,"limit":100e-2`, status: http.StatusOK, limit: 1},
		{name: "maximum", field: `,"limit":2e2`, status: http.StatusOK, limit: 200},
		{name: "fraction", field: `,"limit":1.5`, status: http.StatusBadRequest},
		{name: "fraction beyond float precision", field: `,"limit":1.00000000000000000001`, status: http.StatusBadRequest},
		{name: "fraction above maximum", field: `,"limit":200.00000000000000001`, status: http.StatusBadRequest},
		{name: "zero", field: `,"limit":0`, status: http.StatusBadRequest},
		{name: "out of range", field: `,"limit":201`, status: http.StatusBadRequest},
		{name: "wrong type", field: `,"limit":"sentinel_secret"`, status: http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					called = true
					observations, ok := q.Result.(devices.HistoryObservations)
					require.True(t, ok)
					require.Equal(t, tc.limit, observations.Limit)
					return devices.HistoryQueryResult{
						EntityID: q.EntityID, Range: q.Range,
						Data: devices.HistoryObservationsResult{Order: devices.HistoryAscending},
					}, nil
				},
			})
			body := strings.Replace(historyRequestBody, `"kind":"observations"`, `"kind":"observations"`+tc.field, 1)
			rec := historyPost(router, body)
			require.Equal(t, tc.status, rec.Code, rec.Body.String())
			require.Equal(t, tc.status == http.StatusOK, called)
			if tc.status != http.StatusOK {
				require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
				require.NotContains(t, rec.Body.String(), "sentinel_secret")
			}
		})
	}
}

func TestHistoryQueryDecodesSummaryMetrics(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			require.Equal(t, new("  sample.value  "), q.SelectExpr)
			summary, ok := q.Result.(devices.HistorySummary)
			require.True(t, ok)
			require.Equal(t, map[string]devices.HistoryMetric{
				"low": devices.HistoryMinimum{}, "high": devices.HistoryMaximum{},
				"mean":     devices.HistorySampleMean{},
				"duration": devices.HistoryDurationWhere{WhereExpr: "  value == true  ", Unit: "seconds"},
				"entries":  devices.HistoryTransitionsInto{WhereExpr: " value == false "},
			}, summary.Metrics)
			require.Equal(
				t,
				&devices.HistoryBucket{Duration: 30 * time.Minute, Alignment: "range_start"},
				summary.Bucket,
			)
			return devices.HistoryQueryResult{
				EntityID: q.EntityID, Range: q.Range,
				Data: devices.HistorySummaryResult{Metrics: devices.HistoryMetricValues{}},
			}, nil
		},
	})
	body := `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},` +
		`"select_expr":"  sample.value  ","result":{"kind":"summary","metrics":{` +
		`"low":{"operation":"min"},"high":{"operation":"max"},"mean":{"operation":"sample_mean"},` +
		`"duration":{"operation":"duration_where","where_expr":"  value == true  ","unit":"seconds"},` +
		`"entries":{"operation":"transitions_into","where_expr":" value == false "}},` +
		`"bucket":{"duration":"30m","alignment":"range_start"}}}`
	rec := historyPost(router, body)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestHistoryQueryNullPolicyContract(t *testing.T) {
	t.Parallel()
	const numeric = `"metrics":{"mean":{"operation":"sample_mean"}}`
	const temporal = `"metrics":{"seconds":{"operation":"duration_where","where_expr":"value != nil","unit":"seconds"},"entries":{"operation":"transitions_into","where_expr":"value != nil"}}`
	for _, tc := range []struct {
		name, result string
		policy       devices.HistoryNullPolicy
		valid        bool
	}{
		{"default", `{"kind":"summary",` + numeric + `}`, devices.HistoryNullReject, true},
		{"reject", `{"kind":"summary","nulls":"reject",` + numeric + `}`, devices.HistoryNullReject, true},
		{"gap", `{"kind":"summary","nulls":"gap",` + numeric + `}`, devices.HistoryNullGap, true},
		{"mixed gap", `{"kind":"summary","nulls":"gap","metrics":{"min":{"operation":"min"},"seconds":{"operation":"duration_where","where_expr":"value != nil","unit":"seconds"}}}`, devices.HistoryNullGap, true},
		{"temporal reject", `{"kind":"summary","nulls":"reject",` + temporal + `}`, devices.HistoryNullReject, true},
		{"unknown", `{"kind":"summary","nulls":"sentinel_secret",` + numeric + `}`, "", false},
		{"empty", `{"kind":"summary","nulls":"",` + numeric + `}`, "", false},
		{"null", `{"kind":"summary","nulls":null,` + numeric + `}`, "", false},
		{"wrong type", `{"kind":"summary","nulls":true,` + numeric + `}`, "", false},
		{"temporal gap", `{"kind":"summary","nulls":"gap",` + temporal + `}`, "", false},
		{"observations gap", `{"kind":"observations","nulls":"gap"}`, "", false},
		{"observations reject", `{"kind":"observations","nulls":"reject"}`, "", false},
		{"timeline gap", `{"kind":"timeline","nulls":"gap"}`, "", false},
		{"timeline reject", `{"kind":"timeline","nulls":"reject"}`, "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			router, api := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					called = true
					summary, ok := q.Result.(devices.HistorySummary)
					require.True(t, ok)
					require.Equal(t, tc.policy, summary.Nulls)
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistorySummaryResult{
							Metrics: devices.HistoryMetricValues{"mean": json.RawMessage("null")},
						},
					}, nil
				},
			})
			body := `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"result":` + tc.result + `}`
			rec := historyPost(router, body)
			require.Equal(t, tc.valid, called)
			if tc.valid {
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			} else {
				require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
				require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
				require.NotContains(t, rec.Body.String(), "sentinel_secret")
			}
			// The published schema independently agrees with the production decoder.
			published, err := json.Marshal(api.OpenAPI())
			require.NoError(t, err)
			document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(published)))
			require.NoError(t, err)
			compiler := jsonschema.NewCompiler()
			require.NoError(t, compiler.AddResource("https://hearth.invalid/openapi", document))
			schema, err := compiler.Compile(
				"https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/requestBody/content/application~1json/schema",
			)
			require.NoError(t, err)
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(body))
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
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			called = true
			return devices.HistoryQueryResult{}, nil
		},
	})
	body := `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},` +
		`"result":{"kind":"summary","metrics":{"a":{"operation":"min"}},` +
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
	const prefix = `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"result":`
	for _, tc := range []struct{ name, body string }{
		{"root casing", strings.Replace(historyRequestBody, `"range"`, `"Range"`, 1)},
		{"range casing", strings.Replace(historyRequestBody, `"from"`, `"From"`, 1)},
		{"result casing", prefix + `{"Kind":"timeline"}}`},
		{"irrelevant result field", prefix + `{"kind":"timeline","cursor":"sentinel_secret"}}`},
		{"null bucket", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min"}},"bucket":null}}`},
		{"bucket casing", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min"}},"bucket":{"Duration":"1h","alignment":"range_start"}}}`},
		{"metric null", prefix + `{"kind":"summary","metrics":{"a":null}}}`},
		{"metric casing", prefix + `{"kind":"summary","metrics":{"a":{"Operation":"min"}}}}`},
		{"irrelevant metric field", prefix + `{"kind":"summary","metrics":{"a":{"operation":"min","unit":"sentinel_secret"}}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(
				t,
				&stubDevices{
					queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
						called = true
						return devices.HistoryQueryResult{}, nil
					},
				},
			)
			rec := historyPost(router, tc.body)
			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			require.Contains(t, rec.Body.String(), `"code":"invalid_query"`)
			require.NotContains(t, rec.Body.String(), "sentinel_secret")
			require.False(t, called)
		})
	}
}

//nolint:modernize // Named embedded field clarifies the coverage fixture.
func TestHistoryQuerySummaryWireVariants(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	window := devices.HistoryRange{From: start, To: start.Add(time.Hour)}
	cov := devices.HistoryTemporalCoverage{
		HistoryDurationCoverage: devices.HistoryDurationCoverage{InferredKnownSeconds: 3600},
	}
	for _, tc := range []struct {
		name string
		data devices.HistoryResult
		want string
	}{
		{
			name: "temporal summary",
			data: devices.HistorySummaryResult{
				Coverage: &cov, Metrics: devices.HistoryMetricValues{"count": json.RawMessage(`9007199254740993`)},
			},
			want: `{"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
				`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
				`"coverage":{"basis":"retained_observations","completeness":"unverified","seed_available":false,` +
				`"inferred_known_seconds":3600,"unknown_seconds":0},"metrics":{"count":9007199254740993}}`,
		},
		{
			name: "numeric buckets",
			data: devices.HistoryBucketedSummaryResult{
				Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
				Buckets: []devices.HistorySummaryBucket{{
					Range: window, Metrics: devices.HistoryMetricValues{"min": json.RawMessage(`null`)},
				}},
			},
			want: `{"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
				`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
				`"coverage":{"basis":"retained_observations","completeness":"unverified"},` +
				`"bucket":{"duration":"1h0m0s","alignment":"range_start"},` +
				`"buckets":[{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},` +
				`"metrics":{"min":null}}]}`,
		},
		{
			name: "empty buckets",
			data: devices.HistoryBucketedSummaryResult{
				Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
			},
			want: `{"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
				`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
				`"coverage":{"basis":"retained_observations","completeness":"unverified"},` +
				`"bucket":{"duration":"1h0m0s","alignment":"range_start"},"buckets":[]}`,
		},
		{
			name: "temporal buckets",
			data: devices.HistoryBucketedSummaryResult{
				Coverage: &cov,
				Bucket:   devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"},
				Buckets: []devices.HistorySummaryBucket{{
					Range: window, Metrics: devices.HistoryMetricValues{"seconds": json.RawMessage(`3600`)},
					Coverage: &cov.HistoryDurationCoverage,
				}},
			},
			want: `{"kind":"summary","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab",` +
				`"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at",` +
				`"coverage":{"basis":"retained_observations","completeness":"unverified","seed_available":false,` +
				`"inferred_known_seconds":3600,"unknown_seconds":0},` +
				`"bucket":{"duration":"1h0m0s","alignment":"range_start"},` +
				`"buckets":[{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},` +
				`"metrics":{"seconds":3600},"coverage":{"inferred_known_seconds":3600,"unknown_seconds":0}}]}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, api := testAPI(
				t,
				&stubDevices{
					queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
						return devices.HistoryQueryResult{EntityID: q.EntityID, Range: window, Data: tc.data}, nil
					},
				},
			)
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
			published, err := json.Marshal(api.OpenAPI())
			require.NoError(t, err)
			doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(published)))
			require.NoError(t, err)
			compiler := jsonschema.NewCompiler()
			require.NoError(t, compiler.AddResource("https://hearth.invalid/openapi", doc))
			schema, err := compiler.Compile(
				"https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/responses/200/content/application~1json/schema",
			)
			require.NoError(t, err)
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(rec.Body.String()))
			require.NoError(t, err)
			require.NoError(t, schema.Validate(value))
		})
	}
}

func TestHistoryQueryRejectsInconsistentBucketCoverage(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name     string
		coverage *devices.HistoryTemporalCoverage
		bucket   *devices.HistoryDurationCoverage
	}{
		{name: "numeric summary with temporal bucket", bucket: &devices.HistoryDurationCoverage{}},
		{name: "temporal summary with numeric bucket", coverage: &devices.HistoryTemporalCoverage{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{EntityID: q.EntityID, Range: q.Range,
						Data: devices.HistoryBucketedSummaryResult{
							Coverage: tc.coverage, Buckets: []devices.HistorySummaryBucket{{Coverage: tc.bucket}},
						}}, nil
				},
			})
			rec := historyPost(router, historyRequestBody)
			require.Equal(t, http.StatusInternalServerError, rec.Code)
			require.Contains(t, rec.Body.String(), `"code":"internal_error"`)
			require.NotContains(t, rec.Body.String(), "coverage")
			require.NotContains(t, rec.Body.String(), "buckets")
		})
	}
}
func TestHistoryQueryNormalizesExplicitOffsetsToUTC(t *testing.T) {
	t.Parallel()
	wantFrom := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	wantTo := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	router, _ := testAPI(t, &stubDevices{
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			require.Equal(t, wantFrom, q.Range.From)
			require.Equal(t, wantTo, q.Range.To)
			require.Equal(t, time.UTC, q.Range.From.Location())
			require.Equal(t, time.UTC, q.Range.To.Location())
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data:     devices.HistoryTimelineResult{},
			}, nil
		},
	})
	rec := historyPost(
		router,
		`{"range":{"from":"2026-11-01T10:00:00+01:00","to":"2026-11-01T05:00:00-05:00"},"result":{"kind":"timeline"}}`,
	)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}

func TestHistoryQueryRejectsInvalidBodiesWithoutDisclosure(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(t, &stubDevices{})
	for _, body := range []string{`{"sentinel_secret":`, `{"entity_id":"sentinel_secret","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"result":{"kind":"timeline"}}`, strings.Replace(historyRequestBody, `"observations"`, `"timeline","limit":1`, 1), strings.Replace(historyRequestBody, `"result"`, `"select_expr":null,"result"`, 1), strings.Replace(historyRequestBody, `"observations"`, `"observations","order":null`, 1)} {
		rec := historyPost(router, body)
		if rec.Code != 400 || strings.Contains(rec.Body.String(), "sentinel_secret") {
			t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
		}
	}
}

//nolint:modernize // Named embedded field clarifies the coverage fixture.
func TestHistoryQueryResponsesAndSchemas(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	end := start.Add(time.Hour)
	window := devices.HistoryRange{From: start, To: end}
	cov := devices.HistoryTemporalCoverage{
		HistoryDurationCoverage: devices.HistoryDurationCoverage{InferredKnownSeconds: 3600},
	}
	for _, data := range []devices.HistoryResult{devices.HistoryObservationsResult{Order: devices.HistoryAscending, Items: []devices.HistoryObservation{{ObservationID: apiObservationID, ObservedAt: start, AdapterReceivedAt: start, Disposition: devices.DispositionApplied, Value: json.RawMessage(`9007199254740993`)}}}, devices.HistoryObservationsResult{Order: devices.HistoryAscending}, devices.HistoryTimelineResult{Coverage: cov, Intervals: []devices.HistoryInterval{devices.HistoryUnknownInterval{Range: window}, devices.HistoryValueInterval{Range: window, Value: json.RawMessage(`null`)}}}, devices.HistorySummaryResult{Metrics: devices.HistoryMetricValues{"min": json.RawMessage(`9007199254740993`), "max": json.RawMessage(`null`)}}, devices.HistoryBucketedSummaryResult{Coverage: &cov, Bucket: devices.HistoryBucket{Duration: time.Hour, Alignment: "range_start"}, Buckets: []devices.HistorySummaryBucket{{Range: window, Metrics: devices.HistoryMetricValues{"x": json.RawMessage(`0`)}, Coverage: &cov.HistoryDurationCoverage}}}} {
		router, api := testAPI(
			t,
			&stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					if q.EntityID != apiEntityID {
						t.Fatal(q.EntityID)
					}
					return devices.HistoryQueryResult{EntityID: q.EntityID, Range: window, Data: data}, nil
				},
			},
		)
		rec := historyPost(router, historyRequestBody)
		if rec.Code != 200 {
			t.Fatalf("%T status=%d body=%s", data, rec.Code, rec.Body.String())
		}
		if strings.Contains(rec.Body.String(), `"next_cursor"`) {
			t.Fatal(rec.Body.String())
		}
		raw, _ := json.Marshal(api.OpenAPI())
		doc, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
		if err != nil {
			t.Fatal(err)
		}
		compiler := jsonschema.NewCompiler()
		if err = compiler.AddResource("https://hearth.invalid/openapi", doc); err != nil {
			t.Fatal(err)
		}
		schema, err := compiler.Compile(
			"https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/responses/200/content/application~1json/schema",
		)
		if err != nil {
			t.Fatal(err)
		}
		value, err := jsonschema.UnmarshalJSON(strings.NewReader(rec.Body.String()))
		if err != nil {
			t.Fatal(err)
		}
		if err = schema.Validate(value); err != nil {
			t.Fatalf("%T response=%s schema error=%v", data, rec.Body.String(), err)
		}
	}
}
func TestHistoryQueryErrorsAreSafe(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{{fmt.Errorf("secret: %w", &devices.HistoryQueryError{Code: "expression_type", Field: "result.metrics.open.where_expr"}), 422, "expression_type"}, {devices.ErrEntityNotFound, 404, "entity_not_found"}, {errors.New("sentinel_secret"), 500, "internal_error"}} {
		router, _ := testAPI(
			t,
			&stubDevices{queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
				return devices.HistoryQueryResult{}, tc.err
			}},
		)
		rec := historyPost(router, historyRequestBody)
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), tc.code) ||
			strings.Contains(rec.Body.String(), "secret") {
			t.Fatalf("%d %s", rec.Code, rec.Body.String())
		}
	}
}

func TestHistoryQuerySummaryInputAndResultLimit(t *testing.T) {
	t.Parallel()
	body := `{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"select_expr":"sample.value","result":{"kind":"summary","metrics":{"open":{"operation":"duration_where","where_expr":"value == true","unit":"seconds"},"min":{"operation":"min"}},"bucket":{"duration":"1h","alignment":"range_start"}}}`
	router, _ := testAPI(
		t,
		&stubDevices{queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			summary, ok := q.Result.(devices.HistorySummary)
			if !ok || summary.Bucket == nil || summary.Bucket.Duration != time.Hour || q.SelectExpr == nil ||
				*q.SelectExpr != "sample.value" {
				t.Fatalf("query: %#v", q)
			}
			if _, valid := summary.Metrics["open"].(devices.HistoryDurationWhere); !valid {
				t.Fatalf("metrics: %#v", summary.Metrics)
			}
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistoryObservationsResult{
					Order: devices.HistoryAscending,
					Items: []devices.HistoryObservation{
						{Value: json.RawMessage(`"` + strings.Repeat("x", devices.HistoryMaxResultBytes) + `"`)},
					},
				},
			}, nil
		}},
	)
	rec := historyPost(router, body)
	if rec.Code != 422 || !strings.Contains(rec.Body.String(), "query_limit") ||
		strings.Contains(rec.Body.String(), strings.Repeat("x", 100)) {
		t.Fatalf("status=%d body=%s", rec.Code, rec.Body.String())
	}
}

func TestHistoryQueryPublishedSchemaRejectsContradictoryResults(t *testing.T) {
	t.Parallel()
	_, api := testAPI(t, &stubDevices{})
	raw, err := json.Marshal(api.OpenAPI())
	if err != nil {
		t.Fatal(err)
	}
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	if err = compiler.AddResource("https://hearth.invalid/openapi", document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(
		"https://hearth.invalid/openapi#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/responses/200/content/application~1json/schema",
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{
		`{"kind":"observations","entity_id":"x","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at","coverage":{"basis":"retained_observations","completeness":"unverified"},"order":"ascending"}`,
		`{"kind":"timeline","entity_id":"x","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at","coverage":{"basis":"retained_observations","completeness":"unverified","seed_available":false,"inferred_known_seconds":0,"unknown_seconds":3600},"intervals":[{"kind":"unknown","from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z","reason":"no_prior_observation","value":null}]}`,
		`{"kind":"summary","entity_id":"x","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at","coverage":{"basis":"retained_observations","completeness":"unverified"},"metrics":{},"bucket":{"duration":"1h","alignment":"range_start"},"buckets":[]}`,
		`{"kind":"summary","entity_id":"x","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at","coverage":{"basis":"retained_observations","completeness":"unverified","seed_available":false,"inferred_known_seconds":0,"unknown_seconds":3600},"bucket":{"duration":"1h","alignment":"range_start"},"buckets":[{"range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"metrics":{}}]}`,
	} {
		value, decodeErr := jsonschema.UnmarshalJSON(strings.NewReader(body))
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if err = schema.Validate(value); err == nil {
			t.Fatalf("schema accepted %s", body)
		}
	}
}

func TestHistoryQueryCallerCancellation(t *testing.T) {
	t.Parallel()
	router, _ := testAPI(
		t,
		&stubDevices{
			queryHistory: func(ctx context.Context, _ devices.HistoryQuery) (devices.HistoryQueryResult, error) {
				return devices.HistoryQueryResult{}, fmt.Errorf("private: %w", ctx.Err())
			},
		},
	)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := httptest.NewRequest(http.MethodPost, "/v1/entities/"+string(apiEntityID)+"/state/history", strings.NewReader(historyRequestBody)).
		WithContext(ctx)
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, request)
	if rec.Code != 503 || !strings.Contains(rec.Body.String(), "query_canceled") ||
		strings.Contains(rec.Body.String(), "private") {
		t.Fatalf("%d %s", rec.Code, rec.Body.String())
	}
}

func TestHistoryQueryPreservesValuesAndContinuation(t *testing.T) {
	t.Parallel()
	for _, value := range []string{`false`, `0`, `null`, `9007199254740993`, `{"n":9007199254740993,"missing":null}`} {
		t.Run(value, func(t *testing.T) {
			t.Parallel()
			at := time.Date(2026, 11, 1, 9, 0, 0, 123, time.UTC)
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					require.Equal(t, devices.HistoryObservations{
						Order: devices.HistoryDescending, Limit: 2, Cursor: "opaque", WhereExpr: new("value != null"),
					}, q.Result)
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistoryObservationsResult{
							Order: devices.HistoryDescending, NextCursor: "next-page",
							Items: []devices.HistoryObservation{{ObservationID: apiObservationID, ObservedAt: at,
								AdapterReceivedAt: at, SourceUpdatedAt: &at, Disposition: devices.DispositionUnchanged,
								Value: json.RawMessage(value)}},
						},
					}, nil
				},
			})
			body := strings.Replace(
				historyRequestBody,
				`"observations"`,
				`"observations","order":"descending","limit":2,"cursor":"opaque","where_expr":"value != null"`,
				1,
			)
			rec := historyPost(router, body)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			var got struct {
				EntityID   string `json:"entity_id"`
				NextCursor string `json:"next_cursor"`
				Items      []struct {
					Value       json.RawMessage `json:"value"`
					Source      string          `json:"source_updated_at"`
					Disposition string          `json:"disposition"`
				} `json:"items"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			require.Equal(t, string(apiEntityID), got.EntityID)
			require.Equal(t, "next-page", got.NextCursor)
			require.Len(t, got.Items, 1)
			require.Equal(t, value, string(got.Items[0].Value))
			require.Equal(t, "2026-11-01T09:00:00.000000123Z", got.Items[0].Source)
			require.Equal(t, "unchanged", got.Items[0].Disposition)
		})
	}
}

func TestHistoryQueryExactEncodedResultLimit(t *testing.T) {
	t.Parallel()
	// The independently specified wire envelope fixes overhead without invoking
	// the production mapper. JSON member ordering does not affect its byte size.
	const emptyResponse = `{"kind":"observations","entity_id":"ent_01890f47-7a6b-7c4d-8e9f-0123456789ab","range":{"from":"2026-11-01T09:00:00Z","to":"2026-11-01T10:00:00Z"},"time_axis":"observed_at","coverage":{"basis":"retained_observations","completeness":"unverified"},"order":"ascending","items":[{"observation_id":"obs_01890f47-7a6b-7c4d-8e9f-0123456789ab","observed_at":"2026-11-01T09:00:00Z","adapter_received_at":"2026-11-01T09:00:00Z","source_updated_at":null,"disposition":"applied","value":""}]}`
	for _, delta := range []int{-1, 0, 1} {
		t.Run(strconv.Itoa(delta), func(t *testing.T) {
			t.Parallel()
			value := `"` + strings.Repeat("x", devices.HistoryMaxResultBytes-len(emptyResponse)+delta) + `"`
			router, _ := testAPI(t, &stubDevices{
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistoryObservationsResult{
							Order: devices.HistoryAscending, Items: []devices.HistoryObservation{
								{
									ObservationID:     apiObservationID,
									ObservedAt:        q.Range.From,
									AdapterReceivedAt: q.Range.From,
									Disposition:       devices.DispositionApplied,
									Value:             json.RawMessage(value),
								},
							},
						},
					}, nil
				},
			})
			rec := historyPost(router, historyRequestBody)
			if delta > 0 {
				require.Equal(t, http.StatusUnprocessableEntity, rec.Code)
				require.Contains(t, rec.Body.String(), `"code":"query_limit"`)
				require.NotContains(t, rec.Body.String(), `"items"`)
				return
			}
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String()[:100])
			require.Equal(t, devices.HistoryMaxResultBytes+delta, rec.Body.Len())
			require.Equal(t, "application/json", rec.Header().Get("Content-Type"))
			require.NotContains(t, rec.Body.String(), `"$schema"`)
		})
	}
}

func TestHistoryQueryWrappedErrorClassification(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		code   string
		status int
	}{
		{"invalid_cursor", 400}, {"invalid_query", 422}, {"unsupported_source", 422},
		{"query_limit", 422}, {"expression_type", 422}, {"expression_limit", 422},
		{"expression_value_invalid", 422}, {"expression_evaluation", 422},
		{"unknown_private_failure", 500},
	} {
		t.Run(tc.code, func(t *testing.T) {
			t.Parallel()
			router, _ := testAPI(
				t,
				&stubDevices{
					queryHistory: func(context.Context, devices.HistoryQuery) (devices.HistoryQueryResult, error) {
						return devices.HistoryQueryResult{}, fmt.Errorf(
							"private-expression-state: %w",
							&devices.HistoryQueryError{Code: tc.code, Field: "result.where_expr"},
						)
					},
				},
			)
			rec := historyPost(router, historyRequestBody)
			require.Equal(t, tc.status, rec.Code)
			var got historyProblemError
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &got))
			if tc.status == 500 {
				require.Equal(t, "internal_error", got.Code)
				require.Empty(t, got.Errors)
			} else {
				require.Equal(t, tc.code, got.Code)
				require.Len(t, got.Errors, 1)
				require.Equal(t, "body.result.where_expr", got.Errors[0].Location)
				require.Nil(t, got.Errors[0].Value)
			}
			require.NotContains(t, rec.Body.String(), "private")
			require.NotContains(t, rec.Body.String(), `"items"`)
		})
	}
}

func TestHistoryQueryMediaTypeAndRequestSize(t *testing.T) {
	t.Parallel()
	for _, mediaType := range []string{"text/plain", "application/octet-stream", "", "application/json; charset=utf-8"} {
		t.Run(mediaType, func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(
				t,
				&stubDevices{
					queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
						called = true
						return devices.HistoryQueryResult{
							EntityID: q.EntityID,
							Range:    q.Range,
							Data:     devices.HistoryObservationsResult{Order: devices.HistoryAscending},
						}, nil
					},
				},
			)
			request := httptest.NewRequest(
				http.MethodPost,
				"/v1/entities/"+string(apiEntityID)+"/state/history",
				strings.NewReader(historyRequestBody),
			)
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
	for _, invalid := range []string{
		strings.Replace(historyRequestBody, `"result"`, `"select_expr":"","result"`, 1),
		strings.Replace(historyRequestBody, `"range"`, `"range":{},"range"`, 1),
	} {
		rec = historyPost(router, invalid)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	}
}

func TestHistoryQueryRequestSizeBoundary(t *testing.T) {
	t.Parallel()
	for _, size := range []int{1024*1024 - 1, 1024 * 1024, 1024*1024 + 1} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			t.Parallel()
			called := false
			router, _ := testAPI(
				t,
				&stubDevices{
					queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
						called = true
						return devices.HistoryQueryResult{
							EntityID: q.EntityID,
							Range:    q.Range,
							Data:     devices.HistoryObservationsResult{Order: devices.HistoryAscending},
						}, nil
					},
				},
			)
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
