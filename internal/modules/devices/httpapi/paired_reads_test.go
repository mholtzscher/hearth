package httpapi //nolint:testpackage // Exercise registered HTTP operations.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humaecho"
	"github.com/labstack/echo/v5"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/stretchr/testify/require"

	"github.com/mholtzscher/hearth/internal/modules/devices"
	"github.com/mholtzscher/hearth/internal/modules/household"
)

func TestPairedEntityReadUsesOneObservation(t *testing.T) {
	t.Parallel()
	source := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	view := apiEntityWithState(&devices.State{
		Value: []byte(`21000`), ObservationID: apiObservationID, AdapterReceivedAt: source.Add(time.Second),
		SourceUpdatedAt: &source, ObservedAt: source.Add(2 * time.Second),
	})
	view.Entity.TypeID = devices.EntityTypeTemperatureV1
	view.Entity.Support = []byte(`{}`)
	calls := 0
	router, _ := testAPI(t, &stubDevices{
		getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			calls++
			if calls > 1 {
				view.State.Value = []byte(`22000`)
			}
			return view, nil
		},
	})
	rec := performRequest(router, "/v1/entities/"+string(apiEntityID)+"?include=canonical")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, calls)
	require.Contains(t, rec.Body.String(), `"canonical":{"value":21000,"unit":"mCel"}`)
	require.Contains(t, rec.Body.String(), `"display":{"value":21,"unit":"Cel","label":"°C","fraction_digits":1}`)
	require.Contains(t, rec.Body.String(), `"observation_id":"`+string(apiObservationID)+`"`)
	require.Contains(t, rec.Body.String(), `"source_updated_at":"2026-11-01T09:00:00Z"`)
	require.Contains(t, rec.Body.String(), `"adapter_received_at":"2026-11-01T09:00:01Z"`)
	require.Contains(t, rec.Body.String(), `"observed_at":"2026-11-01T09:00:02Z"`)
	require.NotContains(t, rec.Body.String(), `"quantities"`)
}

func pairedTestAPI(service Devices, system household.UnitSystem) (*echo.Echo, huma.API) {
	router := echo.New()
	api := humaecho.New(router, huma.DefaultConfig("Hearth", "1.0.0"))
	Register(huma.NewGroup(api, "/v1"), service, devices.NewPresenter(system, devices.TableConverter{}), nil)
	return router, api
}

func validateResponseSchema(t *testing.T, api huma.API, pointer, response string) {
	t.Helper()
	schema := responseSchema(t, api, pointer)
	value, err := jsonschema.UnmarshalJSON(strings.NewReader(response))
	require.NoError(t, err)
	require.NoError(t, schema.Validate(value), response)
}

func responseSchema(t *testing.T, api huma.API, pointer string) *jsonschema.Schema {
	t.Helper()
	published, err := json.Marshal(api.OpenAPI())
	require.NoError(t, err)
	document, err := jsonschema.UnmarshalJSON(strings.NewReader(string(published)))
	require.NoError(t, err)
	compiler := jsonschema.NewCompiler()
	require.NoError(t, compiler.AddResource("https://hearth.invalid/openapi", document))
	schema, err := compiler.Compile("https://hearth.invalid/openapi" + pointer)
	require.NoError(t, err)
	return schema
}

func TestPairedEntitySchemaRejectsMixedAndMalformedRepresentations(t *testing.T) {
	t.Parallel()
	view := apiEntityWithState(&devices.State{Value: []byte(`21000`)})
	view.Entity.TypeID, view.Entity.Support = devices.EntityTypeTemperatureV1, []byte(`{}`)
	router, api := testAPI(
		t,
		&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
			return view, nil
		}},
	)
	rec := performRequest(router, "/v1/entities/"+string(apiEntityID)+"?include=canonical")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	schema := responseSchema(
		t,
		api,
		"#/paths/~1v1~1entities~1{entity_id}/get/responses/200/content/application~1json/schema",
	)
	for _, tc := range []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"legacy quantities", func(root map[string]any) { root["quantities"] = map[string]any{} }},
		{"legacy value", func(root map[string]any) { root["state"].(map[string]any)["value"] = 21000 }},
		{"missing display", func(root map[string]any) { delete(root["state"].(map[string]any), "display") }},
		{"nonnumeric reading", func(root map[string]any) {
			root["state"].(map[string]any)["canonical"] = map[string]any{"value": "wrong", "unit": "mCel"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			value, err := jsonschema.UnmarshalJSON(strings.NewReader(rec.Body.String()))
			require.NoError(t, err)
			root := value.(map[string]any)
			tc.mutate(root)
			require.Error(t, schema.Validate(root))
		})
	}
}

func TestPairedEntityShapesAndPublishedSchema(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, value, support string
		kind                 devices.EntityTypeID
		system               household.UnitSystem
		want                 []string
		absent               []string
	}{
		{"US speed", `4.4704`, `{}`, devices.EntityTypeSpeedV1, household.UnitSystemUS,
			[]string{`"canonical":{"value":4.4704,"unit":"m/s"}`, `"display":{"value":10,"unit":"[mi_i]/h","label":"mi/h","fraction_digits":1}`}, nil},
		{"thermostat", `{"mode":"heat","current_temperature":21000}`, `{}`, devices.EntityTypeThermostatV1, household.UnitSystemUS,
			[]string{`"mode":"heat"`, `"value":21000,"unit":"mCel"`, `"value":69.8,"unit":"[degF]"`}, []string{`"target_temperature"`}},
		{"forecast", `{"periods":[{"starts_at":"2026-11-10T12:00:00Z","condition":"clear-day","temperature":2.5},{"starts_at":"2026-11-10T13:00:00Z"}]}`, `{}`, devices.EntityTypeHourlyforecastV1, household.UnitSystemUS,
			[]string{`"condition":"clear-day"`, `"value":2.5,"unit":"Cel"`, `"value":36.5,"unit":"[degF]"`, `"starts_at":"2026-11-10T13:00:00Z"`}, []string{`"wind_speed"`}},
		{"boolean", `true`, `{}`, devices.EntityTypePowerV1, household.UnitSystemMetric,
			[]string{`"canonical":true`, `"display":true`}, nil},
		{"null", "", `{}`, devices.EntityTypeTemperatureV1, household.UnitSystemMetric,
			[]string{`"state":null`}, nil},
		{"event", "", `{}`, devices.EntityTypeEnumeventV1, household.UnitSystemMetric,
			[]string{`"state":null`}, nil},
		{"range", `2.54`, `{"state":{"unit":"mm","quantity_kind":"precipitation_depth","minimum":0,"maximum":254}}`, devices.EntityTypeNumericsensorV1, household.UnitSystemUS,
			[]string{`"value":2.54,"unit":"mm"`, `"range":{"minimum":0,"maximum":10}`}, nil},
		{"large canonical number", `900719925474099312345.0001`, `{"state":{"unit":"opaque","minimum":0,"maximum":1e30}}`, devices.EntityTypeNumericsensorV1, household.UnitSystemMetric,
			[]string{`"value":900719925474099312345.0001,"unit":"opaque"`}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			view := apiEntityWithState(nil)
			view.Entity.TypeID, view.Entity.Support = tc.kind, []byte(tc.support)
			if tc.value != "" {
				view.State = &devices.State{Value: []byte(tc.value), ObservationID: apiObservationID}
			}
			router, api := pairedTestAPI(
				&stubDevices{getEntity: func(context.Context, devices.EntityID) (devices.EntityWithState, error) {
					return view, nil
				}},
				tc.system,
			)
			for _, query := range []string{"?include=canonical", "", "?units=canonical"} {
				rec := performRequest(router, "/v1/entities/"+string(apiEntityID)+query)
				require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
				validateResponseSchema(
					t,
					api,
					"#/paths/~1v1~1entities~1{entity_id}/get/responses/200/content/application~1json/schema",
					rec.Body.String(),
				)
				if query != "?include=canonical" {
					require.Contains(t, rec.Body.String(), `"quantities"`)
					require.Contains(t, rec.Body.String(), `"$schema":"https://example.com/schemas/EntityBody.json"`)
					continue
				}
				for _, want := range tc.want {
					require.Contains(t, rec.Body.String(), want)
				}
				for _, absent := range tc.absent {
					require.NotContains(t, rec.Body.String(), absent)
				}
				require.NotContains(t, rec.Body.String(), `"quantities"`)
			}
		})
	}
}

func TestPairedHistorySizeLimitCountsBothMetricGroups(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		nameLength int
		status     int
	}{
		{devices.HistoryMaxResultBytes/2 - 1000, http.StatusOK},
		{devices.HistoryMaxResultBytes/2 + 1000, http.StatusUnprocessableEntity},
	} {
		t.Run(http.StatusText(tc.status), func(t *testing.T) {
			t.Parallel()
			alias := strings.Repeat("x", tc.nameLength)
			router, _ := testAPI(t, &stubDevices{
				getEntity: historyTemperatureEntity,
				queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
					return devices.HistoryQueryResult{
						EntityID: q.EntityID,
						Range:    q.Range,
						Data: devices.HistorySummaryResult{
							Metrics: devices.HistoryMetricValues{alias: []byte(`1`)},
						},
					}, nil
				},
			})
			req := httptest.NewRequest(
				http.MethodPost,
				"/v1/entities/"+string(apiEntityID)+"/state/history?include=canonical",
				strings.NewReader(historyRequestBody),
			)
			req.Header.Set("Content-Type", "application/json")
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)
			require.Equal(t, tc.status, rec.Code)
			if tc.status != http.StatusOK {
				require.Contains(t, rec.Body.String(), `"code":"query_limit"`)
			}
		})
	}
}

func TestPairedHistoryBucketsAndPublishedSchema(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC)
	calls := 0
	router, api := pairedTestAPI(&stubDevices{
		getEntity: historyTemperatureEntity,
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			calls++
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistoryBucketedSummaryResult{
					Bucket: devices.HistoryBucket{Duration: time.Minute, Alignment: "range_start"},
					Buckets: []devices.HistorySummaryBucket{
						{
							Range:   devices.HistoryRange{From: start, To: start.Add(time.Minute)},
							Metrics: devices.HistoryMetricValues{"custom": []byte(`21000`)},
						},
						{
							Range:   devices.HistoryRange{From: start.Add(time.Minute), To: start.Add(2 * time.Minute)},
							Metrics: devices.HistoryMetricValues{"custom": []byte(`null`)},
						},
					},
				},
			}, nil
		},
	}, household.UnitSystemUS)
	req := httptest.NewRequest(
		http.MethodPost,
		"/v1/entities/"+string(apiEntityID)+"/state/history?include=canonical",
		strings.NewReader(historyRequestBody),
	)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, calls)
	require.Contains(t, rec.Body.String(), `"canonical":{"unit":"mCel","metrics":{"custom":21000}}`)
	require.Contains(
		t,
		rec.Body.String(),
		`"display":{"unit":"[degF]","label":"°F","fraction_digits":1,"metrics":{"custom":69.8}}`,
	)
	require.Contains(t, rec.Body.String(), `"metrics":{"custom":null}`)
	validateResponseSchema(
		t,
		api,
		"#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/responses/200/content/application~1json/schema",
		rec.Body.String(),
	)
}

func TestPairedHistoryAndIncludeValidation(t *testing.T) {
	t.Parallel()
	calls := 0
	router, api := testAPI(t, &stubDevices{
		getEntity: historyTemperatureEntity,
		queryHistory: func(_ context.Context, q devices.HistoryQuery) (devices.HistoryQueryResult, error) {
			calls++
			return devices.HistoryQueryResult{
				EntityID: q.EntityID,
				Range:    q.Range,
				Data: devices.HistorySummaryResult{
					Metrics: devices.HistoryMetricValues{"min": []byte(`21000`), "gap": []byte(`null`)},
				},
			}, nil
		},
	})
	post := func(query string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(
			http.MethodPost,
			"/v1/entities/"+string(apiEntityID)+"/state/history"+query,
			strings.NewReader(historyRequestBody),
		)
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		return rec
	}
	rec := post("?include=canonical")
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	require.Equal(t, 1, calls)
	require.Contains(t, rec.Body.String(), `"canonical":{"unit":"mCel","metrics":{"gap":null,"min":21000}}`)
	require.Contains(
		t,
		rec.Body.String(),
		`"display":{"unit":"Cel","label":"°C","fraction_digits":1,"metrics":{"gap":null,"min":21}}`,
	)
	require.NotContains(t, rec.Body.String(), `"quantity"`)
	validateResponseSchema(
		t,
		api,
		"#/paths/~1v1~1entities~1{entity_id}~1state~1history/post/responses/200/content/application~1json/schema",
		rec.Body.String(),
	)
	for _, query := range []string{
		"?include=bad", "?include=canonical&units=canonical", "?include=", "?include=canonical&include=bad",
	} {
		invalid := post(query)
		require.Equal(t, http.StatusBadRequest, invalid.Code)
		require.Contains(t, invalid.Body.String(), `"code":"invalid_query"`)
		require.Equal(t, http.StatusBadRequest, performRequest(router, "/v1/entities/"+string(apiEntityID)+query).Code)
	}
}
